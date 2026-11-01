package openrelay

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"testing"
)

func TestParsePacket_tooShort(t *testing.T) {
	for _, n := range []int{0, 1, 8} {
		_, _, _, _, err := parsePacket(make([]byte, n))
		if err == nil {
			t.Errorf("expected error for %d-byte packet", n)
		}
	}
}

func TestParsePacket_exactHeader(t *testing.T) {
	var msg [9]byte
	binary.BigEndian.PutUint32(msg[0:4], 0xDEADBEEF)
	copy(msg[4:8], []byte{0x01, 0x02, 0x03, 0x04})
	msg[8] = 0xFF

	token, peerToken, targetIndex, payload, err := parsePacket(msg[:])
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != 0xDEADBEEF {
		t.Errorf("token: got %x, want DEADBEEF", token)
	}
	if peerToken != hex.EncodeToString([]byte{0x01, 0x02, 0x03, 0x04}) {
		t.Errorf("peerToken: got %q", peerToken)
	}
	if targetIndex != 0xFF {
		t.Errorf("targetIndex: got %x, want FF", targetIndex)
	}
	if len(payload) != 0 {
		t.Errorf("expected empty payload, got %d bytes", len(payload))
	}
}

func TestParsePacket_withPayload(t *testing.T) {
	msg := make([]byte, 9+5)
	copy(msg[9:], []byte("hello"))

	_, _, _, payload, err := parsePacket(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(payload, []byte("hello")) {
		t.Errorf("payload: got %q, want %q", payload, "hello")
	}
}

func TestBuildForwarded_prependsSenderIndex(t *testing.T) {
	out := buildForwarded(42, []byte("data"))
	if len(out) != 8 {
		t.Fatalf("expected 8 bytes, got %d", len(out))
	}
	if binary.BigEndian.Uint32(out[0:4]) != 42 {
		t.Errorf("sender index: got %d, want 42", binary.BigEndian.Uint32(out[0:4]))
	}
	if !bytes.Equal(out[4:], []byte("data")) {
		t.Errorf("payload: got %q, want %q", out[4:], "data")
	}
}

func TestBuildForwarded_emptyPayload(t *testing.T) {
	out := buildForwarded(0, nil)
	if len(out) != 4 {
		t.Fatalf("expected 4 bytes, got %d", len(out))
	}
	if binary.BigEndian.Uint32(out) != 0 {
		t.Errorf("expected zero sender index")
	}
}

func TestListenRelay_portInUse(t *testing.T) {
	taken, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	defer taken.Close()
	if conn, err := ListenRelay(taken.LocalAddr().String()); err == nil {
		conn.Close()
		t.Error("expected error binding a port already in use")
	}
}
