# Changelog

## v2.0.0 — 2026-11

A rewrite of everything that decides speed and stability. Config files, users and data from
v1 keep working — just run the installer again.

### New
- **Built-in UDP gateway** (badvpn-udpgw protocol) for voice/video calls, games, QUIC and DNS:
  ports 7100/7200/7300, 512 flows per client, endpoint-independent replies for P2P calls,
  drop-don't-queue under congestion, per-session flow counts, watchdog. External badvpn is still
  available (`udpgw.mode: badvpn`).
- **Multi-protocol gateway**: one port speaks raw SSH, HTTP payload (incl. split and `CONNECT`),
  WebSocket (raw-after-upgrade and real frames) and TLS with any SNI; works behind Cloudflare and
  shows the real client IP. Sockets are handed straight to `sshd -i` with `TCP_NODELAY` on.
- **Daemon** (`sshfortress.service`): device limits, exact-minute expiry, **traffic quotas**,
  per-user traffic accounting (TCP + UDP), event log, live state for the UI. Gateway sessions
  survive daemon restarts.
- **Managed sshd tuning** in `sshd_config.d/00-sshvpn.conf` (or inline blocks on older systems),
  validated with `sshd -t` and rolled back on failure; VPN-only `Match Group` rules; Ubuntu
  `ssh.socket` handled; relaxed OpenSSH 9.8+ per-source penalties for carrier-NAT users.
- **Kernel profile**: BBR + fq, `tcp_notsent_lowat`, no slow-start-after-idle, RAM-sized buffers;
  originals saved so `optimize reset` truly restores them.
- **Firewall**: VPN users can't reach services on the server or send SMTP.
- **`sshfortress doctor`**: diagnoses common problems and prints the fix.
- **CLI** for everything, with `--json`: `user add/list/info/renew/expiry/passwd/lock/unlock/
  limit/traffic/kick/del/purge`, `online`, `status`, `optimize`, `gateway`, `udpgw`, `backup`,
  `apply`, `setup`, `uninstall`.
- **Connection cards** with host, ports, payload and UDPGW settings.
- **Backups carry password hashes**: restore on a new VPS and users keep their passwords.
- New TUI: live dashboard, online users with speed, user actions with confirmations, gateway/UDP
  view, doctor, optimizer, backup.
- Installer: checksum-verified release downloads, mirror support (`SSHVPN_DOWNLOAD_URL`), picks
  free gateway ports, `update` and `uninstall`; release workflow publishes amd64/arm64/armv7.

### Fixed (v1 bugs)
- Session detection never worked (`ss` output parsed with wrong columns) — the limiter and the
  monitor always saw zero sessions. Replaced by a `/proc` scanner.
- The connection limiter was never started.
- Only one process could open the database at a time (the TUI and any other command locked each
  other out).
- BadVPN install downloaded a file that doesn't exist; flow limit of 10 broke calls.
- Backup size was measured before the archive was flushed; restore didn't recreate Linux accounts;
  deleting a backup left its record.
- `sshfortress version` always printed `dev` (ldflags pointed at the wrong package); `go.sum` was missing.
- Generated passwords could contain characters that the validator then rejected.
- Port changes were ignored on Ubuntu 22.10+ (socket activation) and password logins were
  silently disabled by cloud-init drop-ins.
- The `sshfortress.service` unit tried to run the UI on `tty1` at boot.
- Users were deleted without confirmation; the monitor's "auto refresh" never refreshed.
