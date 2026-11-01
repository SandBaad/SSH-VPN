// Package backup creates and restores SSH-VPN backups.
//
// A backup holds the user database, the config, the sshd settings and each
// user's password hash, so restoring on a fresh server brings every account
// back with its original password — moving to a new VPS (e.g. after an IP
// block) takes one command and users don't have to change anything.
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

	"sshfortress/internal/buildinfo"
	"sshfortress/internal/config"
	"sshfortress/internal/store"
	"sshfortress/internal/sysx"
)

// Engine handles backup creation and restoration.
type Engine struct {
	cfg *config.Config
	db  *store.DB
}

// NewEngine creates a backup engine.
func NewEngine(cfg *config.Config, db *store.DB) *Engine {
	return &Engine{cfg: cfg, db: db}
}

// BackupContents is the manifest.
type BackupContents struct {
	Version   string             `json:"version"`
	CreatedAt time.Time          `json:"created_at"`
	Hostname  string             `json:"hostname"`
	Users     []store.UserRecord `json:"users"`
	Config    *config.Config     `json:"config"`
	// Hashes maps user → /etc/shadow password hash.
	Hashes map[string]string `json:"hashes,omitempty"`
}

// RestoreResult reports what Restore did.
type RestoreResult struct {
	Contents    *BackupContents `json:"-"`
	Users       int             `json:"users"`
	Created     []string        `json:"created_accounts,omitempty"`
	Passwords   int             `json:"passwords_restored"`
	ConfigSaved string          `json:"config_saved_as,omitempty"`
	Warnings    []string        `json:"warnings,omitempty"`
}

// Dir is where backups are stored.
func (e *Engine) Dir() string { return filepath.Join(e.cfg.DataDir, "backups") }

// Create writes a new backup archive and returns its path.
func (e *Engine) Create() (string, error) {
	dir := e.Dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}
	users, err := e.db.ListUsers()
	if err != nil {
		return "", fmt.Errorf("list users: %w", err)
	}
	host, _ := os.Hostname()
	now := time.Now()
	id := now.Format("20060102-150405")
	path := filepath.Join(dir, fmt.Sprintf("sshvpn-backup-%s-%s.tar.gz", host, id))

	contents := BackupContents{
		Version:   buildinfo.Version,
		CreatedAt: now,
		Hostname:  host,
		Users:     users,
		Config:    e.cfg,
		Hashes:    shadowHashes(users),
	}
	manifest, err := json.MarshalIndent(contents, "", "  ")
	if err != nil {
		return "", err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	files := map[string][]byte{"manifest.json": manifest}
	if data, err := os.ReadFile(e.cfg.SSH.ConfigPath); err == nil {
		files["sshd_config"] = data
	}
	if data, err := os.ReadFile(e.cfg.Path()); err == nil {
		files["config.yaml"] = data
	}
	for name, data := range files {
		if err := addToTar(tw, name, data); err != nil {
			f.Close()
			os.Remove(path)
			return "", err
		}
	}
	// Close in order so the archive is complete before we measure it
	// (v1 measured the size before flushing and recorded garbage).
	if err := tw.Close(); err != nil {
		f.Close()
		return "", err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	st, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	_ = e.db.PutBackup(&store.BackupRecord{
		ID: id, FilePath: path, CreatedAt: now, SizeBytes: st.Size(), UserCount: len(users),
	})
	return path, nil
}

// Read parses a backup archive without changing anything.
func Read(path string) (*BackupContents, map[string][]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("not a backup archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if h.Size > 64<<20 {
			return nil, nil, fmt.Errorf("entry %s too large", h.Name)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, nil, err
		}
		files[filepath.Base(h.Name)] = data
	}
	m, ok := files["manifest.json"]
	if !ok {
		return nil, nil, fmt.Errorf("invalid backup: no manifest")
	}
	var c BackupContents
	if err := json.Unmarshal(m, &c); err != nil {
		return nil, nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &c, files, nil
}

// Restore brings users (accounts, passwords, limits, traffic) back.
// The backed-up config is saved next to the current one for review.
func (e *Engine) Restore(path string) (*RestoreResult, error) {
	if err := sysx.RequireRoot(); err != nil {
		return nil, err
	}
	c, files, err := Read(path)
	if err != nil {
		return nil, err
	}
	res := &RestoreResult{Contents: c}
	group := e.cfg.UserDefaults.Group
	if _, err := sysx.Run("getent", "group", group); err != nil {
		if _, err := sysx.Run("groupadd", "--system", group); err != nil {
			return nil, fmt.Errorf("create group: %w", err)
		}
	}
	shell := e.cfg.UserDefaults.Shell
	for _, u := range c.Users {
		u := u
		if _, err := sysx.Run("id", "-u", u.Username); err != nil {
			args := []string{"-M", "-N", "-g", group, "-s", shell, "-c", "SSH-VPN user"}
			if !u.ExpiresAt.IsZero() {
				args = append(args, "-e", u.ExpiresAt.Add(24*time.Hour).Format("2006-01-02"))
			}
			if _, err := sysx.Run("useradd", append(args, u.Username)...); err != nil {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %v", u.Username, err))
				continue
			}
			res.Created = append(res.Created, u.Username)
		}
		if h := c.Hashes[u.Username]; h != "" {
			// usermod -p takes an already-hashed password: the original
			// password works again without anyone knowing it.
			if _, err := sysx.Run("usermod", "-p", h, u.Username); err == nil {
				res.Passwords++
			} else {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s password: %v", u.Username, err))
			}
		}
		if u.Disabled {
			_, _ = sysx.Run("usermod", "-L", u.Username)
		}
		if err := e.db.PutUser(&u); err != nil {
			return res, fmt.Errorf("restore user %s: %w", u.Username, err)
		}
		res.Users++
	}
	if data, ok := files["config.yaml"]; ok {
		p := e.cfg.Path() + ".restored"
		if err := os.WriteFile(p, data, 0o640); err == nil {
			res.ConfigSaved = p
		}
	}
	return res, nil
}

// ListBackups returns recorded backups that still exist on disk.
func (e *Engine) ListBackups() ([]store.BackupRecord, error) {
	all, err := e.db.ListBackups()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, b := range all {
		if sysx.FileExists(b.FilePath) {
			out = append(out, b)
		}
	}
	return out, nil
}

// DeleteBackup removes a backup file and its record.
func (e *Engine) DeleteBackup(id string) error {
	all, err := e.db.ListBackups()
	if err != nil {
		return err
	}
	for _, b := range all {
		if b.ID == id {
			_ = os.Remove(b.FilePath)
			return e.db.DeleteBackup(id)
		}
	}
	return fmt.Errorf("backup %q not found", id)
}

func addToTar(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Size: int64(len(data)), Mode: 0o600, ModTime: time.Now()}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func shadowHashes(users []store.UserRecord) map[string]string {
	data, err := os.ReadFile("/etc/shadow")
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, u := range users {
		want[u.Username] = true
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) > 1 && want[f[0]] {
			h := strings.TrimLeft(f[1], "!") // store the unlocked hash
			if h != "" && h != "*" {
				out[f[0]] = h
			}
		}
	}
	return out
}
