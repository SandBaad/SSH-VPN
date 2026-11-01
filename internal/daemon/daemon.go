// Package daemon is the long-running service (`sshfortress daemon`):
//
//   - built-in UDP gateway (or watchdog for external badvpn-udpgw)
//   - multi-protocol gateway (SSH / WebSocket / TLS on any port)
//   - per-user traffic accounting and quotas
//   - simultaneous-connection limits, precise expiry, lock enforcement
//   - publishes live state for the TUI/CLI
//
// v1 had a limiter that was never started; here everything runs under
// systemd with Restart=always, so limits hold even when no admin is
// logged in.
package daemon

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"sshfortress/internal/badvpn"
	"sshfortress/internal/buildinfo"
	"sshfortress/internal/config"
	"sshfortress/internal/firewall"
	"sshfortress/internal/gateway"
	"sshfortress/internal/netopt"
	"sshfortress/internal/session"
	"sshfortress/internal/sshd"
	"sshfortress/internal/state"
	"sshfortress/internal/store"
	"sshfortress/internal/sysx"
	"sshfortress/internal/udpgw"
	"sshfortress/internal/user"
)

const maxEvents = 50

// Daemon is the background service.
type Daemon struct {
	cfg   *config.Config
	db    *store.DB
	users *user.Manager
	log   *log.Logger

	udp      *udpgw.Server
	udpState *state.UDPGW
	gw       *gateway.Gateway

	mu       sync.Mutex
	prev     map[int]state.IOBase
	lastTick time.Time
	started  time.Time
	events   []state.Event
	warnings []string
	killing  map[int]time.Time
}

// New creates a daemon.
func New(cfg *config.Config, db *store.DB) *Daemon {
	return &Daemon{
		cfg:     cfg,
		db:      db,
		users:   user.NewManager(cfg, db),
		log:     log.New(os.Stderr, "", 0),
		prev:    map[int]state.IOBase{},
		killing: map[int]time.Time{},
	}
}

func (d *Daemon) logf(format string, args ...any) { d.log.Printf(format, args...) }

func (d *Daemon) warn(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	d.log.Print("WARN " + msg)
	d.mu.Lock()
	d.warnings = append(d.warnings, msg)
	d.mu.Unlock()
}

func (d *Daemon) event(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	d.log.Print(msg)
	d.mu.Lock()
	d.events = append(d.events, state.Event{Time: time.Now(), Text: msg})
	if len(d.events) > maxEvents {
		d.events = d.events[len(d.events)-maxEvents:]
	}
	d.mu.Unlock()
}

// Run starts every component and blocks until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := sysx.RequireRoot(); err != nil {
		return err
	}
	d.started = time.Now()
	d.logf("SSH-VPN daemon %s starting", buildinfo.Version)
	if err := os.MkdirAll(config.RunDir, 0o755); err != nil {
		return err
	}
	old, _ := state.Read()
	if old != nil {
		d.prev = old.IOBase
		if d.prev == nil {
			d.prev = map[int]state.IOBase{}
		}
		d.events = old.Events
	}

	if err := d.users.EnsureGroup(); err != nil {
		d.warn("create group %s: %v", d.cfg.UserDefaults.Group, err)
	}
	if n, err := d.users.MigrateGroups(); err != nil {
		d.warn("migrate users to group: %v", err)
	} else if n > 0 {
		d.logf("moved %d v1 users into group %s", n, d.cfg.UserDefaults.Group)
	}
	if d.cfg.Network.AutoOptimize {
		if res, err := netopt.Apply(d.cfg); err != nil {
			d.warn("network optimization: %v", err)
		} else {
			d.logf("network profile: %d/%d settings active", res.Applied, res.Total)
			for _, w := range res.Warnings {
				d.warn("network: %s", w)
			}
		}
	}
	if err := firewall.Apply(d.cfg); err != nil {
		d.warn("%v", err)
	}
	d.startUDPGW()
	if d.cfg.UDPGW.Enabled {
		time.Sleep(200 * time.Millisecond) // let listeners (or badvpn) come up
		d.udpState.Healthy = d.udpHealthy()
	}
	d.startGateway(old)
	defer func() {
		if d.udp != nil {
			d.udp.Close()
		}
		if d.gw != nil {
			d.gw.Close() // sessions stay alive: they belong to sshd
		}
		d.publish(nil)
		d.logf("daemon stopped")
	}()

	interval := time.Duration(d.cfg.Limiter.IntervalSecs) * time.Second
	tick := time.NewTicker(interval)
	defer tick.Stop()
	watch := time.NewTicker(30 * time.Second)
	defer watch.Stop()
	d.tick()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			d.tick()
		case <-watch.C:
			d.watchdog()
		}
	}
}

// ─── components ────────────────────────────────────────────────────────────

