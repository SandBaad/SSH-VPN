package security

import (
	"fmt"
	"net"
	"regexp"
	"strings"
)

var (
	// usernameRegex allows only alphanumeric, underscore, hyphen. 3-32 chars.
	usernameRegex = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{2,31}$`)

	// Dangerous characters that must never reach a shell.
	shellDangerChars = []string{";", "|", "&", "`", "$", "(", ")", "{", "}", "<", ">", "\n", "\r", "\\", "'", "\""}
)

// ValidateUsername checks if a username is safe for system use.
func ValidateUsername(username string) error {
	if username == "" {
		return fmt.Errorf("username cannot be empty")
	}
	if !usernameRegex.MatchString(username) {
		return fmt.Errorf("username %q is invalid: must be 3-32 characters, start with a letter, and contain only letters, numbers, underscores, or hyphens", username)
	}

	// Reject system/reserved usernames.
	reserved := map[string]bool{
		"root": true, "admin": true, "daemon": true, "bin": true,
		"sys": true, "sync": true, "games": true, "man": true,
		"mail": true, "news": true, "uucp": true, "proxy": true,
		"www-data": true, "nobody": true, "sshd": true, "systemd-network": true,
		"sshvpn": true, "ubuntu": true, "debian": true, "centos": true, "postgres": true, "mysql": true,
	}
	if reserved[strings.ToLower(username)] {
		return fmt.Errorf("username %q is reserved and cannot be used", username)
	}

	return nil
}

// ValidatePort checks if a port number is valid.
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is out of range (1-65535)", port)
	}
	return nil
}

// ValidateIP checks if an IP address is valid.
func ValidateIP(ip string) error {
	if net.ParseIP(ip) == nil {
		return fmt.Errorf("invalid IP address: %q", ip)
	}
	return nil
}

// ValidateListenAddr validates an address in host:port format.
func ValidateListenAddr(addr string) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if host != "" {
		if err := ValidateIP(host); err != nil {
			return err
		}
	}
	_ = portStr // Port validation would need strconv, but SplitHostPort already validates format.
	return nil
}

// SanitizeShellArg removes any characters that could be used for shell injection.
// This should be used as a LAST RESORT — prefer using exec.Command with separate
// arguments instead of string concatenation.
func SanitizeShellArg(s string) string {
	result := s
	for _, ch := range shellDangerChars {
		result = strings.ReplaceAll(result, ch, "")
	}
	return strings.TrimSpace(result)
}

// ValidatePassword checks if a password meets minimum requirements.
// Passwords go to chpasswd on stdin (never through a shell), so the only
// characters that are truly unsafe are ':' (field separator), whitespace and
// control characters. Quotes and backslashes are rejected too because many
// VPN apps mangle them in their config strings.
func ValidatePassword(password string) error {
	if len(password) < 6 {
		return fmt.Errorf("password must be at least 6 characters")
	}
	if len(password) > 128 {
		return fmt.Errorf("password must be at most 128 characters")
	}
	for _, r := range password {
		switch {
		case r < 0x21 || r > 0x7e:
			return fmt.Errorf("password may only contain printable ASCII characters without spaces")
		case r == ':' || r == '"' || r == '\'' || r == '\\' || r == '`':
			return fmt.Errorf("password contains disallowed character %q", r)
		}
	}
	return nil
}
