// Package sysx contains small, dependency-free helpers for running system
// commands, writing files atomically and talking to systemd.
//
// Every command is executed with exec.Command and separate arguments — never
// through a shell — so user-controlled values can't inject commands.
package sysx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultTimeout bounds every external command so a hung tool can never
// freeze the daemon or the TUI.
const DefaultTimeout = 30 * time.Second

// Run executes a command and returns its trimmed combined output.
// On failure the error message includes the command's output.
func Run(name string, args ...string) (string, error) {
	return RunInput(DefaultTimeout, "", name, args...)
}

// RunTimeout is Run with a custom timeout.
func RunTimeout(timeout time.Duration, name string, args ...string) (string, error) {
	return RunInput(timeout, "", name, args...)
}

// RunInput executes a command, feeding input on stdin.
func RunInput(timeout time.Duration, input, name string, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	out := strings.TrimSpace(buf.String())
	if ctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("%s: timed out after %s", name, timeout)
	}
	if err != nil {
		if out != "" {
			return out, fmt.Errorf("%s %s: %s: %w", name, strings.Join(args, " "), firstLine(out), err)
		}
		return out, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Which returns the absolute path of the first command found in PATH or in
// the usual sbin directories (which are often missing from PATH under sudo).
func Which(names ...string) string {
	extra := []string{"/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin"}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			if abs, err := filepath.Abs(p); err == nil {
				return abs
			}
			return p
		}
		for _, d := range extra {
			p := filepath.Join(d, n)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				return p
			}
		}
	}
	return ""
}

// FileExists reports whether path exists.
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// WriteFileAtomic writes data to a temp file in the same directory and renames
// it over path, so readers never observe a half-written file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// WriteIfChanged writes the file only when its content differs.
// It reports whether a write happened.
func WriteIfChanged(path string, data []byte, perm os.FileMode) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	return true, WriteFileAtomic(path, data, perm)
}

// ─── systemd ────────────────────────────────────────────────────────────────

// HasSystemd reports whether the machine is booted with systemd.
func HasSystemd() bool {
	return FileExists("/run/systemd/system")
}

// Systemctl runs systemctl with the given arguments.
func Systemctl(args ...string) error {
	_, err := Run("systemctl", args...)
	return err
}

// UnitActive reports whether a unit is active.
func UnitActive(unit string) bool {
	out, _ := Run("systemctl", "is-active", unit)
	return strings.TrimSpace(out) == "active"
}

// UnitEnabled reports whether a unit is enabled.
func UnitEnabled(unit string) bool {
	out, _ := Run("systemctl", "is-enabled", unit)
	s := strings.TrimSpace(out)
	return s == "enabled" || s == "enabled-runtime" || s == "alias"
}

// UnitExists reports whether systemd knows about a unit file.
func UnitExists(unit string) bool {
	out, err := Run("systemctl", "list-unit-files", "--no-legend", "--no-pager", unit)
	if err != nil {
		return false
	}
	return strings.Contains(out, unit)
}

// ErrNotRoot is returned by RequireRoot.
var ErrNotRoot = errors.New("this action needs root — run it with sudo")

// RequireRoot returns ErrNotRoot unless running as uid 0.
func RequireRoot() error {
	if os.Geteuid() != 0 {
		return ErrNotRoot
	}
	return nil
}
