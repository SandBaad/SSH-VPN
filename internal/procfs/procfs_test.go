package procfs

import (
	"encoding/binary"
	"os"
	"testing"
)

func hostWord(b [4]byte) string {
	// What the kernel would print for these network-order bytes.
	v := binary.NativeEndian.Uint32(b[:])
	const hexd = "0123456789ABCDEF"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = hexd[v&0xF]
		v >>= 4
	}
	return string(out)
}

func TestParseHexAddrIPv4(t *testing.T) {
	s := hostWord([4]byte{127, 0, 0, 1}) + ":1C84" // 7300
	ap, err := parseHexAddr(s)
	if err != nil {
		t.Fatal(err)
	}
	if ap.Addr().String() != "127.0.0.1" || ap.Port() != 7300 {
		t.Fatalf("got %v", ap)
	}
}

func TestParseHexAddrIPv6Mapped(t *testing.T) {
	// ::ffff:10.1.2.3 port 22
	words := [4][4]byte{{0, 0, 0, 0}, {0, 0, 0, 0}, {0, 0, 0xff, 0xff}, {10, 1, 2, 3}}
	s := ""
	for _, w := range words {
		s += hostWord(w)
	}
	ap, err := parseHexAddr(s + ":0016")
	if err != nil {
		t.Fatal(err)
	}
	if ap.Addr().String() != "10.1.2.3" || ap.Port() != 22 {
		t.Fatalf("got %v", ap)
	}
}

func TestParseTCPTable(t *testing.T) {
	lo := hostWord([4]byte{127, 0, 0, 1})
	any := hostWord([4]byte{0, 0, 0, 0})
	cli := hostWord([4]byte{5, 6, 7, 8})
	srv := hostWord([4]byte{10, 0, 0, 5})
	data := "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n" +
		"   0: " + any + ":0016 " + any + ":0000 0A 00000000:00000000 00:00000000 00000000     0        0 1111 1 0 100 0 0 10 0\n" +
		"   1: " + srv + ":0016 " + cli + ":D431 01 00000000:00000000 02:000AFC1D 00000000     0        0 2222 4 0 20 4 30 10 -1\n" +
		"   2: " + lo + ":A000 " + lo + ":1C84 01 00000000:00000000 00:00000000 00000000  1001        0 3333 1 0 20 4 30 10 -1\n"
	tab := &TCPTable{ByInode: map[uint64]TCPSock{}, ListenPorts: map[uint16]bool{}}
	parseTCP([]byte(data), tab)
	if !tab.ListenPorts[22] {
		t.Fatal("listen port 22 not detected")
	}
	s := tab.ByInode[2222]
	if s.State != TCPEstablished || s.Remote.Addr().String() != "5.6.7.8" || s.Remote.Port() != 0xD431 {
		t.Fatalf("bad established row: %+v", s)
	}
	if tab.ByInode[3333].UID != 1001 {
		t.Fatalf("uid not parsed")
	}
}

func TestParseStatWithWeirdComm(t *testing.T) {
	line := "1234 (sshd: a) b) S 99 1234 1234 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 4242 1000 100\n"
	sf, err := parseStat([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if sf.Comm != "sshd: a) b" || sf.PPID != 99 || sf.startTicks != 4242 {
		t.Fatalf("got %+v", sf)
	}
}

func TestSelfProcess(t *testing.T) {
	p, err := ReadProc(os.Getpid(), BootTime())
	if err != nil {
		t.Skipf("no procfs: %v", err)
	}
	if p.PID != os.Getpid() || p.UID != os.Geteuid() || p.StartTime.IsZero() {
		t.Fatalf("bad self proc: %+v", p)
	}
	if _, err := ReadIO(os.Getpid()); err != nil {
		t.Skipf("no io accounting: %v", err)
	}
}
