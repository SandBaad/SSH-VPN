// Package user manages VPN accounts: the Linux user, its password, expiry,
// connection limit, traffic quota and lock state.
package user

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"sshfortress/internal/config"
	"sshfortress/internal/security"
	"sshfortress/internal/session"
	"sshfortress/internal/store"
	"sshfortress/internal/sysx"
)

// Manager handles the user lifecycle.
type Manager struct {
	cfg     *config.Config
	db      *store.DB
	Scanner *session.Scanner
}

// NewManager creates a manager.
func NewManager(cfg *config.Config, db *store.DB) *Manager {
	return &Manager{cfg: cfg, db: db, Scanner: &session.Scanner{UDPGWPorts: cfg.UDPGW.Ports}}
}

// CreateUserOpts holds options for a new user.
type CreateUserOpts struct {
	Username       string
	Password       string // empty = generate one
	MaxConnections int
	ExpirationDays int
	TrafficGB      float64 // 0 = default, <0 = unlimited
	Notes          string
}

// Status values.
const (
	StatusActive  = "active"
	StatusExpired = "expired"
	StatusLocked  = "locked"
	StatusQuota   = "quota"
)

// UserInfo combines the stored record with live data.
type UserInfo struct {
	store.UserRecord
	ActiveSessions int               `json:"active_sessions"`
	IsExpired      bool              `json:"is_expired"`
	Status         string            `json:"status"`
	DaysLeft       int               `json:"days_left"`
	Sessions       []session.Session `json:"sessions,omitempty"`
}

func statusOf(u *store.UserRecord, now time.Time) string {
	switch {
	case u.Disabled && u.LockReason == store.LockQuota:
		return StatusQuota
	case u.IsExpired(now) || (u.Disabled && u.LockReason == store.LockExpired):
		return StatusExpired
	case u.Disabled:
		return StatusLocked
	case u.QuotaExceeded():
		return StatusQuota
	}
	return StatusActive
}

// shadowDate is the day *after* the precise expiry: PAM only knows whole
// days, the daemon enforces the exact time, so PAM must not cut users off
// hours early. It still blocks logins if the daemon is down.
func shadowDate(t time.Time) string {
	return t.Add(24 * time.Hour).Format("2006-01-02")
}

// EnsureGroup creates the VPN group if it doesn't exist.
func (m *Manager) EnsureGroup() error {
	g := m.cfg.UserDefaults.Group
	if data, err := os.ReadFile("/etc/group"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, g+":") {
				return nil
			}
		}
	}
	_, err := sysx.Run("groupadd", "--system", g)
	return err
}

func systemUserExists(name string) bool {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return false
	}
	return strings.Contains("\n"+string(data), "\n"+name+":")
}

// Create creates the Linux account and the record. It returns the record
// and the (possibly generated) password.
func (m *Manager) Create(opts CreateUserOpts) (*store.UserRecord, string, error) {
	if err := sysx.RequireRoot(); err != nil {
		return nil, "", err
	}
	if err := security.ValidateUsername(opts.Username); err != nil {
		return nil, "", err
	}
	if opts.Password == "" {
		pw, err := security.GenerateRandomPassword(10)
		if err != nil {
			return nil, "", err
		}
		opts.Password = pw
	}
	if err := security.ValidatePassword(opts.Password); err != nil {
		return nil, "", err
	}
	if _, err := m.db.GetUser(opts.Username); err == nil {
		return nil, "", fmt.Errorf("user %q already exists", opts.Username)
	}
	if systemUserExists(opts.Username) {
		return nil, "", fmt.Errorf("a Linux account named %q already exists", opts.Username)
	}
	d := m.cfg.UserDefaults
	if opts.MaxConnections <= 0 {
		opts.MaxConnections = d.DefaultMaxConnections
	}
	if opts.ExpirationDays <= 0 {
		opts.ExpirationDays = d.DefaultExpirationDays
	}
	var limit int64
	switch {
	case opts.TrafficGB > 0:
		limit = config.GB(opts.TrafficGB)
	case opts.TrafficGB == 0 && d.DefaultTrafficGB > 0:
		limit = config.GB(d.DefaultTrafficGB)
	}
	if err := m.EnsureGroup(); err != nil {
		return nil, "", fmt.Errorf("create group: %w", err)
	}
	now := time.Now()
	expires := now.AddDate(0, 0, opts.ExpirationDays)
	shell := d.Shell
	if shell == "" {
		shell = "/bin/false"
	}
	if _, err := sysx.Run("useradd", "-M", "-N", "-g", d.Group, "-s", shell,
		"-e", shadowDate(expires), "-c", "SSH-VPN user", opts.Username); err != nil {
		return nil, "", err
	}
	if _, err := sysx.RunInput(sysx.DefaultTimeout, opts.Username+":"+opts.Password+"\n", "chpasswd"); err != nil {
		_, _ = sysx.Run("userdel", "-f", opts.Username)
		return nil, "", err
	}
	rec := &store.UserRecord{
		Username:       opts.Username,
		MaxConnections: opts.MaxConnections,
		ExpiresAt:      expires,
		CreatedAt:      now,
		Notes:          opts.Notes,
		TrafficLimit:   limit,
	}
	if err := m.db.PutUser(rec); err != nil {
		_, _ = sysx.Run("userdel", "-f", opts.Username)
		return nil, "", fmt.Errorf("save user: %w", err)
	}
	return rec, opts.Password, nil
}

