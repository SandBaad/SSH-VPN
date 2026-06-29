#!/bin/bash
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  SSH-VPN — Auto Installer
#  Enterprise-Grade SSH Manager for Linux Servers
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

set -euo pipefail

# ─── Configuration ───────────────────────────────────────────────────────────
REPO="SandBaad/SSH-VPN"
APP_NAME="sshfortress"
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/sshfortress"
DATA_DIR="/var/lib/sshfortress"
LOG_DIR="/var/log/sshfortress"
BADVPN_URL="https://github.com/ambrop72/badvpn/releases"

# ─── Colors ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[1;37m'
DIM='\033[2m'
BOLD='\033[1m'
NC='\033[0m'

# ─── Helper Functions ────────────────────────────────────────────────────────
print_banner() {
    echo -e "${CYAN}"
    echo "   ███████╗███████╗██╗  ██╗      ██╗   ██╗██████╗ ███╗   ██╗"
    echo "   ██╔════╝██╔════╝██║  ██║      ██║   ██║██╔══██╗████╗  ██║"
    echo "   ███████╗███████╗███████║█████╗██║   ██║██████╔╝██╔██╗ ██║"
    echo "   ╚════██║╚════██║██╔══██║╚════╝╚██╗ ██╔╝██╔═══╝ ██║╚██╗██║"
    echo "   ███████║███████║██║  ██║       ╚████╔╝ ██║     ██║ ╚████║"
    echo "   ╚══════╝╚══════╝╚═╝  ╚═╝        ╚═══╝  ╚═╝     ╚═╝  ╚═══╝"
    echo ""
    echo -e "          ${WHITE}Enterprise-Grade SSH Manager v1.0.0${NC}"
    echo -e "${NC}"
}

info()    { echo -e "  ${CYAN}[INFO]${NC}    $1"; }
success() { echo -e "  ${GREEN}[  OK ]${NC}    $1"; }
warn()    { echo -e "  ${YELLOW}[WARN]${NC}    $1"; }
error()   { echo -e "  ${RED}[ERROR]${NC}   $1"; }
step()    { echo -e "\n  ${MAGENTA}━━━ $1 ━━━${NC}\n"; }

spinner() {
    local pid=$1
    local msg=$2
    local spin='⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏'
    local i=0
    tput civis 2>/dev/null || true
    while kill -0 "$pid" 2>/dev/null; do
        printf "\r  ${CYAN}[${spin:$i:1}]${NC}  %s" "$msg"
        i=$(( (i+1) % ${#spin} ))
        sleep 0.1
    done
    wait "$pid" 2>/dev/null
    local exit_code=$?
    printf "\r"
    tput cnorm 2>/dev/null || true
    return $exit_code
}

# ─── Pre-flight Checks ──────────────────────────────────────────────────────
check_root() {
    if [[ $EUID -ne 0 ]]; then
        error "This installer must be run as root."
        echo -e "  ${DIM}Try: sudo bash install.sh${NC}"
        exit 1
    fi
}

check_os() {
    if [[ ! -f /etc/os-release ]]; then
        error "Cannot detect OS. Only Linux is supported."
        exit 1
    fi
    source /etc/os-release
    info "Detected OS: ${BOLD}${PRETTY_NAME}${NC}"
}

detect_arch() {
    ARCH=$(uname -m)
    case $ARCH in
        x86_64)  BINARY_ARCH="linux-amd64" ;;
        aarch64) BINARY_ARCH="linux-arm64" ;;
        armv7l)  BINARY_ARCH="linux-arm64" ;;
        *)
            error "Unsupported architecture: $ARCH"
            exit 1
            ;;
    esac
    info "Architecture: ${BOLD}${ARCH}${NC} → binary: ${BINARY_ARCH}"
}

# ─── Installation Steps ─────────────────────────────────────────────────────
install_dependencies() {
    step "Installing Dependencies"

    local deps="curl wget tar gzip"
    local missing=""

    for dep in $deps; do
        if ! command -v "$dep" &>/dev/null; then
            missing="$missing $dep"
        fi
    done

    if [[ -n "$missing" ]]; then
        info "Installing missing packages:${missing}"
        if command -v apt-get &>/dev/null; then
            apt-get update -qq &>/dev/null
            apt-get install -y -qq $missing &>/dev/null
        elif command -v yum &>/dev/null; then
            yum install -y -q $missing &>/dev/null
        elif command -v dnf &>/dev/null; then
            dnf install -y -q $missing &>/dev/null
        fi
        success "Dependencies installed"
    else
        success "All dependencies present"
    fi
}

create_directories() {
    step "Creating Directories"

    mkdir -p "$CONFIG_DIR" "$DATA_DIR" "$DATA_DIR/backups" "$LOG_DIR"
    chmod 750 "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR"

    success "Created $CONFIG_DIR"
    success "Created $DATA_DIR"
    success "Created $LOG_DIR"
}

