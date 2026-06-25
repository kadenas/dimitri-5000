// Fuente de audio para el envío RTP. La sesión pide tramas sucesivas de muestras
// PCM (16 bits, 8 kHz mono) y las codifica a G.711. Así se elige QUÉ se manda: un
// tono sintético (por defecto) o el audio que suba el usuario (un WAV convertido).
package media

import "math"

// Source entrega tramas sucesivas de muestras PCM de 16 bits a 8 kHz mono.
type Source interface {
	// NextFrame rellena dst con las siguientes len(dst) muestras (en bucle si la
	// fuente es finita). Se invoca solo desde el bucle de envío de una sesión, así
	// que una implementación no necesita ser segura para varias goroutines.
	NextFrame(dst []int16)
}

// ToneSource genera un tono senoidal continuo. Es la fuente por defecto cuando no
// hay audio cargado: produce energía real (ejercita el códec y se oye al grabar).
type ToneSource struct {
	freq, amp float64
	n         uint64 // índice de muestra global, para que el tono sea continuo
}

// NewToneSource crea un tono de la frecuencia y amplitud dadas.
func NewToneSource(freq, amp float64) *ToneSource {
	return &ToneSource{freq: freq, amp: amp}
}

func (t *ToneSource) NextFrame(dst []int16) {
	for i := range dst {
		dst[i] = int16(t.amp * math.Sin(2*math.Pi*t.freq*float64(t.n)/sampleRate))
		t.n++
	}
}

// PCMSource reproduce EN BUCLE un buffer de muestras PCM (8 kHz mono), típicamente
// un WAV subido y convertido. El buffer es de solo lectura: cada llamada crea su
// propia PCMSource (con su posición) sobre el mismo buffer compartido, sin
// condiciones de carrera.
type PCMSource struct {
	buf []int16
	pos int
}

// NewPCMSource crea una fuente que reproduce buf en bucle.
func NewPCMSource(buf []int16) *PCMSource {
	return &PCMSource{buf: buf}
}

func (p *PCMSource) NextFrame(dst []int16) {
	if len(p.buf) == 0 {
		for i := range dst {
			dst[i] = 0 // silencio si el buffer está vacío
		}
		return
	}
	for i := range dst {
		dst[i] = p.buf[p.pos]
		p.pos++
		if p.pos >= len(p.buf) {
			p.pos = 0 // bucle
		}
	}
}
