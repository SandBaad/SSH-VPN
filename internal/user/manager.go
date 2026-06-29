// Package user provides SSH user management including creation, deletion,
// modification, session monitoring, and connection limiting.
package user

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"sshfortress/internal/config"
	"sshfortress/internal/security"
	"sshfortress/internal/store"
)

// Manager handles SSH user lifecycle operations.
type Manager struct {
	cfg *config.Config
	db  *store.DB
}

// NewManager creates a new user manager.
func NewManager(cfg *config.Config, db *store.DB) *Manager {
	return &Manager{cfg: cfg, db: db}
}

// CreateUserOpts holds options for creating a new SSH user.
type CreateUserOpts struct {
	Username       string
	Password       string
	MaxConnections int
	ExpirationDays int
	Notes          string
}

// UserInfo holds combined information about a user from the OS and database.
type UserInfo struct {
	Username       string
	MaxConnections int
	ExpiresAt      time.Time
	CreatedAt      time.Time
	Disabled       bool
	Notes          string
	ActiveSessions int
	IsExpired      bool
}

// CreateUser creates a new SSH user on the system and records it in the database.
func (m *Manager) CreateUser(opts CreateUserOpts) error {
	// Validate inputs.
	if err := security.ValidateUsername(opts.Username); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}
	if err := security.ValidatePassword(opts.Password); err != nil {
		return fmt.Errorf("invalid password: %w", err)
	}

	if opts.MaxConnections <= 0 {
		opts.MaxConnections = m.cfg.UserDefaults.DefaultMaxConnections
	}
	if opts.ExpirationDays <= 0 {
		opts.ExpirationDays = m.cfg.UserDefaults.DefaultExpirationDays
	}

	expiresAt := time.Now().AddDate(0, 0, opts.ExpirationDays)
	expireStr := expiresAt.Format("2006-01-02")
	shell := m.cfg.UserDefaults.Shell
	if shell == "" {
		shell = "/bin/false"
	}

	// Create system user with useradd — no shell interpolation.
	cmd := exec.Command("useradd",
		"-M",                    // No home directory
		"-s", shell,             // Restricted shell
		"-e", expireStr,         // Expiration date
		opts.Username,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("useradd failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Set password using chpasswd (reads from stdin — no shell interpolation).
	chpCmd := exec.Command("chpasswd")
	chpCmd.Stdin = strings.NewReader(fmt.Sprintf("%s:%s", opts.Username, opts.Password))
	if output, err := chpCmd.CombinedOutput(); err != nil {
		// Rollback: remove the user if password set fails.
		exec.Command("userdel", opts.Username).Run()
		return fmt.Errorf("chpasswd failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Record in database.
	record := &store.UserRecord{
		Username:       opts.Username,
		MaxConnections: opts.MaxConnections,
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now(),
		Notes:          opts.Notes,
	}
	if err := m.db.PutUser(record); err != nil {
		// Rollback system user.
		exec.Command("userdel", opts.Username).Run()
		return fmt.Errorf("database record failed: %w", err)
	}

	return nil
}

// DeleteUser removes a user from the system and database.
func (m *Manager) DeleteUser(username string) error {
	if err := security.ValidateUsername(username); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// Kill active sessions.
	m.KillUserSessions(username)

	// Remove from system.
	cmd := exec.Command("userdel", "-r", username)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Not fatal — user might not exist in OS but exist in DB.
		_ = output
	}

	// Remove from database.
	return m.db.DeleteUser(username)
}

// ChangePassword changes a user's password.
func (m *Manager) ChangePassword(username, newPassword string) error {
	if err := security.ValidateUsername(username); err != nil {
		return err
	}
	if err := security.ValidatePassword(newPassword); err != nil {
		return err
	}

	cmd := exec.Command("chpasswd")
	cmd.Stdin = strings.NewReader(fmt.Sprintf("%s:%s", username, newPassword))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chpasswd failed: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// SetExpiration changes a user's expiration date.
func (m *Manager) SetExpiration(username string, expiresAt time.Time) error {
	if err := security.ValidateUsername(username); err != nil {
		return err
	}

	expireStr := expiresAt.Format("2006-01-02")
	cmd := exec.Command("chage", "-E", expireStr, username)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("chage failed: %s: %w", strings.TrimSpace(string(output)), err)
	}

	// Update database.
	record, err := m.db.GetUser(username)
	if err != nil {
		return err
	}
	record.ExpiresAt = expiresAt
	return m.db.PutUser(record)
}

// SetMaxConnections updates the max simultaneous connections for a user.
func (m *Manager) SetMaxConnections(username string, maxConn int) error {
	if maxConn < 1 {
		return fmt.Errorf("max connections must be at least 1")
	}

	record, err := m.db.GetUser(username)
	if err != nil {
		return err
	}
	record.MaxConnections = maxConn
	return m.db.PutUser(record)
}

// ListUsers returns information about all managed users.
func (m *Manager) ListUsers() ([]UserInfo, error) {
	records, err := m.db.ListUsers()
	if err != nil {
		return nil, err
	}

	activeSessions := m.GetActiveSessions()
	sessionCounts := make(map[string]int)
	for _, s := range activeSessions {
		sessionCounts[s.Username]++
	}

	var users []UserInfo
	now := time.Now()
	for _, r := range records {
		users = append(users, UserInfo{
			Username:       r.Username,
			MaxConnections: r.MaxConnections,
			ExpiresAt:      r.ExpiresAt,
			CreatedAt:      r.CreatedAt,
			Disabled:       r.Disabled,
			Notes:          r.Notes,
			ActiveSessions: sessionCounts[r.Username],
			IsExpired:      r.ExpiresAt.Before(now),
		})
	}
	return users, nil
}

// KillUserSessions terminates all SSH sessions for a given user.
func (m *Manager) KillUserSessions(username string) {
	// Use pkill to terminate all processes for the user.
	exec.Command("pkill", "-u", username).Run()
}

// RemoveExpiredUsers removes all users whose accounts have expired.
func (m *Manager) RemoveExpiredUsers() ([]string, error) {
	records, err := m.db.ListUsers()
	if err != nil {
		return nil, err
	}

	var removed []string
	now := time.Now()
	for _, r := range records {
		if r.ExpiresAt.Before(now) {
			if err := m.DeleteUser(r.Username); err == nil {
				removed = append(removed, r.Username)
			}
		}
	}
	return removed, nil
}

// GetActiveSessions returns currently active SSH sessions by parsing `ss` output.
func (m *Manager) GetActiveSessions() []store.SessionRecord {
	var sessions []store.SessionRecord

	// Use `ss` to find established SSH connections.
	cmd := exec.Command("ss", "-tnp", "state", "established")
	output, err := cmd.Output()
	if err != nil {
		return sessions
	}

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "sshd") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		// Parse the peer address.
		peerAddr := fields[4]
		clientIP := peerAddr
		if idx := strings.LastIndex(peerAddr, ":"); idx > 0 {
			clientIP = peerAddr[:idx]
		}

		// Extract PID and username from the process info field.
		procInfo := fields[5]
		pid := extractPID(procInfo)
		username := getUserForPID(pid)

		if username != "" {
			sessions = append(sessions, store.SessionRecord{
				Username:  username,
				PID:       pid,
				ClientIP:  clientIP,
				LoginTime: time.Now(), // Approximation — real time requires utmp parsing.
			})
		}
	}
	return sessions
}

func extractPID(procInfo string) int {
	// Format: users:(("sshd",pid=1234,fd=3))
	if idx := strings.Index(procInfo, "pid="); idx >= 0 {
		rest := procInfo[idx+4:]
		if end := strings.IndexAny(rest, ",)"); end > 0 {
			if pid, err := strconv.Atoi(rest[:end]); err == nil {
				return pid
			}
		}
	}
	return 0
}

func getUserForPID(pid int) string {
	if pid == 0 {
		return ""
	}
	// Read /proc/PID/status to get the username.
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				uid := fields[1]
				return uidToUsername(uid)
			}
		}
	}
	return ""
}

func uidToUsername(uid string) string {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 3 && fields[2] == uid {
			return fields[0]
		}
	}
	return ""
}