func (d *Daemon) udpAddrs() []string {
	addrs := make([]string, 0, len(d.cfg.UDPGW.Ports))
	for _, p := range d.cfg.UDPGW.Ports {
		addrs = append(addrs, net.JoinHostPort(d.cfg.UDPGW.Listen, strconv.Itoa(p)))
	}
	return addrs
}

func (d *Daemon) startUDPGW() {
	c := d.cfg.UDPGW
	d.udpState = &state.UDPGW{Mode: c.Mode, Ports: c.Ports}
	if !c.Enabled {
		d.udpState.Mode = "off"
		return
	}
	if c.Mode == "badvpn" {
		if err := badvpn.Apply(d.cfg); err != nil {
			d.warn("badvpn-udpgw: %v", err)
		}
		return
	}
	// builtin: the v1 badvpn service would hold our ports — retire it.
	badvpn.Disable()
	dns := udpgw.SystemDNS
	if c.DNS != "" {
		dns = udpgw.StaticDNS(c.DNS)
	}
	d.udp = udpgw.NewServer(udpgw.Config{
		MaxClients: c.MaxClients,
		MaxFlows:   c.MaxConnectionsPerClient,
		FlowIdle:   time.Duration(c.FlowIdleSecs) * time.Second,
		ClientIdle: 90 * time.Second,
		DNS:        dns,
		Logf:       d.logf,
	})
	failed := d.udp.Listen(d.udpAddrs())
	for addr, err := range failed {
		if d.udpState.Failed == nil {
			d.udpState.Failed = map[string]string{}
		}
		d.udpState.Failed[addr] = err.Error()
		d.warn("udpgw %s: %v", addr, err)
	}
	d.logf("udpgw (built-in) listening on %v", d.udpAddrs())
}

func (d *Daemon) startGateway(old *state.State) {
	c := d.cfg.Gateway
	if !c.Enabled || len(c.Ports) == 0 {
		return
	}
	sm := sshd.New(d.cfg)
	bin := sm.Binary()
	if bin == "" {
		d.warn("gateway: sshd binary not found")
		return
	}
	gw, err := gateway.New(gateway.Config{
		Listen:           c.Listen,
		Ports:            c.Ports,
		TLSCert:          c.TLSCert,
		TLSKey:           c.TLSKey,
		CertDir:          filepath.Join(filepath.Dir(d.cfg.Path()), "tls"),
		Host:             d.cfg.Server.Host,
		MaxConnections:   c.MaxConnections,
		HandshakeTimeout: time.Duration(c.HandshakeTimeoutSecs) * time.Second,
		LoginGrace:       time.Duration(d.cfg.SSH.LoginGraceTime) * time.Second,
		Backend:          gateway.SSHDBackend{Path: bin, Config: d.cfg.SSH.ConfigPath},
		Logf:             d.logf,
	})
	if err != nil {
		d.warn("%v", err)
		return
	}
	if old != nil && old.Peers != nil {
		gw.RestorePeers(old.Peers)
	}
	if err := gw.Start(); err != nil {
		d.warn("%v", err)
	}
	d.gw = gw
	d.users.Scanner.PeerLookup = gw.Peer
	d.logf("gateway listening on %v (SSH, HTTP payload, WebSocket, TLS)", gw.Stats().Ports)
}

func (d *Daemon) udpHealthy() bool {
	for _, a := range d.udpAddrs() {
		if c, err := net.DialTimeout("tcp", a, 2*time.Second); err == nil {
			c.Close()
			return true
		}
	}
	return false
}

// watchdog restarts the UDP gateway if it stops answering.
func (d *Daemon) watchdog() {
	if !d.cfg.UDPGW.Enabled {
		return
	}
	healthy := d.udpHealthy()
	d.mu.Lock()
	was := d.udpState.Healthy
	d.udpState.Healthy = healthy
	d.mu.Unlock()
	if healthy {
		return
	}
	if was { // only log the transition, not every retry
		d.event("UDP gateway stopped answering — restarting it")
	}
	if d.cfg.UDPGW.Mode == "badvpn" {
		_ = sysx.Systemctl("restart", badvpn.Unit)
		return
	}
	if d.udp != nil {
		d.udp.Close()
	}
	d.startUDPGW()
	d.udpState.Healthy = d.udpHealthy()
}

// ─── accounting & enforcement ──────────────────────────────────────────────

