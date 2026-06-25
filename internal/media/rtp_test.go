package media

import (
	"bytes"
	"testing"
)

func TestHeaderRoundTrip(t *testing.T) {
	h := Header{
		Marker:         true,
		PayloadType:    PayloadPCMA,
		SequenceNumber: 0x1234,
		Timestamp:      0xDEADBEEF,
		SSRC:           0xCAFEBABE,
	}
	raw := h.Marshal()
	if len(raw) != HeaderSize {
		t.Fatalf("cabecera de %d bytes, se esperaban %d", len(raw), HeaderSize)
	}
	// Versión en los dos bits altos del primer byte.
	if raw[0]>>6 != Version {
		t.Fatalf("versión mal serializada: %d", raw[0]>>6)
	}

	pkt := append(raw, 0xAA, 0xBB) // cabecera + 2 bytes de payload
	got, payload, err := ParsePacket(pkt)
	if err != nil {
		t.Fatalf("ParsePacket devolvió error: %v", err)
	}
	if got != h {
		t.Fatalf("cabecera distinta tras round-trip:\n got %+v\nwant %+v", got, h)
	}
	if !bytes.Equal(payload, []byte{0xAA, 0xBB}) {
		t.Fatalf("payload distinto: %v", payload)
	}
}

func TestParsePacketTooShort(t *testing.T) {
	if _, _, err := ParsePacket([]byte{0x80, 0x00, 0x01}); err == nil {
		t.Fatal("se esperaba error con un paquete demasiado corto")
	}
}

func TestParsePacketBadVersion(t *testing.T) {
	raw := Header{}.Marshal()
	raw[0] = 0x40 // versión 1, no soportada
	if _, _, err := ParsePacket(raw); err == nil {
		t.Fatal("se esperaba error con versión RTP no soportada")
	}
}

func TestParsePacketCSRC(t *testing.T) {
	// Cabecera con CC=2 → 8 bytes de CSRC tras los 12 fijos, luego el payload.
	raw := Header{PayloadType: PayloadPCMU}.Marshal()
	raw[0] |= 0x02 // CC=2
	pkt := append(raw, make([]byte, 8)...)
	pkt = append(pkt, 0x11, 0x22)
	_, payload, err := ParsePacket(pkt)
	if err != nil {
		t.Fatalf("ParsePacket con CSRC devolvió error: %v", err)
	}
	if !bytes.Equal(payload, []byte{0x11, 0x22}) {
		t.Fatalf("payload tras CSRC distinto: %v", payload)
	}
}
