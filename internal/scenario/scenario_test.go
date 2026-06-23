package scenario

import (
	"testing"
	"time"
)

// escenarioUACValido es un escenario UAC correcto que ejercita la mayoría de campos.
const escenarioUACValido = `
name: uac-llamada-basica
role: uac
description: "Llamada básica con BYE"
defaults:
  transport: udp
  recv_timeout: 5s
variables:
  caller: "1000"
  callee: "2000"
steps:
  - send: INVITE
    headers:
      From: "sip:{caller}@{domain}"
      To:   "sip:{callee}@{domain}"
    body: sdp_oferta
  - recv: "100"
    optional: true
  - recv: "180"
    optional: true
  - recv: "200"
    timeout: 8s
    save:
      remote_tag: "{header:To;tag}"
  - send: ACK
  - pause: 3s
  - send: BYE
  - recv: "200"
bodies:
  sdp_oferta:
    type: sdp
    media: g711
`

func TestParseEscenarioValido(t *testing.T) {
	sc, err := Parse([]byte(escenarioUACValido))
	if err != nil {
		t.Fatalf("Parse devolvió error en escenario válido: %v", err)
	}
	if sc.Role != RoleUAC {
		t.Errorf("role esperado uac, obtenido %q", sc.Role)
	}
	if len(sc.Steps) != 8 {
		t.Fatalf("esperaba 8 pasos, obtenidos %d", len(sc.Steps))
	}
	if got := sc.Defaults.RecvTimeout.Std(); got != 5*time.Second {
		t.Errorf("recv_timeout esperado 5s, obtenido %v", got)
	}
	// El primer paso debe ser un send de INVITE con body referenciado.
	if k := sc.Steps[0].Kind(); k != KindSend {
		t.Errorf("paso 1: tipo esperado send, obtenido %s", k)
	}
	// El paso 'pause: 3s'.
	if sc.Steps[5].Kind() != KindPause || sc.Steps[5].Pause.Std() != 3*time.Second {
		t.Errorf("paso 6 debería ser pause de 3s")
	}
}

func TestValidacionesFallan(t *testing.T) {
	casos := map[string]string{
		"role inválido": `
name: x
role: proxy
steps:
  - send: INVITE
`,
		"sin pasos": `
name: x
role: uac
steps: []
`,
		"body no definido": `
name: x
role: uac
steps:
  - send: INVITE
    body: no_existe
`,
		"send código en uac": `
name: x
role: uac
steps:
  - send: "200"
`,
		"campo desconocido": `
name: x
role: uac
stpes:
  - send: INVITE
`,
		"duración inválida": `
name: x
role: uac
steps:
  - pause: tres-segundos
`,
		"recv desconocido": `
name: x
role: uac
steps:
  - recv: HOLA
`,
	}

	for nombre, yamlTexto := range casos {
		t.Run(nombre, func(t *testing.T) {
			if _, err := Parse([]byte(yamlTexto)); err == nil {
				t.Errorf("esperaba error de validación para %q, pero Parse fue OK", nombre)
			}
		})
	}
}
