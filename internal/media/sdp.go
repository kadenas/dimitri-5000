// SDP mínimo (RFC 4566) para negociar audio G.711. No usamos una librería de SDP:
// generamos y parseamos a mano lo justo para ofertar/aceptar PCMU/PCMA y saber a
// qué IP:puerto enviar el RTP. El re-INVITE de HOLD (a=sendonly/inactive) llegará
// en el sub-paso 5.4; aquí solo sendrecv.
package media

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Números de payload estáticos de G.711 (RFC 3551).
const (
	PayloadPCMU = 0 // G.711 µ-law
	PayloadPCMA = 8 // G.711 A-law
)

// CodecName devuelve el nombre rtpmap del payload (para el SDP y para la web).
func CodecName(pt uint8) string {
	switch pt {
	case PayloadPCMU:
		return "PCMU"
	case PayloadPCMA:
		return "PCMA"
	default:
		return "?"
	}
}

// Description resume la media de audio extraída de un SDP: a dónde enviar el RTP
// (IP de la línea c= y puerto de la m=audio) y qué códecs anuncia el otro extremo.
type Description struct {
	ConnIP  string  // IP de la línea c=IN IP4 (destino del RTP)
	Port    int     // puerto de la m=audio (0 = media en espera, sin RTP)
	Formats []uint8 // payload types anunciados en la m=audio
	PTime   int     // a=ptime en ms (0 si no se indica)
}

// HasPayload indica si el SDP anunció ese payload type.
func (d Description) HasPayload(pt uint8) bool {
	for _, f := range d.Formats {
		if f == pt {
			return true
		}
	}
	return false
}

// BuildOffer construye una oferta SDP de audio ofreciendo G.711 µ-law y A-law en
// la IP:puerto locales donde escucha nuestra sesión RTP.
func BuildOffer(ip string, port int) []byte {
	return buildSDP(ip, port, []uint8{PayloadPCMU, PayloadPCMA})
}

// BuildAnswer construye una respuesta SDP aceptando UN códec (el negociado).
func BuildAnswer(ip string, port int, pt uint8) []byte {
	return buildSDP(ip, port, []uint8{pt})
}

// buildSDP arma el cuerpo SDP con los payloads dados. El primero de la lista marca
// la preferencia. Líneas terminadas en CRLF, como exige SIP para los cuerpos.
func buildSDP(ip string, port int, pts []uint8) []byte {
	fmtList := make([]string, len(pts))
	for i, pt := range pts {
		fmtList[i] = strconv.Itoa(int(pt))
	}
	var b bytes.Buffer
	b.WriteString("v=0\r\n")
	fmt.Fprintf(&b, "o=dimitri 0 0 IN IP4 %s\r\n", ip)
	b.WriteString("s=dimitri-5000\r\n")
	fmt.Fprintf(&b, "c=IN IP4 %s\r\n", ip)
	b.WriteString("t=0 0\r\n")
	fmt.Fprintf(&b, "m=audio %d RTP/AVP %s\r\n", port, strings.Join(fmtList, " "))
	for _, pt := range pts {
		fmt.Fprintf(&b, "a=rtpmap:%d %s/8000\r\n", pt, CodecName(pt))
	}
	b.WriteString("a=ptime:20\r\n")
	b.WriteString("a=sendrecv\r\n")
	return b.Bytes()
}

// Parse extrae de un cuerpo SDP la media de audio que necesitamos: IP de destino
// (c=), puerto (m=audio) y payloads ofertados. Tolera c= a nivel de sesión (lo
// habitual en Asterisk/Kamailio); un c= dentro de la m= lo sobrescribe.
func Parse(sdp []byte) (Description, error) {
	var d Description
	foundAudio := false
	inAudio := false

	sc := bufio.NewScanner(bytes.NewReader(sdp))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "m=audio "):
			foundAudio = true
			inAudio = true
			campos := strings.Fields(line) // m=audio PORT RTP/AVP fmt...
			if len(campos) < 4 {
				return d, fmt.Errorf("línea m=audio inválida: %q", line)
			}
			p, err := strconv.Atoi(campos[1])
			if err != nil {
				return d, fmt.Errorf("puerto de media inválido en %q", line)
			}
			d.Port = p
			d.Formats = d.Formats[:0]
			for _, f := range campos[3:] {
				if n, err := strconv.Atoi(f); err == nil {
					d.Formats = append(d.Formats, uint8(n))
				}
			}
		case strings.HasPrefix(line, "m="):
			// Otra sección de media (vídeo, etc.): salimos de la de audio.
			inAudio = false
		case strings.HasPrefix(line, "c=IN IP4 "):
			ip := strings.TrimSpace(strings.TrimPrefix(line, "c=IN IP4 "))
			// El c= de nivel de sesión vale si no hay otro; el de la media manda.
			if d.ConnIP == "" || inAudio {
				d.ConnIP = ip
			}
		case inAudio && strings.HasPrefix(line, "a=ptime:"):
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "a=ptime:"))); err == nil {
				d.PTime = n
			}
		}
	}
	if err := sc.Err(); err != nil {
		return d, err
	}
	if !foundAudio {
		return d, fmt.Errorf("el SDP no contiene una sección de audio (m=audio)")
	}
	if d.ConnIP == "" {
		return d, fmt.Errorf("el SDP no contiene línea de conexión (c=IN IP4 ...)")
	}
	return d, nil
}

// ChooseCodec elige el códec a usar entre los ofertados por el otro extremo:
// preferimos PCMU y, si no está, PCMA. ok=false si no hay ninguno común.
func ChooseCodec(d Description) (pt uint8, ok bool) {
	if d.HasPayload(PayloadPCMU) {
		return PayloadPCMU, true
	}
	if d.HasPayload(PayloadPCMA) {
		return PayloadPCMA, true
	}
	return 0, false
}
