// Package store provides a BoltDB-backed embedded database for SSH-VPN
// (users, traffic counters, settings and backups).
//
// BoltDB takes an exclusive file lock while a database is open, and the
// daemon, the TUI and the CLI are separate processes. Holding the database
// open for the life of a process would lock everyone else out (v1 had this
// bug as soon as two processes ran). So every operation opens the file,
// runs one transaction and closes it again; the lock is held for
// milliseconds and every process can use the database concurrently.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketUsers    = []byte("users")
	bucketSettings = []byte("settings")
	bucketSessions = []byte("sessions")
	bucketBackups  = []byte("backups")
)

// ErrNotFound is returned when a record doesn't exist.
var ErrNotFound = errors.New("not found")

// DB is a handle to the database file.
type DB struct {
	path string
	mu   sync.Mutex // serializes opens within this process
}

// Lock reasons stored on a user.
const (
	LockManual  = "manual"
	LockExpired = "expired"
	LockQuota   = "quota"
)

// UserRecord represents a managed SSH user.
type UserRecord struct {
	Username       string    `json:"username"`
	MaxConnections int       `json:"max_connections"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	// Disabled is true while the account is locked for any reason.
	Disabled bool `json:"disabled"`
	// LockReason explains Disabled: manual, expired or quota.
	LockReason string `json:"lock_reason,omitempty"`
	Notes      string `json:"notes"`
	// TrafficLimit in bytes (0 = unlimited).
	TrafficLimit int64 `json:"traffic_limit,omitempty"`
	// TrafficUsed in bytes since the last reset.
	TrafficUsed int64     `json:"traffic_used,omitempty"`
	LastSeen    time.Time `json:"last_seen,omitempty"`
}

// IsExpired reports whether the account is past its expiry time.
func (u *UserRecord) IsExpired(now time.Time) bool {
	return !u.ExpiresAt.IsZero() && !now.Before(u.ExpiresAt)
}

// QuotaExceeded reports whether the traffic limit has been used up.
func (u *UserRecord) QuotaExceeded() bool {
	return u.TrafficLimit > 0 && u.TrafficUsed >= u.TrafficLimit
}

// SessionRecord is a snapshot of an active SSH session (kept for API
// compatibility with v1; live sessions come from the session package).
type SessionRecord struct {
	Username  string    `json:"username"`
	PID       int       `json:"pid"`
	LoginTime time.Time `json:"login_time"`
	ClientIP  string    `json:"client_ip"`
}

// BackupRecord stores metadata about a backup archive.
type BackupRecord struct {
	ID        string    `json:"id"`
	FilePath  string    `json:"file_path"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
	UserCount int       `json:"user_count"`
}

// Open prepares the database at path, creating it and its buckets if needed.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}
	db := &DB{path: path}
	err := db.update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketUsers, bucketSettings, bucketSessions, bucketBackups} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return fmt.Errorf("create bucket %s: %w", b, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// Path returns the database file path.
func (db *DB) Path() string { return db.path }

// Close is a no-op kept for API compatibility: the file is never held open.
func (db *DB) Close() error { return nil }

func (db *DB) open(readOnly bool) (*bolt.DB, error) {
	b, err := bolt.Open(db.path, 0o600, &bolt.Options{Timeout: 10 * time.Second, ReadOnly: readOnly})
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", db.path, err)
	}
	return b, nil
}

func (db *DB) update(fn func(tx *bolt.Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	b, err := db.open(false)
	if err != nil {
		return err
	}
	defer b.Close()
	return b.Update(fn)
}

func (db *DB) view(fn func(tx *bolt.Tx) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	b, err := db.open(true)
	if err != nil {
		return err
	}
	defer b.Close()
	return b.View(fn)
}

// ─── Users ──────────────────────────────────────────────────────────────────

// PutUser creates or replaces a user record.
func (db *DB) PutUser(u *UserRecord) error {
	data, err := json.Marshal(u)
	if err != nil {
		return err
	}
	return db.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUsers).Put([]byte(u.Username), data)
	})
}

// GetUser retrieves a user record by username.
func (db *DB) GetUser(username string) (*UserRecord, error) {
	var u UserRecord
	err := db.view(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketUsers).Get([]byte(username))
		if data == nil {
			return fmt.Errorf("user %q: %w", username, ErrNotFound)
		}
		return json.Unmarshal(data, &u)
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpdateUser atomically loads, modifies and saves one user.
func (db *DB) UpdateUser(username string, fn func(u *UserRecord) error) (*UserRecord, error) {
	var out UserRecord
	err := db.update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		data := b.Get([]byte(username))
		if data == nil {
			return fmt.Errorf("user %q: %w", username, ErrNotFound)
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return err
		}
		if err := fn(&out); err != nil {
			return err
		}
		enc, err := json.Marshal(&out)
		if err != nil {
			return err
		}
		return b.Put([]byte(username), enc)
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// AddTraffic adds byte deltas to several users in one transaction and
// refreshes their LastSeen time.
func (db *DB) AddTraffic(deltas map[string]int64, seen time.Time) error {
	if len(deltas) == 0 {
		return nil
	}
	return db.update(func(tx *bolt.Tx) error {
		b := tx.Bucket(bucketUsers)
		for name, delta := range deltas {
			data := b.Get([]byte(name))
			if data == nil {
				continue
			}
			var u UserRecord
			if err := json.Unmarshal(data, &u); err != nil {
				continue
			}
			if delta > 0 {
				u.TrafficUsed += delta
			}
			u.LastSeen = seen
			enc, err := json.Marshal(&u)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(name), enc); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteUser removes a user record.
func (db *DB) DeleteUser(username string) error {
	return db.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUsers).Delete([]byte(username))
	})
}

// ListUsers returns all users sorted by name.
func (db *DB) ListUsers() ([]UserRecord, error) {
	var users []UserRecord
	err := db.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUsers).ForEach(func(k, v []byte) error {
			var u UserRecord
			if err := json.Unmarshal(v, &u); err != nil {
				return err
			}
			users = append(users, u)
			return nil
		})
	})
	sort.Slice(users, func(i, j int) bool { return users[i].Username < users[j].Username })
	return users, err
}

// ─── Settings ───────────────────────────────────────────────────────────────

// PutSetting stores a setting.
func (db *DB) PutSetting(key, value string) error {
	return db.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSettings).Put([]byte(key), []byte(value))
	})
}

// GetSetting retrieves a setting.
func (db *DB) GetSetting(key string) (string, error) {
	var val string
	err := db.view(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketSettings).Get([]byte(key))
		if data == nil {
			return fmt.Errorf("setting %q: %w", key, ErrNotFound)
		}
		val = string(data)
		return nil
	})
	return val, err
}

// ─── Backups ────────────────────────────────────────────────────────────────

// PutBackup stores backup metadata.
func (db *DB) PutBackup(b *BackupRecord) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	return db.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBackups).Put([]byte(b.ID), data)
	})
}

// ListBackups returns all backup records, newest first.
func (db *DB) ListBackups() ([]BackupRecord, error) {
	var backups []BackupRecord
	err := db.view(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBackups).ForEach(func(k, v []byte) error {
			var b BackupRecord
			if err := json.Unmarshal(v, &b); err != nil {
				return err
			}
			backups = append(backups, b)
			return nil
		})
	})
	sort.Slice(backups, func(i, j int) bool { return backups[i].CreatedAt.After(backups[j].CreatedAt) })
	return backups, err
}

// DeleteBackup removes backup metadata.
func (db *DB) DeleteBackup(id string) error {
	return db.update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBackups).Delete([]byte(id))
	})
}
