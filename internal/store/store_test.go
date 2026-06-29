package store

import (
	"path/filepath"
	"testing"
	"time"
)

func tempDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUserCRUD(t *testing.T) {
	db := tempDB(t)

	// Create.
	u := &UserRecord{
		Username:       "testuser",
		MaxConnections: 3,
		ExpiresAt:      time.Now().Add(30 * 24 * time.Hour),
		CreatedAt:      time.Now(),
	}
	if err := db.PutUser(u); err != nil {
		t.Fatalf("PutUser: %v", err)
	}

	// Read.
	got, err := db.GetUser("testuser")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.Username != "testuser" {
		t.Errorf("Username = %q, want %q", got.Username, "testuser")
	}
	if got.MaxConnections != 3 {
		t.Errorf("MaxConnections = %d, want 3", got.MaxConnections)
	}

	// List.
	users, err := db.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("ListUsers count = %d, want 1", len(users))
	}

	// Delete.
	if err := db.DeleteUser("testuser"); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	_, err = db.GetUser("testuser")
	if err == nil {
		t.Error("GetUser should fail after delete")
	}
}

func TestSettings(t *testing.T) {
	db := tempDB(t)

	if err := db.PutSetting("version", "1.0.0"); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}

	val, err := db.GetSetting("version")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if val != "1.0.0" {
		t.Errorf("GetSetting = %q, want %q", val, "1.0.0")
	}

	// Missing key.
	_, err = db.GetSetting("nonexistent")
	if err == nil {
		t.Error("GetSetting should fail for missing key")
	}
}

func TestBackups(t *testing.T) {
	db := tempDB(t)

	b := &BackupRecord{
		ID:        "backup-001",
		FilePath:  "/tmp/backup.tar.gz",
		CreatedAt: time.Now(),
		SizeBytes: 1024,
		UserCount: 5,
	}
	if err := db.PutBackup(b); err != nil {
		t.Fatalf("PutBackup: %v", err)
	}

	backups, err := db.ListBackups()
	if err != nil {
		t.Fatalf("ListBackups: %v", err)
	}
	if len(backups) != 1 {
		t.Fatalf("ListBackups count = %d, want 1", len(backups))
	}
	if backups[0].ID != "backup-001" {
		t.Errorf("Backup ID = %q, want %q", backups[0].ID, "backup-001")
	}
}
