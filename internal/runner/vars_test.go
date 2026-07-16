package runner

import (
	"testing"

	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// TestBuildVarsOverride verifica que las variables impuestas desde fuera (Runner.Vars,
// los números A/B del panel de carga) PISAN a las del escenario, y que los
// placeholders internos ({remote_host}) siguen resolviéndose después.
func TestBuildVarsOverride(t *testing.T) {
	core, err := sipcore.New("127.0.0.1", 35090, "vars-test", "", nil)
	if err != nil {
		t.Fatalf("creando core: %v", err)
	}
	defer core.Close()

	sc := &scenario.Scenario{
		Name: "vars",
		Role: scenario.RoleUAC,
		Variables: map[string]string{
			"caller": "1000",
			"callee": "2000",
			"domain": "{remote_host}",
		},
		Steps: []scenario.Step{{Send: "INVITE"}},
	}

	r := New(core, "sip:10.0.0.5:5060", nil)
	r.Vars = map[string]string{"caller": "600100100", "callee": "910200200"}

	vars := r.buildVars(sc)
	if vars["caller"] != "600100100" {
		t.Fatalf("caller=%q, esperado el número A del panel (600100100)", vars["caller"])
	}
	if vars["callee"] != "910200200" {
		t.Fatalf("callee=%q, esperado el número B del panel (910200200)", vars["callee"])
	}
	if vars["domain"] != "10.0.0.5" {
		t.Fatalf("domain=%q, esperado el host del destino (10.0.0.5)", vars["domain"])
	}
}
