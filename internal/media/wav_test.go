package media

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// buildWAV16 arma un WAV PCM de 16 bits con las muestras intercaladas dadas.
func buildWAV16(numCh, rate int, samples []int16) []byte {
	const bits = 16
	blockAlign := numCh * bits / 8
	byteRate := rate * blockAlign
	dataLen := len(samples) * 2

	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+dataLen))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1)) // PCM
	binary.Write(&b, binary.LittleEndian, uint16(numCh))
	binary.Write(&b, binary.LittleEndian, uint32(rate))
	binary.Write(&b, binary.LittleEndian, uint32(byteRate))
	binary.Write(&b, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(dataLen))
	for _, s := range samples {
		binary.Write(&b, binary.LittleEndian, s)
	}
	return b.Bytes()
}

func TestDecodeWAVMono8k(t *testing.T) {
	in := []int16{0, 100, -100, 32767, -32768, 7}
	got, err := DecodeWAV(buildWAV16(1, 8000, in))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("nº de muestras = %d, se esperaba %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Fatalf("muestra %d = %d, se esperaba %d", i, got[i], in[i])
		}
	}
}

func TestDecodeWAVStereoDownmix(t *testing.T) {
	// Dos frames estéreo: (100,200) y (-100,-200) → mono 150 y -150.
	in := []int16{100, 200, -100, -200}
	got, err := DecodeWAV(buildWAV16(2, 8000, in))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	want := []int16{150, -150}
	if len(got) != len(want) {
		t.Fatalf("nº de muestras mono = %d, se esperaba %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("muestra mono %d = %d, se esperaba %d", i, got[i], want[i])
		}
	}
}

func TestDecodeWAVResample16kTo8k(t *testing.T) {
	in := make([]int16, 100) // 100 muestras a 16 kHz
	for i := range in {
		in[i] = int16(i * 100)
	}
	got, err := DecodeWAV(buildWAV16(1, 16000, in))
	if err != nil {
		t.Fatalf("DecodeWAV: %v", err)
	}
	// A la mitad de frecuencia, ~la mitad de muestras.
	if got2 := len(got); got2 < 45 || got2 > 55 {
		t.Fatalf("resampleo 16k->8k dio %d muestras, se esperaban ~50", got2)
	}
}

func TestDecodeWAVRejectsGarbage(t *testing.T) {
	if _, err := DecodeWAV([]byte("esto no es un wav")); err == nil {
		t.Fatal("se esperaba error con datos que no son WAV")
	}
	if _, err := DecodeWAV([]byte{0x01, 0x02}); err == nil {
		t.Fatal("se esperaba error con un fichero demasiado corto")
	}
}
