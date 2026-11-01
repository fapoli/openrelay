package openrelay

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
)

// packet is a datagram from a client:
// [4B session token BE] [4B peer token] [1B target peer index, 0xFE = keepalive, 0xFF = broadcast] [payload]
type packet struct {
	sessionToken uint32
	peerToken    string
	targetIndex  byte
	payload      []byte
}

func parsePacket(msg []byte) (packet, error) {
	if len(msg) < 9 {
		return packet{}, errors.New("packet too short")
	}
	return packet{
		sessionToken: binary.BigEndian.Uint32(msg[0:4]),
		peerToken:    hex.EncodeToString(msg[4:8]),
		targetIndex:  msg[8],
		payload:      msg[9:],
	}, nil
}

func buildForwarded(senderIndex uint32, payload []byte) []byte {
	out := make([]byte, 4+len(payload))
	binary.BigEndian.PutUint32(out, senderIndex)
	copy(out[4:], payload)
	return out
}

// ListenRelay binds the UDP relay socket on addr.
func ListenRelay(addr string) (*net.UDPConn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, fmt.Errorf("resolve UDP %s: %w", addr, err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("bind UDP %s: %w", addr, err)
	}
	slog.Info("UDP relay listening", "addr", conn.LocalAddr().String())
	return conn, nil
}

// ServeRelay forwards packets between the peers of each session until conn is closed.
func ServeRelay(sessions *SessionStore, conn *net.UDPConn) {
	buf := make([]byte, 65535)
	for {
		n, src, err := conn.ReadFromUDPAddrPort(buf)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			slog.Error("UDP read error", "err", err)
			continue
		}

		pkt, err := parsePacket(buf[:n])
		if err != nil {
			continue
		}

		result, err := sessions.registerPeer(pkt.sessionToken, pkt.peerToken, src, pkt.targetIndex)
		if errors.Is(err, errIPMismatch) {
			slog.Warn("dropping packet", "reason", err, "from", src)
		}
		if err != nil {
			continue
		}
		if len(result.Targets) == 0 {
			continue
		}

		slog.Debug("forwarding", "token", pkt.sessionToken, "sender", result.SenderIndex, "peers", len(result.Targets), "bytes", len(pkt.payload))
		forwarded := buildForwarded(result.SenderIndex, pkt.payload)
		for _, target := range result.Targets {
			conn.WriteToUDPAddrPort(forwarded, target)
		}
	}
}
