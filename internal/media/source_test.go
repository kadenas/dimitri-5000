package media

import "testing"

func TestPCMSourceLoops(t *testing.T) {
	src := NewPCMSource([]int16{1, 2, 3})
	dst := make([]int16, 7)
	src.NextFrame(dst)
	want := []int16{1, 2, 3, 1, 2, 3, 1}
	for i := range want {
		if dst[i] != want[i] {
			t.Fatalf("muestra %d = %d, se esperaba %d (debe repetir en bucle)", i, dst[i], want[i])
		}
	}
	// La siguiente trama continúa donde se quedó (pos=1 → 2,3,1...).
	src.NextFrame(dst[:3])
	if dst[0] != 2 || dst[1] != 3 || dst[2] != 1 {
		t.Fatalf("la continuación del bucle es incorrecta: %v", dst[:3])
	}
}

func TestPCMSourceEmpty(t *testing.T) {
	src := NewPCMSource(nil)
	dst := []int16{9, 9, 9}
	src.NextFrame(dst) // buffer vacío → silencio, sin pánico
	for i, v := range dst {
		if v != 0 {
			t.Fatalf("muestra %d = %d, se esperaba 0 (silencio)", i, v)
		}
	}
}

func TestToneSourceProducesSignal(t *testing.T) {
	src := NewToneSource(440, 6000)
	dst := make([]int16, 160) // una trama de 20 ms
	src.NextFrame(dst)
	nonZero := 0
	for _, v := range dst {
		if v > 6000 || v < -6000 {
			t.Fatalf("muestra fuera de amplitud: %d", v)
		}
		if v != 0 {
			nonZero++
		}
	}
	if nonZero < 100 {
		t.Fatalf("el tono debería tener señal en casi todas las muestras, no-cero=%d", nonZero)
	}
}
