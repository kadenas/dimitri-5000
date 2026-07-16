package load

import (
	"testing"

	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// TestScenarioTarget verifica que el Request-URI hacia el SBC lleva el número B
// como user cuando el operador lo rellenó (es el campo por el que enruta el SBC),
// y que sin número queda la forma histórica sip:host:puerto.
func TestScenarioTarget(t *testing.T) {
	casos := []struct {
		nombre string
		inv    sipcore.RichInvite
		want   string
	}{
		{"sin número B", sipcore.RichInvite{DestHost: "10.0.0.5", DestPort: 5060},
			"sip:10.0.0.5:5060"},
		{"con número B", sipcore.RichInvite{DestHost: "10.0.0.5", DestPort: 5060, ToUser: "910200200"},
			"sip:910200200@10.0.0.5:5060"},
		{"con número B y tcp", sipcore.RichInvite{DestHost: "10.0.0.5", DestPort: 5080, ToUser: "910200200", Transport: "tcp"},
			"sip:910200200@10.0.0.5:5080;transport=tcp"},
	}
	for _, c := range casos {
		if got := scenarioTarget(c.inv); got != c.want {
			t.Errorf("%s: scenarioTarget=%q, esperado %q", c.nombre, got, c.want)
		}
	}
}

// TestIdentityVars verifica el mapeo números A/B -> variables {caller}/{callee}
// que la carga impone sobre el escenario: solo se pisan los rellenados; sin
// ninguno, nil (el YAML manda).
func TestIdentityVars(t *testing.T) {
	if v := identityVars(sipcore.RichInvite{}); v != nil {
		t.Errorf("sin números: esperado nil, obtenido %v", v)
	}
	v := identityVars(sipcore.RichInvite{FromUser: "600100100"})
	if len(v) != 1 || v["caller"] != "600100100" {
		t.Errorf("solo A: esperado {caller:600100100}, obtenido %v", v)
	}
	v = identityVars(sipcore.RichInvite{FromUser: "600100100", ToUser: "910200200"})
	if v["caller"] != "600100100" || v["callee"] != "910200200" {
		t.Errorf("A y B: esperado caller/callee del panel, obtenido %v", v)
	}
}
