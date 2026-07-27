package webui

import (
	"testing"

	"github.com/kadenas/dimitri-5000/internal/config"
)

// TestBuildCallSpecDestino comprueba que un destino del catálogo rellena los datos
// del INVITE: host, puerto, transporte y dominio del To. Es lo que permite elegir
// "SBC1" en un desplegable en vez de teclear la URI en cada panel.
func TestBuildCallSpecDestino(t *testing.T) {
	dest := &config.Target{
		ID: "SBC1", Name: "SBC operador", Host: "10.10.10.10", Port: 5060,
		Transport: "TCP", ToDomain: "operador.com",
	}

	spec, err := buildCallSpec(placeCallReq{DestID: "SBC1", ToUser: "910200200"}, dest)
	if err != nil {
		t.Fatalf("buildCallSpec: %v", err)
	}
	inv := spec.Invite
	if inv.DestHost != "10.10.10.10" || inv.DestPort != 5060 {
		t.Errorf("destino real = %s:%d, esperado 10.10.10.10:5060", inv.DestHost, inv.DestPort)
	}
	// El núcleo SIP espera el transporte en minúsculas; el catálogo lo guarda en mayúsculas.
	if inv.Transport != "tcp" {
		t.Errorf("transporte = %q, esperado %q", inv.Transport, "tcp")
	}
	if inv.ToDomain != "operador.com" {
		t.Errorf("dominio del To = %q, esperado %q", inv.ToDomain, "operador.com")
	}
	// El id del destino encabeza el texto de la tabla de llamadas.
	if spec.Display != "SBC1 · sip:910200200@10.10.10.10" {
		t.Errorf("display = %q", spec.Display)
	}
}

// TestBuildCallSpecManualManda verifica la regla de prioridad: lo que el usuario
// escribe a mano gana al destino del catálogo. Si no fuese así, no habría manera de
// desviarse de la ficha para una prueba puntual sin editar el catálogo.
func TestBuildCallSpecManualManda(t *testing.T) {
	dest := &config.Target{ID: "SBC1", Host: "10.10.10.10", Port: 5060, Transport: "UDP", ToDomain: "operador.com"}

	req := placeCallReq{
		DestID:   "SBC1",
		DestHost: "192.0.2.99", // otra IP para esta prueba concreta
		DestPort: 5070,
		ToDomain: "pruebas.local",
	}
	spec, err := buildCallSpec(req, dest)
	if err != nil {
		t.Fatalf("buildCallSpec: %v", err)
	}
	if spec.Invite.DestHost != "192.0.2.99" || spec.Invite.DestPort != 5070 {
		t.Errorf("destino = %s:%d, esperado el manual 192.0.2.99:5070", spec.Invite.DestHost, spec.Invite.DestPort)
	}
	if spec.Invite.ToDomain != "pruebas.local" {
		t.Errorf("dominio del To = %q, esperado el manual %q", spec.Invite.ToDomain, "pruebas.local")
	}
}

// TestBuildCallSpecSinDestino: sin catálogo ni URI no hay a dónde llamar, y debe
// decirlo en vez de fabricar un INVITE hacia la nada.
func TestBuildCallSpecSinDestino(t *testing.T) {
	if _, err := buildCallSpec(placeCallReq{}, nil); err == nil {
		t.Fatal("se esperaba error al no indicar destino")
	}
}

// TestBuildCallSpecURISimple protege el camino de siempre (escribir la URI a mano),
// que debe seguir funcionando igual sin catálogo.
func TestBuildCallSpecURISimple(t *testing.T) {
	spec, err := buildCallSpec(placeCallReq{To: "sip:2000@192.0.2.30:5070"}, nil)
	if err != nil {
		t.Fatalf("buildCallSpec: %v", err)
	}
	if spec.Invite.DestHost != "192.0.2.30" || spec.Invite.DestPort != 5070 {
		t.Errorf("destino = %s:%d", spec.Invite.DestHost, spec.Invite.DestPort)
	}
	if spec.Invite.ToUser != "2000" {
		t.Errorf("to_user = %q, esperado 2000 (el user del Request-URI)", spec.Invite.ToUser)
	}
	if spec.Display != "sip:2000@192.0.2.30:5070" {
		t.Errorf("display = %q, no debe llevar prefijo de catálogo", spec.Display)
	}
}

// TestScenarioTargetURI: el Request-URI que se le pasa al runner desde un destino
// del catálogo, con el transporte como parámetro de URI cuando es TCP.
func TestScenarioTargetURI(t *testing.T) {
	casos := []struct {
		nombre string
		dest   config.Target
		quiere string
	}{
		{"udp", config.Target{Host: "10.10.10.10", Port: 5060, Transport: "UDP"}, "sip:10.10.10.10:5060"},
		{"tcp", config.Target{Host: "10.10.10.10", Port: 5060, Transport: "TCP"}, "sip:10.10.10.10:5060;transport=tcp"},
	}
	for _, c := range casos {
		if got := scenarioTargetURI(c.dest); got != c.quiere {
			t.Errorf("%s: scenarioTargetURI = %q, esperado %q", c.nombre, got, c.quiere)
		}
	}
}
