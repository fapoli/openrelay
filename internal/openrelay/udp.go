package openrelay

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
)

// Packet: [4B session token BE] [4B peer token hex] [1B target peer index, 0xFE = keepalive, 0xFF = broadcast] [payload]

func parsePacket(msg []byte) (token uint32, peerToken string, targetIndex byte, payload []byte, err error) {
	if len(msg) < 9 {
		return 0, "", 0, nil, errors.New("packet too short")
	}
	token = binary.BigEndian.Uint32(msg[0:4])
	peerToken = hex.EncodeToString(msg[4:8])
	targetIndex = msg[8]
	payload = msg[9:]
	return token, peerToken, targetIndex, payload, nil
}

func buildForwarded(senderIndex uint32, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out, senderIndex)
	copy(out[4:], payload)
	return out
}

// ListenRelay binds the UDP relay socket.
func ListenRelay(addr string) (net.PacketConn, error) {
	conn, err := net.ListenPacket("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("bind UDP %s: %w", addr, err)
	}
	slog.Info("UDP relay listening", "addr", conn.LocalAddr().String())
	return conn, nil
}

// ServeRelay reads and forwards packets until conn is closed.
func ServeRelay(store *SessionStore, conn net.PacketConn) {
	buf := make([]byte, 65535)
	for {
		n, src, err := conn.ReadFrom(buf)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			slog.Error("UDP read error", "err", err)
			continue
		}

		token, peerToken, targetIndex, payload, err := parsePacket(buf[:n])
		if err != nil {
			slog.Warn("packet too short, dropping", "bytes", n, "from", src)
			continue
		}

		udpSrc := src.(*net.UDPAddr)
		result := store.registerPeer(token, peerToken, udpSrc.IP.String(), udpSrc.Port, targetIndex)
		if result == nil {
			slog.Warn("unknown session or peer token, dropping", "token", token, "from", src)
			continue
		}

		if len(result.Targets) == 0 {
			continue
		}

		slog.Debug("forwarding", "token", token, "sender", result.SenderIndex, "peers", len(result.Targets), "bytes", len(payload))

		forwarded := buildForwarded(result.SenderIndex, payload)

		for _, t := range result.Targets {
			dest, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", t.address, t.port))
			if err != nil {
				continue
			}
			go conn.WriteTo(forwarded, dest)
		}
	}
}
