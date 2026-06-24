package trace

import "testing"

const inviteMsg = "INVITE sip:2000@127.0.0.1:5072 SIP/2.0\r\n" +
	"Via: SIP/2.0/UDP 127.0.0.1:5070;branch=z9hG4bK.abc\r\n" +
	"From: <sip:1000@pbx.local>;tag=xyz\r\n" +
	"To: <sip:2000@destino.local>\r\n" +
	"Call-ID: call-123\r\n" +
	"CSeq: 1 INVITE\r\n" +
	"Content-Length: 0\r\n\r\n"

const okMsg = "SIP/2.0 200 OK\r\n" +
	"Via: SIP/2.0/UDP 127.0.0.1:5070;branch=z9hG4bK.abc\r\n" +
	"From: <sip:1000@pbx.local>;tag=xyz\r\n" +
	"To: <sip:2000@destino.local>;tag=def\r\n" +
	"Call-ID: call-123\r\n" +
	"CSeq: 1 INVITE\r\n" +
	"Content-Length: 0\r\n\r\n"

func TestParseRequestYResponse(t *testing.T) {
	s := NewStore(100)
	s.Record("out", "UDP", "127.0.0.1:5070", "127.0.0.1:5072", []byte(inviteMsg))
	s.Record("in", "UDP", "127.0.0.1:5070", "127.0.0.1:5072", []byte(okMsg))

	ev := s.Snapshot()
	if len(ev) != 2 {
		t.Fatalf("esperaba 2 eventos, hay %d", len(ev))
	}
	if ev[0].Kind != "request" || ev[0].Method != "INVITE" || ev[0].CallID != "call-123" {
		t.Fatalf("INVITE mal parseado: %+v", ev[0])
	}
	if ev[1].Kind != "response" || ev[1].Code != 200 || ev[1].Method != "INVITE" {
		t.Fatalf("200 OK mal parseado: %+v", ev[1])
	}
	if ev[0].Dir != "out" || ev[1].Dir != "in" {
		t.Fatalf("direcciones mal: %s %s", ev[0].Dir, ev[1].Dir)
	}
}

func TestAgrupacionPorCallID(t *testing.T) {
	s := NewStore(100)
	s.Record("out", "UDP", "a", "b", []byte(inviteMsg))
	s.Record("in", "UDP", "a", "b", []byte(okMsg))

	if got := s.ByCallID("call-123"); len(got) != 2 {
		t.Fatalf("ByCallID esperaba 2, hay %d", len(got))
	}
	calls := s.Calls()
	if len(calls) != 1 || calls[0].CallID != "call-123" || calls[0].Count != 2 {
		t.Fatalf("resumen inesperado: %+v", calls)
	}
}

func TestBufferAcotado(t *testing.T) {
	s := NewStore(3)
	for i := 0; i < 5; i++ {
		s.Record("out", "UDP", "a", "b", []byte(inviteMsg))
	}
	if got := len(s.Snapshot()); got != 3 {
		t.Fatalf("el buffer debería acotar a 3, hay %d", got)
	}
}

func TestIgnoraVacios(t *testing.T) {
	s := NewStore(10)
	s.Record("in", "UDP", "a", "b", []byte("\r\n\r\n"))
	if got := len(s.Snapshot()); got != 0 {
		t.Fatalf("los keepalives vacíos no deberían guardarse, hay %d", got)
	}
}
