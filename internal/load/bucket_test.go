package load

import (
	"testing"
	"time"
)

// drena consume fichas hasta agotarlas y devuelve cuántas había.
func drena(b *bucket) int {
	n := 0
	for b.toma() {
		n++
	}
	return n
}

// TestBucketRitmo verifica que las fichas se devengan por tiempo real al ritmo
// de la cps: 100 ms a 50 cps son 5 llamadas, ni una más.
func TestBucketRitmo(t *testing.T) {
	b := newBucket(50)
	b.suma(100 * time.Millisecond)
	if got := drena(b); got != 5 {
		t.Fatalf("a 50 cps en 100 ms deben devengarse 5 fichas, no %d", got)
	}
	// Vacío: sin más tiempo no hay más fichas.
	if b.toma() {
		t.Fatal("el cubo vacío no debe conceder fichas")
	}
}

// TestBucketCPSAlta verifica el motivo del cambio: con cps > 1000 una sola
// vuelta de 20 ms devenga varias fichas (el ticker antiguo tenía techo en
// ~1000 cps porque no bajaba de 1 ms por llamada).
func TestBucketCPSAlta(t *testing.T) {
	b := newBucket(5000)
	b.suma(20 * time.Millisecond)
	if got := drena(b); got != 100 {
		t.Fatalf("a 5000 cps en 20 ms deben devengarse 100 fichas, no %d", got)
	}
}

// TestBucketNoPierdeRetrasos verifica el segundo defecto del ticker: si el loop
// se retrasa, el tiempo transcurrido devenga las fichas igual (5x20 ms sueltos
// equivalen a un único parón de 100 ms).
func TestBucketNoPierdeRetrasos(t *testing.T) {
	suelto := newBucket(10)
	for i := 0; i < 5; i++ {
		suelto.suma(20 * time.Millisecond)
	}
	deGolpe := newBucket(10)
	deGolpe.suma(100 * time.Millisecond)
	if a, b := drena(suelto), drena(deGolpe); a != b || a != 1 {
		t.Fatalf("mismo tiempo debe devengar lo mismo: sueltos=%d deGolpe=%d (esperado 1)", a, b)
	}
}

// TestBucketTopeRafaga verifica el techo de acumulación: con el objetivo N
// cubierto mucho rato, al liberarse hueco no sale un golpe de llamadas — como
// mucho 100 ms de cps (y nunca menos de 1 ficha, para que una cps muy baja
// pueda lanzar).
func TestBucketTopeRafaga(t *testing.T) {
	b := newBucket(50)
	b.suma(10 * time.Second) // N cubierto durante 10 s: 500 fichas "devengadas"
	if got := drena(b); got != 5 {
		t.Fatalf("el tope de ráfaga es 100 ms de cps (5 fichas a 50 cps), no %d", got)
	}
	lenta := newBucket(2) // 100 ms de cps sería 0.2: el mínimo es 1
	lenta.suma(10 * time.Second)
	if got := drena(lenta); got != 1 {
		t.Fatalf("el tope mínimo es 1 ficha, no %d", got)
	}
}
