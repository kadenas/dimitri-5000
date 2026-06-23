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