func (d *Daemon) tick() {
	now := time.Now()
	recs, err := d.db.ListUsers()
	if err != nil {
		d.warn("read users: %v", err)
		return
	}
	byName := make(map[string]*store.UserRecord, len(recs))
	for i := range recs {
		byName[recs[i].Username] = &recs[i]
	}
	sessions, err := d.users.Scanner.Scan()
	if err != nil {
		d.warn("scan sessions: %v", err)
		return
	}

	dt := now.Sub(d.lastTick).Seconds()
	firstTick := d.lastTick.IsZero()
	d.lastTick = now

	// Traffic: per-session byte counters → per-user deltas.
	deltas := map[string]int64{}
	next := make(map[int]state.IOBase, len(sessions))
	live := make([]state.Session, 0, len(sessions))
	var total uint64
	udpByPort := map[int]int{}
	if d.udp != nil {
		for _, c := range d.udp.Clients() {
			udpByPort[c.RemotePort] += c.Flows
		}
	}
	for _, s := range sessions {
		ls := state.Session{Session: s}
		for _, p := range s.UDPGWPorts {
			ls.UDPFlows += udpByPort[p]
		}
		if p, ok := d.prev[s.PID]; ok && p.Start.Equal(s.StartedAt) {
			if s.Bytes >= p.Bytes {
				delta := s.Bytes - p.Bytes
				deltas[s.Username] += int64(delta)
				if dt > 0 && !firstTick {
					ls.RateBps = uint64(float64(delta) / dt)
				}
			}
		} else if !firstTick || s.StartedAt.After(d.started) {
			// session opened since the last pass: count all of it
			deltas[s.Username] += int64(s.Bytes)
		}
		total += ls.RateBps
		next[s.PID] = state.IOBase{Start: s.StartedAt, Bytes: s.Bytes}
		live = append(live, ls)
	}
	d.prev = next
	managed := map[string]int64{}
	for name, delta := range deltas {
		if u, ok := byName[name]; ok {
			managed[name] = delta
			u.TrafficUsed += delta
		}
	}
	if err := d.db.AddTraffic(managed, now); err != nil {
		d.warn("save traffic: %v", err)
	}

	// Enforcement.
	byUser := session.ByUser(sessions)
	for name, u := range byName {
		ss := byUser[name]
		switch {
		case !u.Disabled && u.IsExpired(now):
			if err := d.users.Lock(name, store.LockExpired); err == nil {
				d.event("locked %s: expired", name)
			}
		case !u.Disabled && u.QuotaExceeded():
			if err := d.users.Lock(name, store.LockQuota); err == nil {
				d.event("locked %s: traffic quota used up", name)
			}
		case u.Disabled && len(ss) > 0:
			for _, s := range ss {
				d.kill(s, "account is "+orDefault(u.LockReason, "locked"))
			}
		case d.cfg.Limiter.Enabled && u.MaxConnections > 0 && len(ss) > u.MaxConnections:
			excess := len(ss) - u.MaxConnections
			victims := ss[:excess] // oldest first: a phone that switched networks reconnects
			if d.cfg.Limiter.Policy == "newest" {
				victims = ss[len(ss)-excess:]
			}
			for _, s := range victims {
				d.kill(s, fmt.Sprintf("over limit %d", u.MaxConnections))
			}
		}
	}
	d.publish(&publishData{sessions: live, total: total})
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// kill disconnects a session (once; Kill waits for the process).
func (d *Daemon) kill(s session.Session, why string) {
	d.mu.Lock()
	if t, ok := d.killing[s.PID]; ok && time.Since(t) < 10*time.Second {
		d.mu.Unlock()
		return
	}
	d.killing[s.PID] = time.Now()
	for pid, t := range d.killing {
		if time.Since(t) > time.Minute {
			delete(d.killing, pid)
		}
	}
	d.mu.Unlock()
	d.event("disconnected %s from %s: %s", s.Username, orDefault(s.ClientIP, "?"), why)
	go func() { _ = session.Kill(s.PID) }()
}

type publishData struct {
	sessions []state.Session
	total    uint64
}

func (d *Daemon) publish(p *publishData) {
	d.mu.Lock()
	st := &state.State{
		Version:   buildinfo.Version,
		StartedAt: d.started,
		UpdatedAt: time.Now(),
		Interval:  d.cfg.Limiter.IntervalSecs,
		IOBase:    d.prev,
		Events:    append([]state.Event(nil), d.events...),
		Warnings:  append([]string(nil), d.warnings...),
		UDPGW:     d.udpState,
	}
	d.mu.Unlock()
	if p != nil {
		st.Sessions = p.sessions
		st.TotalBps = p.total
	}
	if st.UDPGW != nil && d.udp != nil {
		s := d.udp.Stats()
		cp := *st.UDPGW
		cp.Stats = &s
		st.UDPGW = &cp
	}
	if d.gw != nil {
		gs := d.gw.Stats()
		st.Gateway = &gs
		st.Peers = d.gw.Peers()
	}
	if p == nil { // shutting down: keep sessions for continuity, mark not live
		st.UpdatedAt = time.Time{}
	}
	if err := state.Write(st); err != nil {
		d.log.Printf("WARN write state: %v", err)
	}
}
