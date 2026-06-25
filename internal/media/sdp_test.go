package media

import "testing"

func TestBuildAndParseOffer(t *testing.T) {
	off := BuildOffer("192.168.1.5", 40000)
	d, err := Parse(off)
	if err != nil {
		t.Fatalf("Parse de la oferta propia falló: %v", err)
	}
	if d.ConnIP != "192.168.1.5" {
		t.Errorf("ConnIP = %q, se esperaba 192.168.1.5", d.ConnIP)
	}
	if d.Port != 40000 {
		t.Errorf("Port = %d, se esperaba 40000", d.Port)
	}
	if !d.HasPayload(PayloadPCMU) || !d.HasPayload(PayloadPCMA) {
		t.Errorf("la oferta debería anunciar PCMU y PCMA: %+v", d.Formats)
	}
	if d.PTime != 20 {
		t.Errorf("PTime = %d, se esperaba 20", d.PTime)
	}
	if pt, ok := ChooseCodec(d); !ok || pt != PayloadPCMU {
		t.Errorf("ChooseCodec = (%d,%v), se esperaba (0,true)", pt, ok)
	}
}

// SDP de respuesta "real" (estilo Asterisk): c= a nivel de sesión, un solo códec.
func TestParseRealAnswer(t *testing.T) {
	ans := "v=0\r\n" +
		"o=- 12345 1 IN IP4 10.0.0.1\r\n" +
		"s=Asterisk\r\n" +
		"c=IN IP4 10.0.0.9\r\n" +
		"t=0 0\r\n" +
		"m=audio 18000 RTP/AVP 8\r\n" +
		"a=rtpmap:8 PCMA/8000\r\n" +
		"a=ptime:20\r\n"
	d, err := Parse([]byte(ans))
	if err != nil {
		t.Fatalf("Parse de la respuesta falló: %v", err)
	}
	if d.ConnIP != "10.0.0.9" || d.Port != 18000 {
		t.Errorf("destino mal parseado: %s:%d", d.ConnIP, d.Port)
	}
	if pt, ok := ChooseCodec(d); !ok || pt != PayloadPCMA {
		t.Errorf("ChooseCodec = (%d,%v), se esperaba (8,true)", pt, ok)
	}
}

func TestParseNoAudio(t *testing.T) {
	if _, err := Parse([]byte("v=0\r\nc=IN IP4 1.2.3.4\r\n")); err == nil {
		t.Fatal("se esperaba error: SDP sin m=audio")
	}
}

func TestChooseCodecNone(t *testing.T) {
	d := Description{ConnIP: "1.2.3.4", Port: 5000, Formats: []uint8{99}}
	if _, ok := ChooseCodec(d); ok {
		t.Fatal("no debería elegir códec si solo hay payloads desconocidos")
	}
}
