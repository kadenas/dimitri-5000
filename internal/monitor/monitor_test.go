// Tests del faro dinámico: alta/baja de troncales y reconciliación (Sync).
//
// No arrancamos el faro (no llamamos a Start), así que no se envía ningún OPTIONS
// ni se toca la red: probamos solo la mecánica de registro y el Snapshot. Por eso
// el Core puede ser nil sin problema.
package monitor

import (
	"io"
	"log/slog"
	"testing"

	"github.com/kadenas/dimitri-5000/internal/config"
)

// silentLogger devuelve un logger que descarta todo (para no ensuciar el test).
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func target(id, host string) config.Target {
	return config.Target{ID: id, Name: id, Host: host, Port: 5060, Transport: "UDP"}
}

// ids extrae los identificadores de un Snapshot en su orden de aparición.
func ids(states []TargetState) []string {
	out := make([]string, len(states))
	for i, s := range states {
		out[i] = s.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAddRemoveTarget(t *testing.T) {
	cfg := config.MonitorConfig{IntervalSeconds: 5, TimeoutSeconds: 2, FailThreshold: 3}
	m := New(nil, []config.Target{target("a", "10.0.0.1")}, cfg, silentLogger())

	// Alta de una troncal nueva.
	if err := m.AddTarget(target("b", "10.0.0.2")); err != nil {
		t.Fatalf("AddTarget(b) devolvió error: %v", err)
	}
	if got := ids(m.Snapshot()); !equal(got, []string{"a", "b"}) {
		t.Fatalf("orden inesperado tras añadir: %v", got)
	}

	// Id duplicado debe rechazarse.
	if err := m.AddTarget(target("a", "10.0.0.9")); err == nil {
		t.Fatal("AddTarget con id duplicado debería fallar")
	}

	// Troncal inválida (sin host) debe rechazarse.
	if err := m.AddTarget(config.Target{ID: "x", Port: 5060}); err == nil {
		t.Fatal("AddTarget con host vacío debería fallar")
	}

	// Baja de una existente y de una inexistente.
	if !m.RemoveTarget("a") {
		t.Fatal("RemoveTarget(a) debería devolver true")
	}
	if m.RemoveTarget("noexiste") {
		t.Fatal("RemoveTarget de id inexistente debería devolver false")
	}
	if got := ids(m.Snapshot()); !equal(got, []string{"b"}) {
		t.Fatalf("estado inesperado tras borrar: %v", got)
	}
}

func TestSyncReconcilia(t *testing.T) {
	cfg := config.MonitorConfig{IntervalSeconds: 5, TimeoutSeconds: 2, FailThreshold: 3}
	m := New(nil, []config.Target{target("a", "10.0.0.1"), target("b", "10.0.0.2")}, cfg, silentLogger())

	// Deseado: se va "a", se mantiene "b", entra "c". Una troncal inválida (sin
	// host) debe ignorarse sin romper el resto.
	m.Sync([]config.Target{
		target("b", "10.0.0.2"),
		target("c", "10.0.0.3"),
		{ID: "malo", Port: 5060},
	})

	got := ids(m.Snapshot())
	// El orden final no está garantizado entre b y c (Sync recorre mapas), así que
	// comprobamos pertenencia, no orden.
	if len(got) != 2 {
		t.Fatalf("esperaba 2 troncales tras Sync, hay %d: %v", len(got), got)
	}
	set := map[string]bool{}
	for _, id := range got {
		set[id] = true
	}
	if !set["b"] || !set["c"] || set["a"] || set["malo"] {
		t.Fatalf("conjunto inesperado tras Sync: %v", got)
	}
}
