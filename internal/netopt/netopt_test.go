package netopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sshfortress/internal/config"
)

func fakeProc(t *testing.T, vals map[string]string) {
	t.Helper()
	ProcSys = t.TempDir()
	for k, v := range vals {
		p := sysPath(k)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(v+"\n"), 0o644)
	}
}

func TestStatusNormalizesWhitespace(t *testing.T) {
	fakeProc(t, map[string]string{
		"net.ipv4.tcp_rmem":               "4096\t131072\t" + itoa(bufferMax()),
		"net.ipv4.tcp_congestion_control": "cubic",
		"net.core.default_qdisc":          "fq",
	})
	cfg := config.Defaults()
	got := map[string]Param{}
	for _, p := range Status(cfg) {
		got[p.Key] = p
	}
	if !got["net.ipv4.tcp_rmem"].Applied {
		t.Errorf("tab-separated value should match: %+v", got["net.ipv4.tcp_rmem"])
	}
	if got["net.ipv4.tcp_congestion_control"].Applied {
		t.Error("cubic must not count as bbr")
	}
	if got["fs.file-max"].Supported {
		t.Error("missing keys must be unsupported")
	}
	a, total := Score(Status(cfg))
	if total != 3 || a != 2 {
		t.Errorf("score = %d/%d, want 2/3", a, total)
	}
}

func TestProfileHasLatencyKeys(t *testing.T) {
	keys := map[string]string{}
	for _, p := range Profile(config.Defaults()) {
		keys[p.Key] = p.Value
	}
	for k, v := range map[string]string{
		"net.ipv4.tcp_notsent_lowat":         "16384",
		"net.ipv4.tcp_slow_start_after_idle": "0",
		"net.ipv4.tcp_congestion_control":    "bbr",
		"net.core.default_qdisc":             "fq",
	} {
		if keys[k] != v {
			t.Errorf("%s = %q, want %q", k, keys[k], v)
		}
	}
	if !strings.HasSuffix(keys["net.ipv4.tcp_wmem"], itoa(bufferMax())) {
		t.Errorf("wmem max not RAM-sized: %s", keys["net.ipv4.tcp_wmem"])
	}
}

func itoa(n int) string {
	s := ""
	if n == 0 {
		return "0"
	}
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