download_binary() {
    step "Downloading SSH-VPN"

    local url="https://github.com/${REPO}/releases/latest/download/${APP_NAME}-${BINARY_ARCH}"
    local dest="${INSTALL_DIR}/${APP_NAME}"

    info "Downloading from GitHub releases..."
    info "URL: ${DIM}${url}${NC}"

    # Try downloading from releases first; if not available, build from source.
    if curl -fsSL --connect-timeout 10 "$url" -o "$dest" 2>/dev/null; then
        chmod +x "$dest"
        success "Binary installed to ${dest}"
    else
        warn "Pre-built binary not found. Building from source..."
        build_from_source
    fi
}

build_from_source() {
    info "Checking for Go compiler..."

    # Ensure git is available for cloning.
    if ! command -v git &>/dev/null; then
        info "Installing git..."
        if command -v apt-get &>/dev/null; then
            apt-get install -y -qq git &>/dev/null
        elif command -v yum &>/dev/null; then
            yum install -y -q git &>/dev/null
        elif command -v dnf &>/dev/null; then
            dnf install -y -q git &>/dev/null
        fi
    fi

    if ! command -v go &>/dev/null; then
        info "Installing Go..."
        local go_version="1.23.4"
        local go_arch="amd64"
        [[ "$ARCH" == "aarch64" ]] && go_arch="arm64"

        curl -fsSL "https://go.dev/dl/go${go_version}.linux-${go_arch}.tar.gz" -o /tmp/go.tar.gz
        tar -C /usr/local -xzf /tmp/go.tar.gz
        export PATH=$PATH:/usr/local/go/bin
        rm -f /tmp/go.tar.gz
        success "Go ${go_version} installed"
    fi

    info "Cloning repository..."
    local tmpdir=$(mktemp -d)
    if ! git clone --depth 1 "https://github.com/${REPO}.git" "$tmpdir"; then
        error "Failed to clone repository from https://github.com/${REPO}.git"
        rm -rf "$tmpdir"
        exit 1
    fi

    # Validate cloned repo structure.
    if [[ ! -f "$tmpdir/go.mod" ]]; then
        error "Cloned repository is missing go.mod. Aborting."
        rm -rf "$tmpdir"
        exit 1
    fi

    info "Building binary..."
    cd "$tmpdir"

    # Ensure go.sum exists for dependency verification.
    go mod tidy

    go build -ldflags "-s -w -X main.Version=1.0.0" -o "${INSTALL_DIR}/${APP_NAME}" .
    chmod +x "${INSTALL_DIR}/${APP_NAME}"

    cd /
    rm -rf "$tmpdir"

    success "Built and installed to ${INSTALL_DIR}/${APP_NAME}"
}

install_badvpn() {
    step "Installing BadVPN (UDP Gateway)"

    local badvpn_path="/usr/local/bin/badvpn-udpgw"

    if [[ -f "$badvpn_path" ]]; then
        success "BadVPN already installed at ${badvpn_path}"
        return
    fi

    info "Downloading badvpn-udpgw binary..."

    # Try to download pre-compiled binary.
    local badvpn_dl="https://github.com/ambrop72/badvpn/releases/download/1.999.130/badvpn-udpgw"
    if curl -fsSL --connect-timeout 10 "$badvpn_dl" -o "$badvpn_path" 2>/dev/null; then
        chmod +x "$badvpn_path"
        success "BadVPN installed to ${badvpn_path}"
    else
        warn "Could not download BadVPN. You can install it later."
        warn "SSH-VPN will work without it (UDP forwarding disabled)."
    fi
}

create_default_config() {
    step "Creating Configuration"

    local config_file="${CONFIG_DIR}/config.yaml"

    if [[ -f "$config_file" ]]; then
        info "Config already exists, preserving: ${config_file}"
        return
    fi

    cat > "$config_file" <<'YAML'
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  SSH Fortress Configuration
#  https://github.com/SandBaad/SSH-VPN
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

data_dir: /var/lib/sshfortress
log_dir: /var/log/sshfortress

ssh:
  ports: [22]
  config_path: /etc/ssh/sshd_config
  max_auth_tries: 3
  password_auth: true

badvpn:
  enabled: false
  binary_path: /usr/local/bin/badvpn-udpgw
  listen_addr: "127.0.0.1:7300"
  max_clients: 1000
  max_connections_per_client: 10

network:
  auto_optimize: false
  congestion_control: bbr

user_defaults:
  default_expiration_days: 30
  default_max_connections: 2
  shell: /bin/false

monitor:
  refresh_interval_secs: 5
  auth_log_path: /var/log/auth.log
YAML

    chmod 640 "$config_file"
    success "Config created: ${config_file}"
}

