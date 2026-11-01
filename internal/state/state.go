// Package state is the live snapshot the daemon publishes for the TUI and
// CLI (/run/sshfortress/state.json): online sessions with bandwidth,
// component health and recent enforcement events.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"sshfortress/internal/config"
	"sshfortress/internal/gateway"
	"sshfortress/internal/session"
	"sshfortress/internal/sysx"
	"sshfortress/internal/udpgw"
)

// Session is a live session with its current throughput.
type Session struct {
	session.Session
	RateBps  uint64 `json:"rate_bps"`  // bytes/s over the last interval
	UDPFlows int    `json:"udp_flows"` // active udpgw flows (calls, DNS…)
}

// Event is an enforcement action, e.g. "kicked alice (limit 2)".
type Event struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

// IOBase lets a restarted daemon continue traffic accounting without
// double-counting sessions that were already open.
type IOBase struct {
	Start time.Time `json:"start"`
	Bytes uint64    `json:"bytes"`
}

// UDPGW describes the UDP gateway.
type UDPGW struct {
	Mode    string            `json:"mode"`
	Ports   []int             `json:"ports"`
	Failed  map[string]string `json:"failed,omitempty"`
	Stats   *udpgw.Stats      `json:"stats,omitempty"`
	Healthy bool              `json:"healthy"`
}

// State is the published snapshot.
type State struct {
	Version   string    `json:"version"`
	StartedAt time.Time `json:"started_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Interval  int       `json:"interval_secs"`

	Sessions []Session                 `json:"sessions"`
	UDPGW    *UDPGW                    `json:"udpgw,omitempty"`
	Gateway  *gateway.Stats            `json:"gateway,omitempty"`
	Peers    map[int]gateway.PeerEntry `json:"peers,omitempty"`
	IOBase   map[int]IOBase            `json:"io_base,omitempty"`
	Events   []Event                   `json:"events,omitempty"`
	Warnings []string                  `json:"warnings,omitempty"`
	TotalBps uint64                    `json:"total_bps"`
}

// Path is where the snapshot lives.
func Path() string { return filepath.Join(config.RunDir, "state.json") }

// Read loads the snapshot.
func Read() (*State, error) {
	data, err := os.ReadFile(Path())
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Write stores the snapshot atomically (readable by root only: it contains
// client IPs).
func Write(s *State) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return sysx.WriteFileAtomic(Path(), data, 0o600)
}

// Fresh reports whether the daemon updated the snapshot recently.
func (s *State) Fresh() bool {
	iv := s.Interval
	if iv <= 0 {
		iv = 5
	}
	return time.Since(s.UpdatedAt) < time.Duration(iv*3+5)*time.Second
}
