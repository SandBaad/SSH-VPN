// Package udpgw is a fast, built-in implementation of the badvpn-udpgw
// protocol. SSH can only forward TCP, so VPN apps (HTTP Injector, NPV
// Tunnel, HTTP Custom, NetMod, …) carry UDP — voice/video calls, games,
// QUIC, DNS — inside a TCP stream to 127.0.0.1:7300 through the tunnel.
// This server unpacks those datagrams, sends them to the internet and
// streams the replies back.
//
// Wire format (all little-endian except addresses/ports, which are in
// network byte order exactly as badvpn sends them):
//
//	frame   := len:u16le payload[len]
//	payload := flags:u8 conid:u16le addr data
//	addr    := ip4[4] port:u16be            (flags & IPV6 == 0)
//	        |  ip6[16] port:u16be           (flags & IPV6 != 0)
package udpgw

import (
	"encoding/binary"
	"errors"
	"net/netip"
)

// Client flags (protocol/udpgw_proto.h in badvpn).
const (
	FlagKeepalive = 1 << 0
	FlagRebind    = 1 << 1
	FlagDNS       = 1 << 2
	FlagIPv6      = 1 << 3
)

const (
	headerLen = 3      // flags + conid
	addr4Len  = 4 + 2  // ip + port
	addr6Len  = 16 + 2 // ip + port
	// MaxPayload is the largest frame payload (PACKETPROTO_MAXPAYLOAD).
	MaxPayload = 65535
)

var (
	errShort = errors.New("udpgw: packet too short")
	errLarge = errors.New("udpgw: packet too large")
)

// Packet is one decoded frame payload.
type Packet struct {
	Flags uint8
	ConID uint16
	Addr  netip.AddrPort
	Data  []byte // aliases the input buffer
}

// ParsePacket decodes a frame payload (without the 2-byte length prefix).
func ParsePacket(b []byte) (Packet, error) {
	var p Packet
	if len(b) < headerLen {
		return p, errShort
	}
	p.Flags = b[0]
	p.ConID = binary.LittleEndian.Uint16(b[1:3])
	b = b[headerLen:]
	if p.Flags&FlagKeepalive != 0 {
		return p, nil // badvpn ignores everything after a keepalive header
	}
	if p.Flags&FlagIPv6 != 0 {
		if len(b) < addr6Len {
			return p, errShort
		}
		var ip [16]byte
		copy(ip[:], b[:16])
		p.Addr = netip.AddrPortFrom(netip.AddrFrom16(ip), binary.BigEndian.Uint16(b[16:18]))
		b = b[addr6Len:]
	} else {
		if len(b) < addr4Len {
			return p, errShort
		}
		var ip [4]byte
		copy(ip[:], b[:4])
		p.Addr = netip.AddrPortFrom(netip.AddrFrom4(ip), binary.BigEndian.Uint16(b[4:6]))
		b = b[addr4Len:]
	}
	p.Data = b
	return p, nil
}

// FrameLen is the encoded size (including the length prefix) of a frame
// carrying n data bytes for addr.
func FrameLen(addr netip.AddrPort, n int) int {
	if addr.Addr().Is4() {
		return 2 + headerLen + addr4Len + n
	}
	return 2 + headerLen + addr6Len + n
}

// AppendFrame appends a complete frame (length prefix included) to dst.
// The IPV6 flag is set automatically from addr.
func AppendFrame(dst []byte, flags uint8, conid uint16, addr netip.AddrPort, data []byte) ([]byte, error) {
	ip := addr.Addr()
	addrLen := addr4Len
	if !ip.Is4() {
		addrLen = addr6Len
		flags |= FlagIPv6
	} else {
		flags &^= FlagIPv6
	}
	payload := headerLen + addrLen + len(data)
	if payload > MaxPayload {
		return dst, errLarge
	}
	dst = binary.LittleEndian.AppendUint16(dst, uint16(payload))
	dst = append(dst, flags)
	dst = binary.LittleEndian.AppendUint16(dst, conid)
	if ip.Is4() {
		a := ip.As4()
		dst = append(dst, a[:]...)
	} else {
		a := ip.As16()
		dst = append(dst, a[:]...)
	}
	dst = binary.BigEndian.AppendUint16(dst, addr.Port())
	dst = append(dst, data...)
	return dst, nil
}

// AppendKeepalive appends a keepalive frame (used by tests and health checks).
func AppendKeepalive(dst []byte) []byte {
	dst = binary.LittleEndian.AppendUint16(dst, headerLen)
	return append(dst, FlagKeepalive, 0, 0)
}
