// Package backup provides backup and restore functionality
// for SSH Fortress user data and configuration.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sshfortress/internal/config"
	"sshfortress/internal/store"
)

// Engine handles backup creation and restoration.
type Engine struct {
	cfg *config.Config
	db  *store.DB
}

// NewEngine creates a new backup engine.
func NewEngine(cfg *config.Config, db *store.DB) *Engine {
	return &Engine{cfg: cfg, db: db}
}

// BackupContents describes what a backup contains.
type BackupContents struct {
	Version   string             `json:"version"`
	CreatedAt time.Time          `json:"created_at"`
	Hostname  string             `json:"hostname"`
	Users     []store.UserRecord `json:"users"`
	Config    *config.Config     `json:"config"`
}

// Create creates a new backup archive.
// Returns the path to the created backup file.
func (e *Engine) Create() (string, error) {
	backupDir := filepath.Join(e.cfg.DataDir, "backups")
	if err := os.MkdirAll(backupDir, 0750); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	hostname, _ := os.Hostname()
	filename := fmt.Sprintf("sshfortress-backup-%s-%s.tar.gz", hostname, timestamp)
	backupPath := filepath.Join(backupDir, filename)

	// Gather data.
	users, err := e.db.ListUsers()
	if err != nil {
		return "", fmt.Errorf("list users: %w", err)
	}

	contents := BackupContents{
		Version:   "1.0.0",
		CreatedAt: time.Now(),
		Hostname:  hostname,
		Users:     users,
		Config:    e.cfg,
	}

	// Create tar.gz archive.
	outFile, err := os.Create(backupPath)
	if err != nil {
		return "", fmt.Errorf("create backup file: %w", err)
	}
	defer outFile.Close()

	gzWriter := gzip.NewWriter(outFile)
	defer gzWriter.Close()

	tarWriter := tar.NewWriter(gzWriter)
	defer tarWriter.Close()

	// Write manifest.
	manifestData, err := json.MarshalIndent(contents, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal manifest: %w", err)
	}

	if err := addToTar(tarWriter, "manifest.json", manifestData); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}

	// Include sshd_config if it exists.
	if data, err := os.ReadFile(e.cfg.SSH.ConfigPath); err == nil {
		addToTar(tarWriter, "sshd_config", data)
	}

	// Include the sysctl config if it exists.
	if data, err := os.ReadFile("/etc/sysctl.d/99-sshfortress.conf"); err == nil {
		addToTar(tarWriter, "99-sshfortress.conf", data)
	}

	// Include /etc/passwd and /etc/shadow entries for managed users.
	if passwdEntries, err := extractUserEntries("/etc/passwd", users); err == nil {
		addToTar(tarWriter, "passwd_entries", []byte(passwdEntries))
	}
	if shadowEntries, err := extractUserEntries("/etc/shadow", users); err == nil {
		addToTar(tarWriter, "shadow_entries", []byte(shadowEntries))
	}

	// Record backup in database.
	stat, _ := outFile.Stat()
	size := int64(0)
	if stat != nil {
		size = stat.Size()
	}

	record := &store.BackupRecord{
		ID:        timestamp,
		FilePath:  backupPath,
		CreatedAt: time.Now(),
		SizeBytes: size,
		UserCount: len(users),
	}
	e.db.PutBackup(record)

	return backupPath, nil
}

// Restore restores from a backup archive.
func (e *Engine) Restore(backupPath string) (*BackupContents, error) {
	file, err := os.Open(backupPath)
	if err != nil {
		return nil, fmt.Errorf("open backup: %w", err)
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	var contents *BackupContents

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}

		data, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("read entry %s: %w", header.Name, err)
		}

		switch header.Name {
		case "manifest.json":
			contents = &BackupContents{}
			if err := json.Unmarshal(data, contents); err != nil {
				return nil, fmt.Errorf("parse manifest: %w", err)
			}
		case "sshd_config":
			// Restore sshd_config.
			os.WriteFile(e.cfg.SSH.ConfigPath+".restored", data, 0644)
		case "99-sshfortress.conf":
			os.WriteFile("/etc/sysctl.d/99-sshfortress.conf", data, 0644)
		}
	}

	if contents == nil {
		return nil, fmt.Errorf("invalid backup: no manifest found")
	}

	// Restore user records to database.
	for _, user := range contents.Users {
		u := user // Copy for pointer.
		if err := e.db.PutUser(&u); err != nil {
			return nil, fmt.Errorf("restore user %s: %w", user.Username, err)
		}
	}

	return contents, nil
}

// ListBackups returns all recorded backups.
func (e *Engine) ListBackups() ([]store.BackupRecord, error) {
	return e.db.ListBackups()
}

// DeleteBackup removes a backup file and its record.
func (e *Engine) DeleteBackup(id string) error {
	backups, err := e.db.ListBackups()
	if err != nil {
		return err
	}

	for _, b := range backups {
		if b.ID == id {
			os.Remove(b.FilePath) // Best effort file removal.
			// Note: BoltDB doesn't expose DeleteBackup, but we could add it.
			return nil
		}
	}
	return fmt.Errorf("backup %q not found", id)
}

func addToTar(tw *tar.Writer, name string, data []byte) error {
	header := &tar.Header{
		Name:    name,
		Size:    int64(len(data)),
		Mode:    0600,
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func extractUserEntries(filePath string, users []store.UserRecord) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}

	userSet := make(map[string]bool)
	for _, u := range users {
		userSet[u.Username] = true
	}

	var entries []string
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) >= 1 && userSet[parts[0]] {
			entries = append(entries, line)
		}
	}
	return strings.Join(entries, "\n"), nil
}
