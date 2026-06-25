package media

import "testing"

// El silencio (muestra 0) tiene un código canónico en cada ley: 0xFF en µ-law y
// 0xD5 en A-law. Es la comprobación más sencilla de que la codificación es la real.
func TestG711Silence(t *testing.T) {
	if got := EncodeMuLaw(0); got != 0xFF {
		t.Errorf("EncodeMuLaw(0) = 0x%02X, se esperaba 0xFF", got)
	}
	if got := EncodeALaw(0); got != 0xD5 {
		t.Errorf("EncodeALaw(0) = 0x%02X, se esperaba 0xD5", got)
	}
	if got := DecodeMuLaw(0xFF); got != 0 {
		t.Errorf("DecodeMuLaw(0xFF) = %d, se esperaba 0", got)
	}
	// A-law no tiene cero exacto: el nivel mínimo es ±8.
	if got := DecodeALaw(0xD5); got != 8 {
		t.Errorf("DecodeALaw(0xD5) = %d, se esperaba 8", got)
	}
	if got := DecodeALaw(0x55); got != -8 {
		t.Errorf("DecodeALaw(0x55) = %d, se esperaba -8", got)
	}
}

// Un códec de companding correcto es MONÓTONO: si x crece, decode(encode(x)) no
// decrece. Una inversión de signo o un error de segmento rompe esta propiedad, así
// que es una prueba fuerte de corrección. De paso acotamos el error de round-trip.
func TestG711MonotonicAndBounded(t *testing.T) {
	check := func(name string, enc func(int16) byte, dec func(byte) int16) {
		prev := -100000
		for x := -32768; x <= 32767; x += 7 {
			y := int(dec(enc(int16(x))))
			if y < prev {
				t.Fatalf("%s no monótono en x=%d: y=%d < prev=%d", name, x, y, prev)
			}
			prev = y
			// G.711 es lossy; el error crece con la magnitud pero nunca debería
			// ser enorme. Cota generosa que aún detecta fallos gruesos.
			if d := y - x; d > 1100 || d < -1100 {
				t.Fatalf("%s error de round-trip excesivo en x=%d: y=%d (d=%d)", name, x, y, d)
			}
		}
	}
	check("µ-law", EncodeMuLaw, DecodeMuLaw)
	check("A-law", EncodeALaw, DecodeALaw)
}

// El signo debe conservarse en el round-trip salvo en el entorno del cero.
func TestG711SignPreserved(t *testing.T) {
	for x := -32000; x <= 32000; x += 137 {
		y := int(DecodeMuLaw(EncodeMuLaw(int16(x))))
		if x > 500 && y < 0 {
			t.Fatalf("µ-law: x=%d positivo dio y=%d negativo", x, y)
		}
		if x < -500 && y > 0 {
			t.Fatalf("µ-law: x=%d negativo dio y=%d positivo", x, y)
		}
	}
}
