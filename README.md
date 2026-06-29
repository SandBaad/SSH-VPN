<div align="center">

<img src="assets/banner.png" alt="SSH-VPN Banner" width="700">

<br>

[![Go Version](https://img.shields.io/badge/Go-1.22+-00ADD8?style=for-the-badge&logo=go&logoColor=white)](https://go.dev)
[![Platform](https://img.shields.io/badge/Platform-Linux-FCC624?style=for-the-badge&logo=linux&logoColor=black)](https://www.linux.org/)
[![Release](https://img.shields.io/badge/Version-1.0.0-blue?style=for-the-badge)](https://github.com/SandBaad/SSH-VPN/releases)
[![Stars](https://img.shields.io/github/stars/SandBaad/SSH-VPN?style=for-the-badge&color=yellow)](https://github.com/SandBaad/SSH-VPN/stargazers)

<br>

**A modern, high-performance SSH management system built in Go with a stunning terminal UI.**  
*Manage users, tunnels, BadVPN, network optimization, and backups — all from one beautiful interface.*

<br>

[**🚀 Quick Install**](#-installation) · [**📖 Documentation**](#-features) · [**🐛 Report Bug**](https://github.com/SandBaad/SSH-VPN/issues) · [**✨ Request Feature**](https://github.com/SandBaad/SSH-VPN/issues)

</div>

---

## ⚡ One-Line Installation

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/SandBaad/SSH-VPN/main/scripts/install.sh)
```

> **Requirements:** Root access on a Linux server (Ubuntu 18.04+, Debian 10+, CentOS 7+)

---

## ✨ Features

<table>
<tr>
<td width="50%">

### 🔐 SSH Management
- Multi-port SSH listening
- Secure user creation & deletion
- Password management with Argon2id
- Connection limit enforcement
- Account expiration tracking

</td>
<td width="50%">

### 🌐 BadVPN Integration
- One-click UDP Gateway (udpgw) setup
- Systemd-managed process lifecycle
- Configurable max clients & connections
- Auto-start on boot
- Clean start/stop/restart

</td>
</tr>
<tr>
<td width="50%">

### 📡 Real-Time Monitoring
- Live SSH session tracking
- Per-user connection counts
- Client IP visibility
- Auto-refresh dashboard
- Resource usage (CPU/RAM/Disk)

</td>
<td width="50%">

### ⚡ Network Optimization
- TCP BBR congestion control
- Buffer size optimization
- Keepalive tuning
- IP forwarding for tunnels
- One-click apply & revert

</td>
</tr>
<tr>
<td width="50%">

### 💾 Backup & Restore
- Full user + config backup
- Compressed tar.gz archives
- One-click restore
- Backup history tracking
- Safe credential handling

</td>
<td width="50%">

### 🛡️ Security First
- No command injection (Go binary)
- Argon2id password hashing
- Input validation & sanitization
- No plaintext credential storage
- Atomic database transactions

</td>
</tr>
</table>

---

## 📦 Installation

### Method 1: Auto-Installer (Recommended)

```bash
# Install
bash <(curl -fsSL https://raw.githubusercontent.com/SandBaad/SSH-VPN/main/scripts/install.sh)

# Uninstall
bash <(curl -fsSL https://raw.githubusercontent.com/SandBaad/SSH-VPN/main/scripts/install.sh) uninstall
```

### Method 2: Manual Download

```bash
# Download the latest release
wget https://github.com/SandBaad/SSH-VPN/releases/latest/download/sshfortress-linux-amd64

# Install
chmod +x sshfortress-linux-amd64
mv sshfortress-linux-amd64 /usr/local/bin/sshfortress

# Run
sshfortress
```

### Method 3: Build from Source

```bash
# Clone the repository
git clone https://github.com/SandBaad/SSH-VPN.git
cd SSH-VPN

# Build
go build -o sshfortress ./cmd/sshfortress/

# Install
sudo mv sshfortress /usr/local/bin/
sudo sshfortress
```

---

## 🚀 Quick Start

```bash
# Launch the manager (any of these work)
sshfortress          # Full command
sshf                 # Short alias
menu                 # Legacy alias

# Use with custom config
sshfortress --config /path/to/config.yaml

# Check version
sshfortress version
```

### Navigation

| Key | Action |
|---|---|
| `↑` `↓` | Navigate menu items |
| `Enter` | Select / confirm |
| `Tab` | Switch between sidebar and content |
| `Esc` | Go back / cancel |
| `r` | Refresh data |
| `q` | Quit |

---

## ⚙️ Configuration

Configuration file: `/etc/sshfortress/config.yaml`

```yaml
# SSH Settings
ssh:
  ports: [22, 443]            # Multi-port listening
  max_auth_tries: 3
  password_auth: true

# BadVPN UDP Gateway
badvpn:
  enabled: true
  listen_addr: "127.0.0.1:7300"
  max_clients: 1000
  max_connections_per_client: 10

# Network Optimization
network:
  auto_optimize: true
  congestion_control: bbr

# User Defaults
user_defaults:
  default_expiration_days: 30
  default_max_connections: 2
  shell: /bin/false
```

---

## 🏗️ Architecture

```
sshfortress/
├── cmd/sshfortress/        # CLI entry point (Cobra)
├── internal/
│   ├── config/             # YAML configuration
│   ├── store/              # BoltDB embedded database
│   ├── security/           # Argon2id hashing + sanitization
│   ├── user/               # User CRUD + session monitoring + limiter
│   ├── tunnel/             # SSH tunnel + BadVPN lifecycle
│   ├── system/             # System info + network optimizer + services
│   └── backup/             # Backup/restore engine (tar.gz)
├── tui/
│   ├── views/              # Dashboard, Users, Tunnels, BadVPN, etc.
│   ├── theme.go            # Dark theme + Lipgloss styles
│   └── keys.go             # Keybinding definitions
├── scripts/
│   └── install.sh          # One-line auto-installer
├── Makefile                # Cross-compilation (amd64/arm64)
└── go.mod
```

### Technology Stack

| Component | Technology |
|---|---|
| **Language** | Go 1.22+ |
| **TUI Framework** | [Bubbletea](https://github.com/charmbracelet/bubbletea) + [Lipgloss](https://github.com/charmbracelet/lipgloss) |
| **Database** | [BoltDB](https://github.com/etcd-io/bbolt) (embedded key-value) |
| **CLI** | [Cobra](https://github.com/spf13/cobra) |
| **Config** | YAML |
| **Security** | Argon2id (golang.org/x/crypto) |

---

## 🔒 Security Improvements

This project is a complete rewrite of the legacy [SSH-PLUS-MANAGER](https://github.com/Danura99/SSH-PLUS-MANAGER) bash scripts. Every security vulnerability has been eliminated:

| Legacy Vulnerability | SSH-VPN Fix |
|---|---|
| ❌ Command injection via `${comando[0]}` | ✅ Go's `exec.Command` with separate args — no shell |
| ❌ Plaintext passwords in files | ✅ Argon2id hashing with constant-time comparison |
| ❌ Race conditions on shared files | ✅ BoltDB atomic transactions with mutex locks |
| ❌ `stderr > /dev/null` silent failures | ✅ Structured error handling and logging |
| ❌ Hardcoded licence strings | ✅ Removed — open source, no licence checks |
| ❌ Backups via Apache on port 81 | ✅ Local tar.gz files — no web exposure |

---

## 🤝 Contributing

Contributions are welcome! Here's how to get started:

```bash
# Fork and clone
git clone https://github.com/YOUR_USERNAME/SSH-VPN.git
cd SSH-VPN

# Install dependencies
go mod download

# Run tests
go test ./internal/... -v -race

# Build
make build
```

---

<div align="center">

**Built with ❤️ in Go**

[⬆ Back to Top](#️-ssh-vpn)

</div>
