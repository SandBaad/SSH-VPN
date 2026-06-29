// Package store provides a BoltDB-backed embedded key-value store
// for SSH Fortress persistent data (users, sessions, settings).
package store

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"os"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	bucketUsers    = []byte("users")
	bucketSettings = []byte("settings")
	bucketSessions = []byte("sessions")
	bucketBackups  = []byte("backups")
)

// DB wraps a BoltDB instance with typed operations.
type DB struct {
	bolt *bolt.DB
	path string
}

// UserRecord represents a managed SSH user in the database.
type UserRecord struct {
	Username       string    `json:"username"`
	MaxConnections int       `json:"max_connections"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	Disabled       bool      `json:"disabled"`
	Notes          string    `json:"notes"`
}

// SessionRecord represents a snapshot of an active SSH session.
type SessionRecord struct {
	Username  string    `json:"username"`
	PID       int       `json:"pid"`
	LoginTime time.Time `json:"login_time"`
	ClientIP  string    `json:"client_ip"`
}

// BackupRecord stores metadata about a backup.
type BackupRecord struct {
	ID        string    `json:"id"`
	FilePath  string    `json:"file_path"`
	CreatedAt time.Time `json:"created_at"`
	SizeBytes int64     `json:"size_bytes"`
	UserCount int       `json:"user_count"`
}

// Open creates or opens a BoltDB database at the given path.
func Open(path string) (*DB, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("create db dir: %w", err)
	}

	bdb, err := bolt.Open(path, 0600, &bolt.Options{
		Timeout: 5 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}

	// Initialize buckets.
	err = bdb.Update(func(tx *bolt.Tx) error {
		for _, b := range [][]byte{bucketUsers, bucketSettings, bucketSessions, bucketBackups} {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return fmt.Errorf("create bucket %s: %w", string(b), err)
			}
		}
		return nil
	})
	if err != nil {
		bdb.Close()
		return nil, err
	}

	return &DB{bolt: bdb, path: path}, nil
}

// Close closes the database.
func (db *DB) Close() error {
	return db.bolt.Close()
}

// --- User Operations ---

// PutUser creates or updates a user record.
func (db *DB) PutUser(u *UserRecord) error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(u)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketUsers).Put([]byte(u.Username), data)
	})
}

// GetUser retrieves a user record by username.
func (db *DB) GetUser(username string) (*UserRecord, error) {
	var u UserRecord
	err := db.bolt.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketUsers).Get([]byte(username))
		if data == nil {
			return fmt.Errorf("user %q not found", username)
		}
		return json.Unmarshal(data, &u)
	})
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// DeleteUser removes a user record.
func (db *DB) DeleteUser(username string) error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUsers).Delete([]byte(username))
	})
}

// ListUsers returns all user records.
func (db *DB) ListUsers() ([]UserRecord, error) {
	var users []UserRecord
	err := db.bolt.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketUsers).ForEach(func(k, v []byte) error {
			var u UserRecord
			if err := json.Unmarshal(v, &u); err != nil {
				return err
			}
			users = append(users, u)
			return nil
		})
	})
	return users, err
}

// --- Settings Operations ---

// PutSetting stores a setting key-value pair.
func (db *DB) PutSetting(key, value string) error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketSettings).Put([]byte(key), []byte(value))
	})
}

// GetSetting retrieves a setting by key.
func (db *DB) GetSetting(key string) (string, error) {
	var val string
	err := db.bolt.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketSettings).Get([]byte(key))
		if data == nil {
			return fmt.Errorf("setting %q not found", key)
		}
		val = string(data)
		return nil
	})
	return val, err
}

// --- Backup Operations ---

// PutBackup stores backup metadata.
func (db *DB) PutBackup(b *BackupRecord) error {
	return db.bolt.Update(func(tx *bolt.Tx) error {
		data, err := json.Marshal(b)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketBackups).Put([]byte(b.ID), data)
	})
}

// ListBackups returns all backup records.
func (db *DB) ListBackups() ([]BackupRecord, error) {
	var backups []BackupRecord
	err := db.bolt.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketBackups).ForEach(func(k, v []byte) error {
			var b BackupRecord
			if err := json.Unmarshal(v, &b); err != nil {
				return err
			}
			backups = append(backups, b)
			return nil
		})
	})
	return backups, err
}
