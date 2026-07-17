package load

import "time"

// bucket es un "cubo de fichas" (token bucket) que marca la cadencia de
// lanzamiento de la carga. Sustituye al ticker por-llamada, que tenía dos
// defectos: con cps > 1000 el intervalo caía por debajo del milisegundo (el
// techo práctico de time.Ticker) y, si el loop se retrasaba, los ticks
// perdidos eran llamadas que nunca salían (el ritmo real quedaba por debajo
// del pedido). Aquí las fichas se devengan por TIEMPO REAL transcurrido: un
// retraso no pierde llamadas (se recuperan en la vuelta siguiente) y una cps
// alta solo significa varias fichas por vuelta.
type bucket struct {
	cps    float64
	tope   float64 // máximo acumulable (ráfaga): ~100 ms de cps, mínimo 1
	fichas float64
}

// newBucket crea el cubo para la cps pedida. El tope de ráfaga (100 ms de cps)
// evita que, con el objetivo N cubierto un buen rato, se acumule "deuda" y al
// liberarse hueco salga un golpe de llamadas: como mucho se adelanta una décima
// de segundo de ritmo.
func newBucket(cps float64) *bucket {
	tope := cps / 10
	if tope < 1 {
		tope = 1
	}
	return &bucket{cps: cps, tope: tope}
}

// suma acredita las fichas devengadas en el tiempo transcurrido d.
func (b *bucket) suma(d time.Duration) {
	b.fichas += d.Seconds() * b.cps
	if b.fichas > b.tope {
		b.fichas = b.tope
	}
}

// toma consume una ficha si la hay; cada ficha autoriza UNA llamada nueva.
func (b *bucket) toma() bool {
	if b.fichas < 1 {
		return false
	}
	b.fichas--
	return true
}
