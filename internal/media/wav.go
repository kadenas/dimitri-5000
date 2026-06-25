// Decodificación de WAV (PCM) a muestras lineales de 16 bits a 8 kHz mono, listas
// para codificar a G.711 y enviar por RTP (Fase 5.2: "enviar audio propio"). Sin
// dependencias: parseamos los chunks RIFF a mano y, si hace falta, mezclamos a mono
// y resampleamos a 8 kHz por interpolación lineal.
//
// Empezamos por WAV (lo que produce ffmpeg/Audacity sin esfuerzo). El MP3 vendría
// después con una librería en Go puro (a aprobar antes de añadir la dependencia).
package media

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// SampleRate es la frecuencia de muestreo de G.711 (8 kHz). El audio se entrega
// siempre a esta tasa.
const SampleRate = sampleRate

// DecodeWAV convierte un fichero WAV (PCM) en muestras PCM de 16 bits a 8 kHz mono.
// Acepta 8/16 bits y cualquier nº de canales y frecuencia: mezcla a mono y
// resamplea a 8 kHz. Devuelve error si no es un WAV PCM válido.
func DecodeWAV(data []byte) ([]int16, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, errors.New("no es un fichero WAV (falta RIFF/WAVE)")
	}

	var (
		audioFmt   uint16
		numCh      uint16
		sampleRate uint32
		bits       uint16
		pcm        []byte
		haveFmt    bool
		haveData   bool
	)

	// Recorremos los chunks: id(4) + tamaño(4) + cuerpo, alineados a 2 bytes.
	for off := 12; off+8 <= len(data); {
		id := string(data[off : off+4])
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		if size < 0 || body+size > len(data) {
			size = len(data) - body // toleramos un tamaño declarado excesivo
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, errors.New("WAV: chunk 'fmt ' demasiado corto")
			}
			audioFmt = binary.LittleEndian.Uint16(data[body : body+2])
			numCh = binary.LittleEndian.Uint16(data[body+2 : body+4])
			sampleRate = binary.LittleEndian.Uint32(data[body+4 : body+8])
			bits = binary.LittleEndian.Uint16(data[body+14 : body+16])
			haveFmt = true
		case "data":
			pcm = data[body : body+size]
			haveData = true
		}
		off = body + size + (size & 1) // padding de alineación a 2 bytes
	}

	if !haveFmt || !haveData {
		return nil, errors.New("WAV: faltan los chunks 'fmt ' o 'data'")
	}
	if audioFmt != 1 {
		return nil, fmt.Errorf("WAV no es PCM (formato %d); conviértelo a PCM", audioFmt)
	}
	if numCh == 0 || sampleRate == 0 {
		return nil, errors.New("WAV: cabecera inválida (0 canales o 0 Hz)")
	}

	// Decodificamos a muestras int16 mezclando los canales a mono.
	mono, err := decodeSamplesMono(pcm, int(numCh), int(bits))
	if err != nil {
		return nil, err
	}

	// Resampleamos a 8 kHz si la fuente venía a otra frecuencia.
	if int(sampleRate) != SampleRate {
		mono = resampleLinear(mono, int(sampleRate), SampleRate)
	}
	return mono, nil
}

// decodeSamplesMono decodifica las muestras PCM intercaladas a int16 y las mezcla a
// un solo canal (media de los canales).
func decodeSamplesMono(pcm []byte, numCh, bits int) ([]int16, error) {
	switch bits {
	case 16:
		frames := (len(pcm) / 2) / numCh
		out := make([]int16, frames)
		for f := 0; f < frames; f++ {
			sum := 0
			for ch := 0; ch < numCh; ch++ {
				idx := (f*numCh + ch) * 2
				sum += int(int16(binary.LittleEndian.Uint16(pcm[idx : idx+2])))
			}
			out[f] = int16(sum / numCh)
		}
		return out, nil
	case 8:
		// WAV de 8 bits es PCM SIN signo, centrado en 128.
		frames := len(pcm) / numCh
		out := make([]int16, frames)
		for f := 0; f < frames; f++ {
			sum := 0
			for ch := 0; ch < numCh; ch++ {
				sum += (int(pcm[f*numCh+ch]) - 128) << 8
			}
			out[f] = int16(sum / numCh)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("WAV de %d bits no soportado (usa 16 u 8)", bits)
	}
}

// resampleLinear convierte de inRate a outRate por interpolación lineal. Es simple
// (sin filtro anti-alias), suficiente para una herramienta de prueba; para máxima
// calidad conviene subir el audio ya a 8 kHz mono.
func resampleLinear(in []int16, inRate, outRate int) []int16 {
	if inRate == outRate || len(in) == 0 {
		return in
	}
	outLen := int(int64(len(in)) * int64(outRate) / int64(inRate))
	if outLen <= 0 {
		return []int16{}
	}
	out := make([]int16, outLen)
	ratio := float64(inRate) / float64(outRate)
	for i := 0; i < outLen; i++ {
		pos := float64(i) * ratio
		i0 := int(pos)
		frac := pos - float64(i0)
		s0 := float64(in[i0])
		s1 := s0
		if i0+1 < len(in) {
			s1 = float64(in[i0+1])
		}
		out[i] = int16(s0 + (s1-s0)*frac)
	}
	return out
}
