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

// TestParseIdentidades comprueba que se extraen Request-URI, From y To.
func TestParseIdentidades(t *testing.T) {
	s := NewStore(100)
	s.Record("out", "UDP", "127.0.0.1:5070", "127.0.0.1:5072", []byte(inviteMsg))
	ev := s.Snapshot()[0]
	if ev.ReqURI != "sip:2000@127.0.0.1:5072" {
		t.Fatalf("ReqURI inesperado: %q", ev.ReqURI)
	}
	if ev.FromURI != "<sip:1000@pbx.local>;tag=xyz" {
		t.Fatalf("FromURI inesperado: %q", ev.FromURI)
	}
	if ev.ToURI != "<sip:2000@destino.local>" {
		t.Fatalf("ToURI inesperado: %q", ev.ToURI)
	}
}

const byeMsg = "BYE sip:2000@127.0.0.1:5072 SIP/2.0\r\n" +
	"From: <sip:1000@pbx.local>;tag=xyz\r\n" +
	"To: <sip:2000@destino.local>;tag=def\r\n" +
	"Call-ID: call-123\r\n" +
	"CSeq: 2 BYE\r\n" +
	"Content-Length: 0\r\n\r\n"

const busyMsg = "SIP/2.0 486 Busy Here\r\n" +
	"From: <sip:1000@pbx.local>;tag=xyz\r\n" +
	"To: <sip:2000@destino.local>;tag=zzz\r\n" +
	"Call-ID: call-486\r\n" +
	"CSeq: 1 INVITE\r\n" +
	"Content-Length: 0\r\n\r\n"

// TestEstadoDerivado comprueba la máquina de estados del resumen por diálogo.
func TestEstadoDerivado(t *testing.T) {
	s := NewStore(100)
	// Llamada establecida y luego colgada -> TERMINATED-200.
	s.Record("out", "UDP", "a", "b", []byte(inviteMsg))
	s.Record("in", "UDP", "a", "b", []byte(okMsg))
	s.Record("out", "UDP", "a", "b", []byte(byeMsg))
	// Llamada rechazada con 486 -> FAILED-486.
	s.Record("out", "UDP", "a", "b", []byte("INVITE sip:2000@x SIP/2.0\r\nCall-ID: call-486\r\nCSeq: 1 INVITE\r\nContent-Length: 0\r\n\r\n"))
	s.Record("in", "UDP", "a", "b", []byte(busyMsg))

	calls := s.Calls()
	byID := map[string]CallSummary{}
	for _, c := range calls {
		byID[c.CallID] = c
	}
	if got := byID["call-123"].State; got != "TERMINATED-200" {
		t.Fatalf("call-123: esperaba TERMINATED-200, hay %q", got)
	}
	if got := byID["call-123"].DurationSec; got < 0 {
		t.Fatalf("call-123: duración negativa %d", got)
	}
	if got := byID["call-486"].State; got != "FAILED-486" {
		t.Fatalf("call-486: esperaba FAILED-486, hay %q", got)
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
