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

func ListenRelay(addr string) (net.PacketConn, error) {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("bind UDP %s: %w", addr, err)
	}
	slog.Info("UDP relay listening", "addr", conn.LocalAddr().String())
	return conn, nil
}

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

		result, err := store.registerPeer(token, peerToken, src.(*net.UDPAddr).AddrPort(), targetIndex)
		var mismatch *ipMismatchError
		if errors.As(err, &mismatch) {
			slog.Warn("source IP does not match join IP, dropping", "joinIP", mismatch.joinIP, "from", src)
			continue
		}
		if err != nil {
			slog.Warn("unknown or expired session or peer, dropping", "token", token, "from", src)
			continue
		}

		if len(result.Targets) == 0 {
			continue
		}

		slog.Debug("forwarding", "token", token, "sender", result.SenderIndex, "peers", len(result.Targets), "bytes", len(payload))

		forwarded := buildForwarded(result.SenderIndex, payload)

		for _, t := range result.Targets {
			go conn.WriteTo(forwarded, net.UDPAddrFromAddrPort(t))
		}
	}
}
