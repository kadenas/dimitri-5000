// G.711: companding logarítmico que comprime audio lineal de 16 bits a 8 bits por
// muestra (64 kbit/s a 8 kHz), en dos variantes: µ-law (PCMU, payload 0; habitual
// en Norteamérica/Japón) y A-law (PCMA, payload 8; habitual en Europa). Es lo que
// transporta la mayoría de troncales SIP, por eso es el primer (y de momento único)
// códec que implementamos.
//
// Implementación portada de la referencia de Sun Microsystems (dominio público),
// fiel a la ITU-T G.711, para no depender de librerías externas (evitamos CGO y
// bibliotecas con licencias problemáticas).
package media

// Constantes del algoritmo, idénticas a la referencia g711.c.
const (
	g711SignBit   = 0x80
	g711QuantMask = 0x0F
	g711SegShift  = 4
	g711SegMask   = 0x70
	muBias        = 0x84
	muClip        = 8159
)

// Tablas de fin de segmento: localizan el segmento (exponente) por búsqueda lineal.
var segUEnd = [8]int{0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF, 0x1FFF}
var segAEnd = [8]int{0x1F, 0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF}

// segSearch devuelve el índice del primer fin de segmento >= val (8 si se pasa).
func segSearch(val int, table *[8]int) int {
	for i := 0; i < 8; i++ {
		if val <= table[i] {
			return i
		}
	}
	return 8
}

// EncodeMuLaw comprime una muestra PCM lineal de 16 bits a un byte µ-law.
func EncodeMuLaw(pcm int16) byte {
	val := int(pcm) >> 2
	var mask int
	if val < 0 {
		val = -val
		mask = 0x7F
	} else {
		mask = 0xFF
	}
	if val > muClip {
		val = muClip
	}
	val += muBias >> 2
	seg := segSearch(val, &segUEnd)
	if seg >= 8 {
		return byte(0x7F ^ mask)
	}
	uval := (seg << 4) | ((val >> (seg + 1)) & 0x0F)
	return byte(uval ^ mask)
}

// DecodeMuLaw expande un byte µ-law a una muestra PCM lineal de 16 bits.
func DecodeMuLaw(u byte) int16 {
	uv := int(^u) & 0xFF
	t := ((uv & g711QuantMask) << 3) + muBias
	t <<= (uv & g711SegMask) >> g711SegShift
	if uv&g711SignBit != 0 {
		return int16(muBias - t)
	}
	return int16(t - muBias)
}

// EncodeALaw comprime una muestra PCM lineal de 16 bits a un byte A-law.
func EncodeALaw(pcm int16) byte {
	val := int(pcm) >> 3
	var mask int
	if val >= 0 {
		mask = 0xD5
	} else {
		mask = 0x55
		val = -val - 1
	}
	seg := segSearch(val, &segAEnd)
	if seg >= 8 {
		return byte(0x7F ^ mask)
	}
	aval := seg << 4
	if seg < 2 {
		aval |= (val >> 1) & 0x0F
	} else {
		aval |= (val >> seg) & 0x0F
	}
	return byte(aval ^ mask)
}

// DecodeALaw expande un byte A-law a una muestra PCM lineal de 16 bits.
func DecodeALaw(a byte) int16 {
	av := int(a) ^ 0x55
	t := (av & g711QuantMask) << 4
	seg := (av & g711SegMask) >> g711SegShift
	switch seg {
	case 0:
		t += 8
	case 1:
		t += 0x108
	default:
		t += 0x108
		t <<= seg - 1
	}
	if av&g711SignBit != 0 {
		return int16(t)
	}
	return int16(-t)
}

// encoderFor devuelve la función de codificación del payload type indicado.
func encoderFor(pt uint8) func(int16) byte {
	if pt == PayloadPCMA {
		return EncodeALaw
	}
	return EncodeMuLaw // PCMU por defecto
}