// Delete kills the user's sessions and removes the account.
func (m *Manager) Delete(name string) error {
	if err := sysx.RequireRoot(); err != nil {
		return err
	}
	if err := security.ValidateUsername(name); err != nil {
		return err
	}
	if _, err := m.db.GetUser(name); err != nil {
		return err
	}
	_ = m.lockSystem(name) // stop new logins while we clean up
	_, _ = m.Kick(name)
	if systemUserExists(name) {
		if _, err := sysx.Run("userdel", "-f", name); err != nil {
			return err
		}
	}
	return m.db.DeleteUser(name)
}

// SetPassword sets a new password (generated when pw is empty).
func (m *Manager) SetPassword(name, pw string) (string, error) {
	if err := sysx.RequireRoot(); err != nil {
		return "", err
	}
	if _, err := m.db.GetUser(name); err != nil {
		return "", err
	}
	if pw == "" {
		var err error
		if pw, err = security.GenerateRandomPassword(10); err != nil {
			return "", err
		}
	}
	if err := security.ValidatePassword(pw); err != nil {
		return "", err
	}
	if _, err := sysx.RunInput(sysx.DefaultTimeout, name+":"+pw+"\n", "chpasswd"); err != nil {
		return "", err
	}
	// chpasswd clears the lock marker; re-lock if the account is locked.
	if u, err := m.db.GetUser(name); err == nil && u.Disabled {
		_ = m.lockSystem(name)
	}
	return pw, nil
}