create_systemd_service() {
    step "Creating Systemd Service"

    cat > /etc/systemd/system/sshfortress.service <<EOF
[Unit]
Description=SSH-VPN — Enterprise SSH Manager
After=network.target

[Service]
Type=oneshot
ExecStart=${INSTALL_DIR}/${APP_NAME} --config ${CONFIG_DIR}/config.yaml
RemainAfterExit=no
StandardInput=tty
TTYPath=/dev/tty1

[Install]
WantedBy=multi-user.target
EOF

    success "Systemd service created"
}

create_shell_alias() {
    step "Creating Shell Shortcut"

    # Create a simple launcher script.
    cat > /usr/local/bin/sshf <<'EOF'
#!/bin/bash
exec /usr/local/bin/sshfortress "$@"
EOF
    chmod +x /usr/local/bin/sshf

    # Also create 'menu' alias for legacy compatibility.
    cat > /usr/local/bin/menu <<'EOF'
#!/bin/bash
exec /usr/local/bin/sshfortress "$@"
EOF
    chmod +x /usr/local/bin/menu

    success "Commands available: ${BOLD}sshfortress${NC}, ${BOLD}sshf${NC}, ${BOLD}menu${NC}"
}

verify_installation() {
    step "Verifying Installation"

    if [[ -x "${INSTALL_DIR}/${APP_NAME}" ]]; then
        local version=$(${INSTALL_DIR}/${APP_NAME} version 2>/dev/null || echo "unknown")
        success "Binary:  ${INSTALL_DIR}/${APP_NAME} (${version})"
    else
        error "Binary not found!"
        exit 1
    fi

    [[ -f "${CONFIG_DIR}/config.yaml" ]] && success "Config:  ${CONFIG_DIR}/config.yaml" || warn "Config missing"
    [[ -d "$DATA_DIR" ]]                 && success "Data:    ${DATA_DIR}" || warn "Data dir missing"
    [[ -d "$LOG_DIR" ]]                  && success "Logs:    ${LOG_DIR}" || warn "Log dir missing"

    if [[ -x "/usr/local/bin/badvpn-udpgw" ]]; then
        success "BadVPN:  /usr/local/bin/badvpn-udpgw"
    else
        warn "BadVPN:  not installed (optional)"
    fi
}

print_success() {
    echo ""
    echo -e "${GREEN}  ✓  SSH-VPN installed successfully!${NC}"
    echo ""
    echo -e "  ${BOLD}Quick Start:${NC}"
    echo -e "  ${CYAN}┌──────────────────────────────────────────────────┐${NC}"
    echo -e "  ${CYAN}│${NC}  Run the manager:  ${WHITE}${BOLD}sshfortress${NC}                   ${CYAN}│${NC}"
    echo -e "  ${CYAN}│${NC}  Short command:    ${WHITE}${BOLD}sshf${NC}                          ${CYAN}│${NC}"
    echo -e "  ${CYAN}│${NC}  Legacy command:   ${WHITE}${BOLD}menu${NC}                          ${CYAN}│${NC}"
    echo -e "  ${CYAN}│${NC}                                                  ${CYAN}│${NC}"
    echo -e "  ${CYAN}│${NC}  Edit config:      ${DIM}nano /etc/sshfortress/config.yaml${NC} ${CYAN}│${NC}"
    echo -e "  ${CYAN}│${NC}  View version:     ${DIM}sshfortress version${NC}               ${CYAN}│${NC}"
    echo -e "  ${CYAN}└──────────────────────────────────────────────────┘${NC}"
    echo ""
    echo -e "  ${DIM}Documentation: https://github.com/${REPO}${NC}"
    echo ""
}

# ─── Uninstall ───────────────────────────────────────────────────────────────
uninstall() {
    print_banner
    step "Uninstalling SSH-VPN"

    rm -f "${INSTALL_DIR}/${APP_NAME}"
    rm -f /usr/local/bin/sshf
    rm -f /usr/local/bin/menu
    rm -f /etc/systemd/system/sshfortress.service
    rm -f /etc/systemd/system/badvpn-udpgw.service
    systemctl daemon-reload 2>/dev/null || true

    success "Binaries and services removed"
    info "Config and data preserved at: ${CONFIG_DIR}, ${DATA_DIR}"
    info "To remove everything: rm -rf ${CONFIG_DIR} ${DATA_DIR} ${LOG_DIR}"
}

# ─── Main ────────────────────────────────────────────────────────────────────
main() {
    print_banner
    check_root
    check_os
    detect_arch

    install_dependencies
    create_directories
    download_binary
    install_badvpn
    create_default_config
    create_systemd_service
    create_shell_alias
    verify_installation
    print_success
}

# Handle arguments.
case "${1:-}" in
    uninstall|remove)
        check_root
        uninstall
        ;;
    *)
        main
        ;;
esac
