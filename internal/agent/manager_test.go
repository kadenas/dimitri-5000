// Tests de la mecánica del Manager: alta/baja, ids duplicados, orden estable y
// validación de la Spec. No arrancamos agentes (no tocamos la red), así que es
// rápido y determinista.
package agent

import (
	"io"
	"log/slog"
	"testing"
)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func spec(id string, port int) Spec {
	return Spec{ID: id, Name: id, BindIP: "127.0.0.1", SIPPort: port, Transport: "udp"}
}

func TestManagerAddRemove(t *testing.T) {
	m := NewManager(silentLogger())

	if _, err := m.Add(spec("a", 5070)); err != nil {
		t.Fatalf("Add(a): %v", err)
	}
	if _, err := m.Add(spec("b", 5072)); err != nil {
		t.Fatalf("Add(b): %v", err)
	}

	// Id duplicado debe fallar.
	if _, err := m.Add(spec("a", 5099)); err == nil {
		t.Fatal("Add con id duplicado debería fallar")
	}

	// Orden de alta estable.
	snap := m.Snapshot()
	if len(snap) != 2 || snap[0].ID != "a" || snap[1].ID != "b" {
		t.Fatalf("orden inesperado: %+v", snap)
	}

	// Transport se normaliza a minúsculas/por defecto.
	if snap[0].Transport != "udp" {
		t.Fatalf("transporte no normalizado: %q", snap[0].Transport)
	}

	// Baja.
	if !m.Remove("a") {
		t.Fatal("Remove(a) debería devolver true")
	}
	if m.Remove("noexiste") {
		t.Fatal("Remove de id inexistente debería devolver false")
	}
	snap = m.Snapshot()
	if len(snap) != 1 || snap[0].ID != "b" {
		t.Fatalf("estado inesperado tras borrar: %+v", snap)
	}
}

func TestManagerValidacion(t *testing.T) {
	m := NewManager(silentLogger())

	// Sin id.
	if _, err := m.Add(Spec{BindIP: "127.0.0.1", SIPPort: 5060}); err == nil {
		t.Fatal("Spec sin id debería fallar")
	}
	// Sin bind ip.
	if _, err := m.Add(Spec{ID: "x", SIPPort: 5060}); err == nil {
		t.Fatal("Spec sin bind ip debería fallar")
	}
	// Puerto inválido.
	if _, err := m.Add(Spec{ID: "y", BindIP: "127.0.0.1", SIPPort: 0}); err == nil {
		t.Fatal("Spec con puerto inválido debería fallar")
	}
}

func TestManagerStartSinBind(t *testing.T) {
	m := NewManager(silentLogger())
	if _, err := m.Add(spec("a", 5070)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Sin Bind(ctx) previo, Start debe quejarse en vez de hacer algo raro.
	if err := m.Start("a"); err == nil {
		t.Fatal("Start sin Bind debería fallar")
	}
}
