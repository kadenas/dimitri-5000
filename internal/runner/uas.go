// Runner del lado UAS: a diferencia del UAC (imperativo: lanza una llamada y
// termina), un escenario role uas es REACTIVO. No se "ejecuta": se ARMA sobre el
// Core de un agente y dirige la respuesta a CADA llamada entrante.
//
// La pieza de este fichero es la TRADUCCIÓN del escenario YAML a un guion neutro
// (sipcore.UASStep), que es lo único que sipcore sabe ejecutar. Así el "lenguaje"
// de escenarios se queda aquí y sipcore no depende de él.
package runner

import (
	"fmt"
	"strconv"

	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// BuildUASPolicy traduce un escenario role uas a una UASPolicy con su guion de
// respuesta (pausas + provisionales + final, opcionalmente esperar/enviar BYE).
// Devuelve error si el escenario no es uas, no empieza recibiendo el INVITE o no
// llega a enviar una respuesta final.
//
// Mapa de pasos:
//   - recv INVITE  -> disparador (el INVITE entrante); debe ser el primer recv.
//   - pause D      -> UASPause D
//   - send 1xx     -> UASSendProvisional (180, 183...)
//   - send 2xx-6xx -> UASSendFinal (2xx contesta con media si está activa; otros rechazan)
//   - recv ACK     -> implícito tras el 2xx; se ignora
//   - recv BYE     -> UASWaitBye (esperar a que el otro cuelgue); detiene el guion
//   - send BYE     -> UASSendBye (colgamos nosotros)
func BuildUASPolicy(sc *scenario.Scenario) (sipcore.UASPolicy, error) {
	if sc.Role != scenario.RoleUAS {
		return sipcore.UASPolicy{}, fmt.Errorf("BuildUASPolicy: el escenario debe ser role uas (encontrado %q)", sc.Role)
	}

	var script []sipcore.UASStep
	sawInvite := false // ya recibimos el INVITE disparador
	sawFinal := false  // ya enviamos una respuesta final (2xx-6xx)

	for i, st := range sc.Steps {
		paso := i + 1
		switch st.Kind() {

		case scenario.KindRecv:
			switch {
			case st.Recv == "INVITE":
				if sawInvite {
					return sipcore.UASPolicy{}, fmt.Errorf("paso %d: un escenario uas recibe un único INVITE inicial", paso)
				}
				sawInvite = true
			case st.Recv == "ACK":
				// El ACK del 2xx ya lo absorbe sipgo al contestar; no genera paso.
			case st.Recv == "BYE":
				script = append(script, sipcore.UASStep{Kind: sipcore.UASWaitBye})
				// Tras esperar el BYE el diálogo termina: lo que venga después
				// (p. ej. un 'send 200' de cortesía) lo responde la infraestructura.
				return finalize(script, sawInvite, sawFinal)
			case isStatusCode(st.Recv):
				return sipcore.UASPolicy{}, fmt.Errorf("paso %d: un uas no 'recibe' códigos de respuesta (%s)", paso, st.Recv)
			default:
				return sipcore.UASPolicy{}, fmt.Errorf("paso %d: 'recv %s' no soportado en un escenario uas", paso, st.Recv)
			}

		case scenario.KindSend:
			if !sawInvite {
				return sipcore.UASPolicy{}, fmt.Errorf("paso %d: el uas debe recibir el INVITE antes de responder", paso)
			}
			if st.Send == "BYE" {
				script = append(script, sipcore.UASStep{Kind: sipcore.UASSendBye})
				return finalize(script, sawInvite, sawFinal)
			}
			if !isStatusCode(st.Send) {
				return sipcore.UASPolicy{}, fmt.Errorf("paso %d: un uas envía códigos de respuesta o BYE, no '%s'", paso, st.Send)
			}
			code, _ := strconv.Atoi(st.Send)
			if code >= 100 && code < 200 {
				script = append(script, sipcore.UASStep{Kind: sipcore.UASSendProvisional, Code: code})
			} else {
				script = append(script, sipcore.UASStep{Kind: sipcore.UASSendFinal, Code: code})
				sawFinal = true
				if code < 200 || code >= 300 {
					// Respuesta no-2xx: la llamada no se establece; el guion acaba aquí.
					return finalize(script, sawInvite, sawFinal)
				}
			}

		case scenario.KindPause:
			script = append(script, sipcore.UASStep{Kind: sipcore.UASPause, Dur: st.Pause.Std()})

		case scenario.KindNop:
			// save/log no afectan a la respuesta; los ignoramos en el guion UAS.
		}
	}

	return finalize(script, sawInvite, sawFinal)
}

// finalize valida que el guion es coherente (recibió el INVITE y envía una final)
// y lo devuelve como UASPolicy.
func finalize(script []sipcore.UASStep, sawInvite, sawFinal bool) (sipcore.UASPolicy, error) {
	if !sawInvite {
		return sipcore.UASPolicy{}, fmt.Errorf("el escenario uas no recibe ningún INVITE (falta 'recv: INVITE')")
	}
	if !sawFinal {
		return sipcore.UASPolicy{}, fmt.Errorf("el escenario uas no envía ninguna respuesta final (2xx-6xx)")
	}
	return sipcore.UASPolicy{Script: script}, nil
}
