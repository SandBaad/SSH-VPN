package user

import (
	"fmt"
	"os/exec"
	"sync"
	"time"

	"sshfortress/internal/config"
	"sshfortress/internal/store"
)

// Limiter enforces maximum simultaneous SSH connections per user.
// It runs as a background goroutine and periodically checks for violations.
type Limiter struct {
	mgr      *Manager
	cfg      *config.Config
	db       *store.DB
	stopCh   chan struct{}
	mu       sync.Mutex
	running  bool
	interval time.Duration
}

// NewLimiter creates a new connection limiter.
func NewLimiter(mgr *Manager, cfg *config.Config, db *store.DB) *Limiter {
	interval := time.Duration(cfg.Monitor.RefreshIntervalSecs) * time.Second
	if interval < 2*time.Second {
		interval = 5 * time.Second
	}
	return &Limiter{
		mgr:      mgr,
		cfg:      cfg,
		db:       db,
		stopCh:   make(chan struct{}),
		interval: interval,
	}
}

// Start begins the background connection limiting loop.
func (l *Limiter) Start() {
	l.mu.Lock()
	if l.running {
		l.mu.Unlock()
		return
	}
	l.running = true
	l.mu.Unlock()

	go l.loop()
}

// Stop halts the background limiter.
func (l *Limiter) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running {
		return
	}
	l.running = false
	close(l.stopCh)
}

// IsRunning reports whether the limiter is active.
func (l *Limiter) IsRunning() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

func (l *Limiter) loop() {
	ticker := time.NewTicker(l.interval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.enforce()
		}
	}
}

// enforce checks all users and kills excess sessions.
func (l *Limiter) enforce() {
	users, err := l.db.ListUsers()
	if err != nil {
		return
	}

	sessions := l.mgr.GetActiveSessions()

	// Count sessions per user.
	sessionsByUser := make(map[string][]store.SessionRecord)
	for _, s := range sessions {
		sessionsByUser[s.Username] = append(sessionsByUser[s.Username], s)
	}

	// Check each user against their limit.
	for _, user := range users {
		userSessions := sessionsByUser[user.Username]
		if len(userSessions) > user.MaxConnections {
			excess := len(userSessions) - user.MaxConnections
			// Kill the newest (last) excess sessions.
			for i := len(userSessions) - 1; i >= 0 && excess > 0; i-- {
				pid := userSessions[i].PID
				if pid > 0 {
					exec.Command("kill", "-9", fmt.Sprintf("%d", pid)).Run()
					excess--
				}
			}
		}
	}
}
