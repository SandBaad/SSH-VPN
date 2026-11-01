// Package system provides system information gathering, network optimization,
// and service management for SSH Fortress.
package system

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Info holds system information for the dashboard.
type Info struct {
	Hostname    string
	OS          string
	Kernel      string
	Arch        string
	Uptime      time.Duration
	PublicIP    string
	CPUModel    string
	CPUCores    int
	MemTotal    uint64 // bytes
	MemUsed     uint64 // bytes
	MemPercent  float64
	DiskTotal   uint64 // bytes
	DiskUsed    uint64 // bytes
	DiskPercent float64
	LoadAvg     string
}

// NetTraffic holds network I/O counters.
type NetTraffic struct {
	Interface string
	RxBytes   uint64
	TxBytes   uint64
}

// GetInfo gathers current system information.
func GetInfo() Info {
	info := Info{
		Arch:     runtime.GOARCH,
		CPUCores: runtime.NumCPU(),
	}

	// Hostname
	info.Hostname, _ = os.Hostname()

	// OS info from /etc/os-release
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				info.OS = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}

	// Kernel
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		info.Kernel = strings.TrimSpace(string(out))
	}

	// Uptime
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 1 {
			if secs, err := strconv.ParseFloat(fields[0], 64); err == nil {
				info.Uptime = time.Duration(secs) * time.Second
			}
		}
	}

	// CPU model
	if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "model name") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					info.CPUModel = strings.TrimSpace(parts[1])
					break
				}
			}
		}
	}

	// Memory from /proc/meminfo
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		memMap := parseMemInfo(string(data))
		info.MemTotal = memMap["MemTotal"] * 1024
		memFree := memMap["MemAvailable"]
		if memFree == 0 {
			memFree = memMap["MemFree"] + memMap["Buffers"] + memMap["Cached"]
		}
		info.MemUsed = info.MemTotal - (memFree * 1024)
		if info.MemTotal > 0 {
			info.MemPercent = float64(info.MemUsed) / float64(info.MemTotal) * 100
		}
	}

	// Disk usage
	if out, err := exec.Command("df", "-B1", "/").Output(); err == nil {
		lines := strings.Split(string(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 5 {
				info.DiskTotal, _ = strconv.ParseUint(fields[1], 10, 64)
				info.DiskUsed, _ = strconv.ParseUint(fields[2], 10, 64)
				if info.DiskTotal > 0 {
					info.DiskPercent = float64(info.DiskUsed) / float64(info.DiskTotal) * 100
				}
			}
		}
	}

	// Load average
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(data))
		if len(fields) >= 3 {
			info.LoadAvg = strings.Join(fields[:3], " ")
		}
	}

	// Public IP
	info.PublicIP = PublicIP()

	return info
}

// GetNetTraffic returns network traffic counters from /proc/net/dev.
func GetNetTraffic() []NetTraffic {
	var traffic []NetTraffic

	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return traffic
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // Skip header lines.
		}

		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue // Skip loopback.
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}

		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)

		traffic = append(traffic, NetTraffic{
			Interface: iface,
			RxBytes:   rxBytes,
			TxBytes:   txBytes,
		})
	}
	return traffic
}

func parseMemInfo(data string) map[string]uint64 {
	result := make(map[string]uint64)
	for _, line := range strings.Split(data, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valStr := strings.TrimSpace(parts[1])
		valStr = strings.TrimSuffix(valStr, " kB")
		val, _ := strconv.ParseUint(strings.TrimSpace(valStr), 10, 64)
		result[key] = val
	}
	return result
}

var (
	publicIPMu    sync.Mutex
	publicIPCache string
	publicIPAt    time.Time
)

// PublicIP returns the server's public IPv4 address (cached for 10 min).
func PublicIP() string {
	publicIPMu.Lock()
	defer publicIPMu.Unlock()
	if publicIPCache != "" && time.Since(publicIPAt) < 10*time.Minute {
		return publicIPCache
	}
	publicIPCache, publicIPAt = getPublicIP(), time.Now()
	return publicIPCache
}

func getPublicIP() string {
	// Prefer a public address on an interface (no network call).
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, i := range ifaces {
			addrs, err := i.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				var ip net.IP
				switch v := addr.(type) {
				case *net.IPNet:
					ip = v.IP
				case *net.IPAddr:
					ip = v.IP
				}
				v4 := ip.To4()
				if v4 == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
					continue
				}
				if v4[0] == 100 && v4[1]&0xC0 == 64 { // CGNAT 100.64.0.0/10
					continue
				}
				return v4.String()
			}
		}
	}
	// Behind NAT (some clouds): ask an external service.
	for _, url := range []string{"https://api.ipify.org", "https://ifconfig.me/ip", "https://icanhazip.com"} {
		out, err := exec.Command("curl", "-4", "-fsS", "--max-time", "4", url).Output()
		if ip := net.ParseIP(strings.TrimSpace(string(out))); err == nil && ip != nil {
			return ip.String()
		}
	}
	return "unknown"
}

// FormatBytes formats byte counts as human-readable strings.
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatUptime formats a duration as a human-readable uptime string.
func FormatUptime(d time.Duration) string {
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, mins)
	}
	return fmt.Sprintf("%dm", mins)
}
