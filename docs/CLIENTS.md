# Client setup · راهنمای اتصال

Every SSH-VPN user gets a **connection card** (`sshfortress user add` / `user info`) with the host,
username, password, ports and the WebSocket payload. This page explains where each value goes.

> Menu names differ a little between apps and versions — look for the matching option.

## 1. Pick a connection mode

| Mode | Use when | Server : port | What to set |
|---|---|---|---|
| **SSH direct** | SSH isn't blocked | `host : 22` | user, password |
| **SSH over WebSocket** (payload) | SSH is blocked but HTTP works, or you use a CDN | `host : 80` (any gateway port) | payload below |
| **SSH over SSL/TLS** | DPI blocks SSH/HTTP; you have a whitelisted SNI | `host : 443` (any gateway port) | SNI = any domain |
| **SSL + payload** | both of the above | `host : 443` | SNI + payload |
| **Cloudflare CDN** | the server IP is blocked | `your-domain : 80` or `: 443` | payload, Host = your domain |

Payload (WebSocket upgrade) — replace `[host]` if your app doesn't do it for you:

```
GET / HTTP/1.1[crlf]Host: [host][crlf]Upgrade: websocket[crlf][crlf]
```

Split payloads, `CONNECT` payloads and real WebSocket framing all work too — the gateway detects
what the app sends.

## 2. Turn on UDPGW (voice/video calls, games)

SSH only carries TCP. For Telegram/WhatsApp calls, games and QUIC, enable the app's **UDPGW**
(sometimes "UDP forwarding" or "BadVPN") and set:

```
127.0.0.1:7300
```

7100 and 7200 work too. Without this, calls usually fall back to slow relays or fail.

## 3. App hints

| App | Where things go |
|---|---|
| **HTTP Injector** | SSH settings → host/port/user/password. Payload generator or "Payload" for WebSocket. "SSL/TLS tunnel" + SNI for TLS. Enable UDPGW in SSH settings → `127.0.0.1:7300`. |
| **NPV Tunnel** | Add config → SSH / SSH-SSL / SSH-WS type, fill host, port, user, password, SNI/payload. UDPGW port `7300`. |
| **HTTP Custom** | SSH account as `host:port@user:password`; payload in the payload box; "SSL" + SNI for TLS; enable UDP / UDPGW → `127.0.0.1:7300`. |
| **NetMod** | Server type SSH (+ SSL or + WS), host/port/user/password, SNI or payload; UDPGW `127.0.0.1:7300`. |
| **Termux / Linux / macOS** | see below |

## 4. Command line (tested)

```bash
# SOCKS5 proxy on localhost:1080
ssh -N -D 1080 -p 22 ali@SERVER

# through the TLS gateway with a custom SNI
ssh -N -D 1080 -o "ProxyCommand=openssl s_client -quiet -connect SERVER:443 -servername allowed.example.com" ali@SERVER

# forward udpgw to use with tun2socks / badvpn-tun2socks
ssh -N -D 1080 -L 7300:127.0.0.1:7300 -p 22 ali@SERVER
```

## 5. Cloudflare (CDN) in 4 steps

1. Point a domain/sub-domain at your server IP (A record) and turn the **orange cloud on**.
2. SSL/TLS mode: **Full** (the gateway's self-signed certificate is accepted).
3. Keep the gateway on Cloudflare-compatible ports: HTTP `80, 8080, 8880, 2052, 2082, 2086, 2095`,
   HTTPS `443, 2053, 2083, 2087, 2096, 8443`.
4. In the app use **your domain** as host, WebSocket payload with `Host: your-domain`.
   The real client IPs still show up in `sshfortress online` (read from `CF-Connecting-IP`).

Set the domain on cards: `server.host` in `/etc/sshfortress/config.yaml`, then `sshfortress apply`.

---

<div dir="rtl">

## خلاصهٔ فارسی

۱. **حالت اتصال** را انتخاب کنید: SSH مستقیم (پورت ۲۲)، SSH روی WebSocket با پیلود (پورت ۸۰)، SSH روی SSL/TLS با هر SNI (پورت ۴۴۳)، یا از طریق Cloudflare با دامنهٔ خودتان.

۲. پیلود WebSocket:

</div>

```
GET / HTTP/1.1[crlf]Host: [host][crlf]Upgrade: websocket[crlf][crlf]
```

<div dir="rtl">

۳. **برای تماس تلگرام/واتساپ و بازی** گزینهٔ UDPGW (گاهی UDP forwarding یا BadVPN) را در اپ روشن کنید و آدرس `127.0.0.1:7300` را بدهید. بدون این گزینه، تماس‌ها کند می‌شوند یا وصل نمی‌شوند.

۴. **Cloudflare:** دامنه را به IP سرور وصل کنید و ابر نارنجی را روشن کنید، حالت SSL را روی Full بگذارید، از پورت‌های سازگار با Cloudflare استفاده کنید و در اپ، دامنه را به‌عنوان host و در پیلود بدهید. IP واقعی کاربران همچنان در `sshfortress online` دیده می‌شود.

۵. اگر وصل نشد: روی سرور `sshfortress doctor` را اجرا کنید؛ مشکل و راه‌حل را می‌نویسد.

</div>
