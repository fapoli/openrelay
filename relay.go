package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
)

// Packet: [4B session token BE] [4B peer token hex] [1B target peer index, 0xFF = broadcast] [payload]

func (a *App) runRelay(addr string) {
	conn, err := net.ListenPacket("udp4", addr)
	if err != nil {
		slog.Error("failed to bind UDP socket", "err", err)
		return
	}
	defer conn.Close()
	slog.Info("UDP relay listening", "addr", addr)

	buf := make([]byte, 65535)
	for {
		n, src, err := conn.ReadFrom(buf)
		if err != nil {
			slog.Error("UDP read error", "err", err)
			continue
		}

		msg := buf[:n]
		if len(msg) < 9 {
			slog.Warn("packet too short, dropping", "bytes", n, "from", src)
			continue
		}

		token := binary.BigEndian.Uint32(msg[0:4])
		peerToken := hex.EncodeToString(msg[4:8])
		targetIndex := msg[8]
		payload := msg[9:]

		udpSrc := src.(*net.UDPAddr)
		result := a.registerPeer(token, peerToken, udpSrc.IP.String(), udpSrc.Port, targetIndex)
		if result == nil {
			slog.Warn("unknown session or peer token, dropping", "token", token, "from", src)
			continue
		}

		slog.Debug("forwarding", "token", token, "sender", result.SenderIndex, "peers", len(result.Targets), "bytes", len(payload))

		// Prepend sender index (4B BE) so receivers know who sent it
		forwarded := make([]byte, 4+len(payload))
		binary.BigEndian.PutUint32(forwarded, result.SenderIndex)
		copy(forwarded[4:], payload)

		for _, t := range result.Targets {
			dest, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", t.address, t.port))
			if err != nil {
				continue
			}
			go conn.WriteTo(forwarded, dest)
		}
	}
}
