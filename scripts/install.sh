#!/usr/bin/env bash
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
#  SSH-VPN — installer / updater / uninstaller
#  https://github.com/SandBaad/SSH-VPN
#
#  bash <(curl -fsSL https://raw.githubusercontent.com/SandBaad/SSH-VPN/main/scripts/install.sh)
#  ... install.sh update | uninstall
#
#  Options:
#    --gateway-ports 80,443   ports for the SSH/WebSocket/TLS gateway
#                             (default: whichever of 80 and 443 are free)
#    --no-gateway             don't enable the gateway
#    --host vpn.example.com   domain/IP printed on user connection cards
#    --from-source            build from source instead of downloading
#    --version v2.0.0         install a specific release
#
#  Mirrors: SSHVPN_DOWNLOAD_URL=https://my.mirror/sshvpn bash install.sh
#  (a folder with sshfortress-linux-<arch> and sha256sums.txt)
# ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
set -euo pipefail

REPO="SandBaad/SSH-VPN"
APP="sshfortress"
BIN="/usr/local/bin/${APP}"
CONFIG="/etc/sshfortress/config.yaml"
GO_VERSION="1.23.4"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[0;33m'; CYAN='\033[0;36m'
MAGENTA='\033[0;35m'; WHITE='\033[1;37m'; DIM='\033[2m'; BOLD='\033[1m'; NC='\033[0m'

info()    { echo -e "  ${CYAN}[INFO]${NC}  $*"; }
ok()      { echo -e "  ${GREEN}[ OK ]${NC}  $*"; }
warn()    { echo -e "  ${YELLOW}[WARN]${NC}  $*"; }
die()     { echo -e "  ${RED}[FAIL]${NC}  $*" >&2; exit 1; }
step()    { echo -e "\n  ${MAGENTA}━━━ $* ━━━${NC}"; }

banner() {
    echo -e "${CYAN}"
    echo "   ███████╗███████╗██╗  ██╗      ██╗   ██╗██████╗ ███╗   ██╗"
    echo "   ██╔════╝██╔════╝██║  ██║      ██║   ██║██╔══██╗████╗  ██║"
    echo "   ███████╗███████╗███████║█████╗██║   ██║██████╔╝██╔██╗ ██║"
    echo "   ╚════██║╚════██║██╔══██║╚════╝╚██╗ ██╔╝██╔═══╝ ██║╚██╗██║"
    echo "   ███████║███████║██║  ██║       ╚████╔╝ ██║     ██║ ╚████║"
    echo "   ╚══════╝╚══════╝╚═╝  ╚═╝        ╚═══╝  ╚═╝     ╚═╝  ╚═══╝"
    echo -e "${NC}${WHITE}        Fast · Stable · Calls work — SSH VPN server v2${NC}\n"
}

# ─── arguments ──────────────────────────────────────────────────────────────
ACTION="install"; GW_PORTS=""; NO_GW=0; HOST=""; FROM_SOURCE=0; RELEASE="latest"
while [[ $# -gt 0 ]]; do
    case "$1" in
        install|update|upgrade) ACTION="install" ;;
        uninstall|remove)       ACTION="uninstall" ;;
        --gateway-ports)        GW_PORTS="${2:?}"; shift ;;
        --no-gateway)           NO_GW=1 ;;
        --host)                 HOST="${2:?}"; shift ;;
        --from-source)          FROM_SOURCE=1 ;;
        --version)              RELEASE="${2:?}"; shift ;;
        -h|--help)              sed -n '2,20p' "$0" 2>/dev/null || true; exit 0 ;;
        *) die "unknown option: $1" ;;
    esac
    shift
done

[[ $EUID -eq 0 ]] || die "run as root (sudo bash install.sh)"

