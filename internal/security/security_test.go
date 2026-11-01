package security

import (
	"strings"
	"testing"
)

func TestGeneratedPasswordsAreValidAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw, err := GenerateRandomPassword(10)
		if err != nil {
			t.Fatal(err)
		}
		if len(pw) != 10 {
			t.Fatalf("len = %d", len(pw))
		}
		if err := ValidatePassword(pw); err != nil {
			t.Fatalf("generated password %q rejected: %v", pw, err)
		}
		if strings.ContainsAny(pw, "0O1lI") {
			t.Fatalf("look-alike character in %q", pw)
		}
		if seen[pw] {
			t.Fatalf("duplicate password %q", pw)
		}
		seen[pw] = true
	}
}

func TestValidatePassword(t *testing.T) {
	for _, bad := range []string{"short", "has space1", "colon:pass", "quote\"pass", "new\nline12", "ünicode123"} {
		if ValidatePassword(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	for _, good := range []string{"abcdef", "P@ss-w0rd!", "x#y%z+1=2"} {
		if err := ValidatePassword(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
}

func TestValidateUsername(t *testing.T) {
	for _, bad := range []string{"", "ab", "1abc", "root", "sshvpn", "a b", "abc;rm", strings.Repeat("a", 40)} {
		if ValidateUsername(bad) == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if err := ValidateUsername("ali_reza-2"); err != nil {
		t.Error(err)
	}
}
