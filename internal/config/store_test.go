package config

import (
	"path/filepath"
	"testing"
)

func TestStoreAddRemovePersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	// Arrancamos sin fichero: debe usar defaults y no fallar.
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	// Añadir un trunk válido.
	if err := store.AddTarget(Target{ID: "t1", Name: "Trunk 1", Host: "10.0.0.1", Port: 5060, Transport: "udp"}); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}
	// El transporte debe quedar normalizado a mayúsculas.
	ts := store.Targets()
	if len(ts) < 1 || ts[len(ts)-1].Transport != "UDP" {
		t.Fatalf("transporte no normalizado: %+v", ts)
	}

	// id duplicado debe fallar.
	if err := store.AddTarget(Target{ID: "t1", Host: "10.0.0.2", Port: 5060}); err == nil {
		t.Errorf("esperaba error por id duplicado")
	}
	// trunk inválido (sin host) debe fallar.
	if err := store.AddTarget(Target{ID: "bad", Port: 5060}); err == nil {
		t.Errorf("esperaba error por host vacío")
	}

	// Releemos desde disco: el cambio debe haber persistido.
	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load tras persistir: %v", err)
	}
	found := false
	for _, tr := range reloaded.Targets {
		if tr.ID == "t1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("el trunk t1 no persistió en disco")
	}

	// Borrar.
	ok, err := store.RemoveTarget("t1")
	if err != nil || !ok {
		t.Fatalf("RemoveTarget: ok=%v err=%v", ok, err)
	}
	if _, err := store.RemoveTarget("inexistente"); err != nil {
		t.Errorf("RemoveTarget inexistente no debe dar error: %v", err)
	}
}

// TestStoreCatalogoDestinos cubre lo que espera el modo web del catálogo: que un
// destino se busque por id y que sobreviva a un reinicio CON todos sus campos,
// incluido el dominio del To (que solo usan las llamadas, no el faro).
func TestStoreCatalogoDestinos(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	// Sin fichero previo el catálogo arranca vacío: no nos inventamos destinos.
	if n := len(store.Targets()); n != 0 {
		t.Fatalf("catálogo inicial con %d destinos, esperado vacío", n)
	}

	dest := Target{ID: "SBC1", Name: "SBC operador", Host: "10.10.10.10", Port: 5060,
		Transport: "tcp", ToDomain: "operador.com"}
	if err := store.AddTarget(dest); err != nil {
		t.Fatalf("AddTarget: %v", err)
	}

	// Búsqueda por id (la que usa la web para resolver un 'dest_id').
	got, ok := store.Target("SBC1")
	if !ok {
		t.Fatal("Target(SBC1) no encontrado")
	}
	if got.Host != "10.10.10.10" || got.Port != 5060 || got.ToDomain != "operador.com" || got.Transport != "TCP" {
		t.Errorf("destino recuperado inesperado: %+v", got)
	}
	if _, ok := store.Target("noexiste"); ok {
		t.Error("Target de un id inexistente debe devolver false")
	}

	// Reinicio: un Store nuevo sobre el mismo fichero conserva el destino entero.
	store2, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore tras reinicio: %v", err)
	}
	tras, ok := store2.Target("SBC1")
	if !ok {
		t.Fatal("el destino no sobrevivió al reinicio")
	}
	if tras != got {
		t.Errorf("el destino cambió al releer: %+v vs %+v", tras, got)
	}
}

// TestMemoryStoreNoEscribe: el modo degradado (config.json ilegible) debe permitir
// trabajar en memoria SIN tocar el disco, para no machacar el fichero del usuario.
func TestMemoryStoreNoEscribe(t *testing.T) {
	store := NewMemoryStore()
	if store.Path() != "" {
		t.Fatalf("un Store de memoria no debe tener ruta: %q", store.Path())
	}
	if err := store.AddTarget(Target{ID: "tmp", Host: "192.0.2.1", Port: 5060}); err != nil {
		t.Fatalf("AddTarget en memoria: %v", err)
	}
	if _, ok := store.Target("tmp"); !ok {
		t.Error("el destino debería estar disponible en memoria")
	}
}