# ─── system detection ───────────────────────────────────────────────────────
detect() {
    [[ -f /etc/os-release ]] || die "cannot detect the OS"
    # read in a subshell: os-release defines VERSION, NAME, ID… globally
    local pretty
    pretty="$(. /etc/os-release && echo "${PRETTY_NAME:-$NAME}")"
    info "OS: ${BOLD}${pretty}${NC}"
    case "$(uname -m)" in
        x86_64|amd64)   ARCH="amd64" ;;
        aarch64|arm64)  ARCH="arm64" ;;
        armv7l|armv7*)  ARCH="armv7" ;;
        *) die "unsupported CPU architecture: $(uname -m)" ;;
    esac
    info "Architecture: ${BOLD}${ARCH}${NC}"
    if   command -v apt-get >/dev/null; then PM="apt"
    elif command -v dnf     >/dev/null; then PM="dnf"
    elif command -v yum     >/dev/null; then PM="yum"
    else PM=""; fi
    command -v systemctl >/dev/null || die "systemd is required"
}

pkg_install() {
    case "$PM" in
        apt) DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null
             DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" >/dev/null ;;
        dnf) dnf install -y -q "$@" >/dev/null ;;
        yum) yum install -y -q "$@" >/dev/null ;;
        *)   warn "no package manager found; please install: $*"; return 1 ;;
    esac
}