// Renew extends the account by days, counting from now if it already
// expired, and unlocks it if it was locked for expiry.
func (m *Manager) Renew(name string, days int) (*store.UserRecord, error) {
	if days <= 0 {
		return nil, errors.New("days must be positive")
	}
	now := time.Now()
	u, err := m.db.UpdateUser(name, func(u *store.UserRecord) error {
		base := u.ExpiresAt
		if base.Before(now) {
			base = now
		}
		u.ExpiresAt = base.AddDate(0, 0, days)
		if u.Disabled && u.LockReason == store.LockExpired {
			u.Disabled, u.LockReason = false, ""
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, m.syncSystem(u)
}

// SetExpiry sets an exact expiry time.
func (m *Manager) SetExpiry(name string, t time.Time) (*store.UserRecord, error) {
	u, err := m.db.UpdateUser(name, func(u *store.UserRecord) error {
		u.ExpiresAt = t
		if u.Disabled && u.LockReason == store.LockExpired && t.After(time.Now()) {
			u.Disabled, u.LockReason = false, ""
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, m.syncSystem(u)
}

// SetMaxConnections updates the simultaneous-connection limit.
func (m *Manager) SetMaxConnections(name string, n int) error {
	if n < 1 {
		return errors.New("max connections must be at least 1")
	}
	_, err := m.db.UpdateUser(name, func(u *store.UserRecord) error { u.MaxConnections = n; return nil })
	return err
}

// SetTrafficLimit sets the quota in bytes (0 = unlimited).
func (m *Manager) SetTrafficLimit(name string, bytes int64) (*store.UserRecord, error) {
	if bytes < 0 {
		bytes = 0
	}
	u, err := m.db.UpdateUser(name, func(u *store.UserRecord) error {
		u.TrafficLimit = bytes
		if u.Disabled && u.LockReason == store.LockQuota && !u.QuotaExceeded() {
			u.Disabled, u.LockReason = false, ""
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, m.syncSystem(u)
}

// ResetTraffic zeroes the usage counter (and lifts a quota lock).
func (m *Manager) ResetTraffic(name string) (*store.UserRecord, error) {
	u, err := m.db.UpdateUser(name, func(u *store.UserRecord) error {
		u.TrafficUsed = 0
		if u.Disabled && u.LockReason == store.LockQuota {
			u.Disabled, u.LockReason = false, ""
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u, m.syncSystem(u)
}

// Lock disables the account and disconnects it.
func (m *Manager) Lock(name, reason string) error {
	if reason == "" {
		reason = store.LockManual
	}
	u, err := m.db.UpdateUser(name, func(u *store.UserRecord) error {
		u.Disabled, u.LockReason = true, reason
		return nil
	})
	if err != nil {
		return err
	}
	if err := m.syncSystem(u); err != nil {
		return err
	}
	_, _ = m.Kick(name)
	return nil
}

// Unlock re-enables a manually locked account. Expired or over-quota
// accounts must be renewed / reset instead.
func (m *Manager) Unlock(name string) error {
	now := time.Now()
	u, err := m.db.UpdateUser(name, func(u *store.UserRecord) error {
		if u.IsExpired(now) {
			return fmt.Errorf("%s expired on %s — renew it instead", name, u.ExpiresAt.Format("2006-01-02"))
		}
		if u.QuotaExceeded() {
			return fmt.Errorf("%s used its whole traffic quota — reset traffic or raise the limit", name)
		}
		u.Disabled, u.LockReason = false, ""
		return nil
	})
	if err != nil {
		return err
	}
	return m.syncSystem(u)
}

// syncSystem makes the Linux account match the record.
func (m *Manager) syncSystem(u *store.UserRecord) error {
	if os.Geteuid() != 0 || !systemUserExists(u.Username) {
		return nil
	}
	if u.Disabled {
		return m.lockSystem(u.Username)
	}
	if _, err := sysx.Run("usermod", "-U", u.Username); err != nil && !strings.Contains(err.Error(), "unlocking") {
		// usermod -U fails harmlessly when the password isn't locked
		_ = err
	}
	_, err := sysx.Run("chage", "-E", shadowDate(u.ExpiresAt), u.Username)
	return err
}

func (m *Manager) lockSystem(name string) error {
	if os.Geteuid() != 0 || !systemUserExists(name) {
		return nil
	}
	_, err := sysx.Run("usermod", "-L", name)
	return err
}

// Kick disconnects all sessions of a user and returns how many.
func (m *Manager) Kick(name string) (int, error) {
	sessions, err := m.Scanner.Scan()
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range sessions {
		if s.Username == name {
			if err := session.Kill(s.PID); err == nil {
				n++
			}
		}
	}
	return n, nil
}

// List returns all users with live session data.
func (m *Manager) List() ([]UserInfo, error) {
	records, err := m.db.ListUsers()
	if err != nil {
		return nil, err
	}
	var byUser map[string][]session.Session
	if sessions, err := m.Scanner.Scan(); err == nil {
		byUser = session.ByUser(sessions)
	}
	now := time.Now()
	out := make([]UserInfo, 0, len(records))
	for i := range records {
		out = append(out, infoOf(&records[i], byUser[records[i].Username], now))
	}
	return out, nil
}

// ListUsers is kept for v1 API compatibility.
func (m *Manager) ListUsers() ([]UserInfo, error) { return m.List() }

// Get returns one user with live data.
func (m *Manager) Get(name string) (*UserInfo, error) {
	rec, err := m.db.GetUser(name)
	if err != nil {
		return nil, err
	}
	var mine []session.Session
	if sessions, err := m.Scanner.Scan(); err == nil {
		mine = session.ByUser(sessions)[name]
	}
	info := infoOf(rec, mine, time.Now())
	return &info, nil
}

func infoOf(r *store.UserRecord, sessions []session.Session, now time.Time) UserInfo {
	days := int(math.Ceil(r.ExpiresAt.Sub(now).Hours() / 24))
	if r.ExpiresAt.Before(now) {
		days = 0
	}
	return UserInfo{
		UserRecord:     *r,
		ActiveSessions: len(sessions),
		IsExpired:      r.IsExpired(now),
		Status:         statusOf(r, now),
		DaysLeft:       days,
		Sessions:       sessions,
	}
}

// PurgeExpired deletes users that expired more than graceDays ago.
func (m *Manager) PurgeExpired(graceDays int) ([]string, error) {
	records, err := m.db.ListUsers()
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().AddDate(0, 0, -graceDays)
	var removed []string
	for _, r := range records {
		if !r.ExpiresAt.IsZero() && r.ExpiresAt.Before(cutoff) {
			if err := m.Delete(r.Username); err == nil {
				removed = append(removed, r.Username)
			}
		}
	}
	return removed, nil
}

// MigrateGroups moves users created by v1 (own primary group) into the VPN
// group, which the sshd Match block and the firewall rely on.
func (m *Manager) MigrateGroups() (int, error) {
	if os.Geteuid() != 0 {
		return 0, nil
	}
	if err := m.EnsureGroup(); err != nil {
		return 0, err
	}
	records, err := m.db.ListUsers()
	if err != nil {
		return 0, err
	}
	out, _ := sysx.Run("getent", "group", m.cfg.UserDefaults.Group)
	f := strings.Split(out, ":")
	if len(f) < 3 {
		return 0, fmt.Errorf("group %s not found", m.cfg.UserDefaults.Group)
	}
	gid := f[2]
	passwd, _ := os.ReadFile("/etc/passwd")
	n := 0
	for _, line := range strings.Split(string(passwd), "\n") {
		p := strings.Split(line, ":")
		if len(p) < 4 {
			continue
		}
		for _, r := range records {
			if p[0] == r.Username && p[3] != gid {
				if _, err := sysx.Run("usermod", "-g", m.cfg.UserDefaults.Group, r.Username); err == nil {
					n++
				}
			}
		}
	}
	return n, nil
}