dependencies() {
    step "Dependencies"
    local need=()
    command -v curl     >/dev/null || need+=(curl)
    command -v iptables >/dev/null || need+=(iptables)
    command -v tar      >/dev/null || need+=(tar)
    [[ -e /etc/ssl/certs/ca-certificates.crt || -e /etc/pki/tls/certs/ca-bundle.crt ]] || need+=(ca-certificates)
    if ! command -v sshd >/dev/null && [[ ! -x /usr/sbin/sshd ]]; then
        need+=(openssh-server)
    fi
    if [[ ${#need[@]} -gt 0 ]]; then
        info "installing: ${need[*]}"
        pkg_install "${need[@]}" || die "could not install ${need[*]}"
    fi
    ok "dependencies ready"
}

# ─── binary ─────────────────────────────────────────────────────────────────
download() {
    local base
    if [[ -n "${SSHVPN_DOWNLOAD_URL:-}" ]]; then
        base="${SSHVPN_DOWNLOAD_URL%/}"
    elif [[ "$RELEASE" == "latest" ]]; then
        base="https://github.com/${REPO}/releases/latest/download"
    else
        base="https://github.com/${REPO}/releases/download/${RELEASE}"
    fi
    local tmp; tmp="$(mktemp -d)"
    info "downloading ${APP}-linux-${ARCH} (${RELEASE})"
    if ! curl -fsSL --retry 3 --connect-timeout 15 "${base}/${APP}-linux-${ARCH}" -o "${tmp}/${APP}"; then
        rm -rf "$tmp"; return 1
    fi
    if curl -fsSL --retry 3 --connect-timeout 15 "${base}/sha256sums.txt" -o "${tmp}/sums" 2>/dev/null; then
        local want got
        want="$(awk -v f="${APP}-linux-${ARCH}" '$2==f {print $1}' "${tmp}/sums")"
        got="$(sha256sum "${tmp}/${APP}" | awk '{print $1}')"
        [[ -n "$want" && "$want" == "$got" ]] || { rm -rf "$tmp"; die "checksum mismatch — download corrupted or tampered"; }
        ok "checksum verified"
    else
        warn "no checksum file in the release; skipping verification"
    fi
    install -m 0755 "${tmp}/${APP}" "${BIN}.new" && mv -f "${BIN}.new" "$BIN"
    rm -rf "$tmp"
}

build_from_source() {
    info "building from source"
    command -v git >/dev/null || pkg_install git || die "git is required"
    if ! command -v go >/dev/null && [[ ! -x /usr/local/go/bin/go ]]; then
        local garch="$ARCH"; [[ "$garch" == "armv7" ]] && garch="armv6l"
        info "installing Go ${GO_VERSION}"
        curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${garch}.tar.gz" -o /tmp/go.tgz
        rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm -f /tmp/go.tgz
    fi
    export PATH="$PATH:/usr/local/go/bin"
    local src; src="$(mktemp -d)"
    git clone --depth 1 "https://github.com/${REPO}.git" "$src" >/dev/null 2>&1 || die "git clone failed"
    local ver; ver="$(git -C "$src" describe --tags --always 2>/dev/null || echo dev)"
    (cd "$src" && CGO_ENABLED=0 go build -trimpath \
        -ldflags "-s -w -X sshfortress/internal/buildinfo.Version=${ver#v}" -o "${BIN}.new" .) || die "build failed"
    mv -f "${BIN}.new" "$BIN" && chmod 0755 "$BIN"
    rm -rf "$src"
}

install_binary() {
    step "SSH-VPN binary"
    if [[ $FROM_SOURCE -eq 1 ]] || ! download; then
        [[ $FROM_SOURCE -eq 1 ]] || warn "no release binary available — building from source"
        build_from_source
    fi
    for alias in sshf menu; do
        # v1 created these; only (re)write them if they are ours
        if [[ ! -e /usr/local/bin/$alias ]] || grep -q "$APP" "/usr/local/bin/$alias" 2>/dev/null; then
            printf '#!/bin/sh\nexec %s "$@"\n' "$BIN" > "/usr/local/bin/$alias"
            chmod +x "/usr/local/bin/$alias"
        fi
    done
    ok "$("$BIN" version) → $BIN"
}

# ─── setup ──────────────────────────────────────────────────────────────────
# A port is busy if any socket is LISTENing on it (read /proc: works even
# where `ss`/`netstat` are not installed).
port_free() {
    local hex f files=()
    hex="$(printf '%04X' "$1")"
    for f in /proc/net/tcp /proc/net/tcp6; do [[ -r $f ]] && files+=("$f"); done
    [[ ${#files[@]} -gt 0 ]] || return 0
    ! awk -v h=":${hex}" '$4 == "0A" && substr($2, length($2) - 4) == h { f = 1 } END { exit !f }' "${files[@]}"
}

configure() {
    step "Configuration"
    local args=()
    if [[ $NO_GW -eq 1 ]]; then
        args+=(--no-gateway)
    elif [[ -n "$GW_PORTS" ]]; then
        args+=(--gateway-ports "$GW_PORTS")
    elif [[ ! -f "$CONFIG" ]]; then
        local free=()
        for p in 80 443; do port_free "$p" && free+=("$p"); done
        if [[ ${#free[@]} -gt 0 ]]; then
            GW_PORTS="$(IFS=,; echo "${free[*]}")"
            args+=(--gateway-ports "$GW_PORTS")
            info "gateway will use free port(s): $GW_PORTS"
        else
            warn "ports 80 and 443 are busy — gateway disabled (enable later: sshfortress gateway enable --ports 8080)"
            args+=(--no-gateway)
        fi
    fi
    [[ -n "$HOST" ]] && args+=(--host "$HOST")
    "$BIN" setup "${args[@]}"
}

summary() {
    local ip; ip="$(curl -4 -fsS --max-time 4 https://api.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')"
    echo
    echo -e "  ${GREEN}✓ SSH-VPN is installed and running${NC}"
    echo
    echo -e "  ${BOLD}Server${NC}        ${HOST:-$ip}"
    echo -e "  ${BOLD}Manager${NC}       sshfortress   ${DIM}(or: sshf, menu)${NC}"
    echo -e "  ${BOLD}New user${NC}      sshfortress user add ali --days 30 --limit 2"
    echo -e "  ${BOLD}Who's online${NC}  sshfortress online"
    echo -e "  ${BOLD}Health check${NC}  sshfortress doctor"
    echo
    echo -e "  ${DIM}Calls/games: set UDPGW to 127.0.0.1:7300 in the client app.${NC}"
    echo -e "  ${DIM}Docs: https://github.com/${REPO}${NC}"
    echo
}

uninstall() {
    step "Uninstalling"
    if [[ -x "$BIN" ]]; then
        "$BIN" uninstall --yes || warn "cleanup reported problems"
    fi
    rm -f "$BIN"
    for alias in sshf menu; do
        grep -q "$APP" "/usr/local/bin/$alias" 2>/dev/null && rm -f "/usr/local/bin/$alias"
    done
    ok "removed. Users and data are kept in /var/lib/sshfortress and /etc/sshfortress"
    info "remove everything: rm -rf /etc/sshfortress /var/lib/sshfortress /var/log/sshfortress"
}

banner
if [[ "$ACTION" == "uninstall" ]]; then
    uninstall
    exit 0
fi
detect
dependencies
install_binary
configure
summary
