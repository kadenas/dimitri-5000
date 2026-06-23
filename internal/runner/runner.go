// Paquete runner: ejecuta un escenario (paquete scenario) contra el núcleo SIP
// (paquete sipcore). Es el "intérprete" de la máquina de estados.
//
// Alcance de esta primera versión (Fase 2): ejecuta escenarios UAC con el flujo
// estándar de llamada (INVITE -> respuestas -> ACK -> pausa -> BYE). Se apoya en
// la capa de diálogo de sipgo a través de sipcore, validando los pasos 'recv'
// (códigos de respuesta) contra lo que realmente llega. El soporte de escenarios
// UAS y de peticiones arbitrarias se añadirá encima de esta base.
package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// Runner ejecuta escenarios contra un Core.
type Runner struct {
	core   *sipcore.Core
	target string // URI de destino, p. ej. "sip:127.0.0.1:5060"
	log    *slog.Logger
}

// New crea un runner para un destino dado.
func New(core *sipcore.Core, target string, log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{core: core, target: target, log: log}
}

// Run ejecuta el escenario según su rol. De momento solo UAC.
func (r *Runner) Run(ctx context.Context, sc *scenario.Scenario) error {
	switch sc.Role {
	case scenario.RoleUAC:
		return r.runUAC(ctx, sc)
	case scenario.RoleUAS:
		return fmt.Errorf("la ejecución de escenarios UAS aún no está soportada (siguiente paso de la Fase 2)")
	default:
		return fmt.Errorf("rol desconocido: %q", sc.Role)
	}
}

// runUAC ejecuta el flujo de llamada saliente dirigido por los pasos del escenario.
func (r *Runner) runUAC(ctx context.Context, sc *scenario.Scenario) error {
	vars := r.buildVars(sc)
	r.log.Info("ejecutando escenario", "name", sc.Name, "role", sc.Role, "target", r.target)

	var call *sipcore.UACCall
	observed := make(map[int]bool) // códigos de respuesta vistos en la fase INVITE

	i := 0
	for i < len(sc.Steps) {
		st := sc.Steps[i]
		switch st.Kind() {

		case scenario.KindSend:
			if isStatusCode(st.Send) {
				return fmt.Errorf("paso %d: un UAC no envía respuestas (%s)", i+1, st.Send)
			}
			switch st.Send {
			case "INVITE":
				headers := substMap(st.Headers, vars)
				// El From de un INVITE inicial debe llevar 'tag'. Si el escenario
				// define From sin tag, se lo añadimos (sipgo lo exige para el diálogo).
				if from, ok := headers["From"]; ok && !strings.Contains(from, "tag=") {
					headers["From"] = from + ";tag=" + genTag()
				}
				body := r.bodyFor(sc, st)
				if len(body) > 0 {
					if _, ok := headers["Content-Type"]; !ok {
						headers["Content-Type"] = "application/sdp"
					}
				}

				c, err := r.core.DialURIWithOptions(ctx, r.target, sipcore.CallOptions{
					Headers: headers,
					Body:    body,
				})
				if err != nil {
					return fmt.Errorf("paso %d (send INVITE): %w", i+1, err)
				}
				call = c

				// Observamos cada respuesta hasta el 2xx final.
				err = call.WaitAnswerObserved(ctx, func(code int, reason string) {
					observed[code] = true
					r.log.Info("recibido", "code", code, "reason", reason)
				})
				if err != nil {
					return fmt.Errorf("paso %d: la llamada no fue contestada: %w", i+1, err)
				}

				// Consumimos los pasos 'recv' de respuestas que siguen al INVITE,
				// validando que los no opcionales realmente llegaron.
				i++
				for i < len(sc.Steps) {
					next := sc.Steps[i]
					if next.Kind() != scenario.KindRecv || !isStatusCode(next.Recv) {
						break
					}
					code, _ := strconv.Atoi(next.Recv)
					if !observed[code] && !next.Optional {
						return fmt.Errorf("paso %d: se esperaba recibir %d y no llegó", i+1, code)
					}
					i++
				}
				continue

			case "ACK":
				if call == nil {
					return fmt.Errorf("paso %d: ACK sin llamada establecida", i+1)
				}
				if err := call.Ack(ctx); err != nil {
					return fmt.Errorf("paso %d (send ACK): %w", i+1, err)
				}
				i++
				continue

			case "BYE":
				if call == nil {
					return fmt.Errorf("paso %d: BYE sin llamada establecida", i+1)
				}
				// Hangup envía el BYE y espera su 200 internamente.
				if err := call.Hangup(ctx); err != nil {
					return fmt.Errorf("paso %d (send BYE): %w", i+1, err)
				}
				i++
				// Si el escenario declara el recv 200 del BYE, lo damos por consumido.
				if i < len(sc.Steps) && sc.Steps[i].Kind() == scenario.KindRecv && sc.Steps[i].Recv == "200" {
					i++
				}
				continue

			default:
				return fmt.Errorf("paso %d: 'send %s' aún no soportado por el runner inicial", i+1, st.Send)
			}

		case scenario.KindPause:
			select {
			case <-time.After(st.Pause.Std()):
			case <-ctx.Done():
				return ctx.Err()
			}
			i++

		case scenario.KindRecv:
			return fmt.Errorf("paso %d: 'recv %s' fuera de una secuencia soportada por el runner inicial", i+1, st.Recv)

		case scenario.KindNop:
			if st.Log != "" {
				r.log.Info("escenario", "log", subst(st.Log, vars))
			}
			i++
		}
	}

	r.log.Info("escenario completado", "name", sc.Name)
	return nil
}

// buildVars construye el mapa de variables: primero las internas (IP/puerto/host
// del destino) y luego las del escenario, resolviendo placeholders entre ellas.
func (r *Runner) buildVars(sc *scenario.Scenario) map[string]string {
	host, port := splitHostPort(r.target)

	vars := map[string]string{
		"local_ip":    r.core.LocalIP(),
		"local_port":  strconv.Itoa(r.core.LocalPort()),
		"remote_host": host,
		"remote_port": port,
	}
	// Variables del escenario, resolviendo placeholders internos (varias pasadas
	// para casos como domain: "{remote_host}").
	for k, v := range sc.Variables {
		vars[k] = v
	}
	for pasada := 0; pasada < 5; pasada++ {
		cambiado := false
		for k, v := range vars {
			nuevo := subst(v, vars)
			if nuevo != v {
				vars[k] = nuevo
				cambiado = true
			}
		}
		if !cambiado {
			break
		}
	}
	return vars
}

// bodyFor genera el cuerpo del mensaje para un paso 'send' que referencia un body.
func (r *Runner) bodyFor(sc *scenario.Scenario, st scenario.Step) []byte {
	if st.Body == "" {
		return nil
	}
	b, ok := sc.Bodies[st.Body]
	if !ok {
		return nil // la validación ya garantiza que existe; defensivo
	}
	if strings.TrimSpace(b.Content) != "" {
		return []byte(b.Content)
	}
	if b.Type == "sdp" {
		return []byte(r.sdpOferta(b.Media))
	}
	return nil
}

// sdpOferta genera un SDP mínimo. En la Fase 1/2 es solo señalización: anunciamos
// audio G.711 (PCMU) en un puerto fijo de ejemplo; el RTP real llega en la Fase 5.
func (r *Runner) sdpOferta(media string) string {
	ip := r.core.LocalIP()
	// Por ahora solo G.711 (PCMU/8000, payload 0).
	return strings.Join([]string{
		"v=0",
		"o=dimitri 0 0 IN IP4 " + ip,
		"s=dimitri-5000",
		"c=IN IP4 " + ip,
		"t=0 0",
		"m=audio 40000 RTP/AVP 0",
		"a=rtpmap:0 PCMU/8000",
		"",
	}, "\r\n")
}

// --- utilidades de texto -----------------------------------------------------

var rePlaceholder = regexp.MustCompile(`\{([^}]+)\}`)

// genTag genera un tag aleatorio para la cabecera From (identifica el diálogo
// por nuestro lado). 8 bytes en hexadecimal son más que suficientes.
func genTag() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fallback improbable: usar el reloj.
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	return hex.EncodeToString(b[:])
}

// subst sustituye los placeholders {var} de s por su valor. Si una variable no
// existe, se deja el placeholder tal cual (útil para detectar fallos y para los
// {header:...} que resuelve otra fase).
func subst(s string, vars map[string]string) string {
	return rePlaceholder.ReplaceAllStringFunc(s, func(m string) string {
		clave := m[1 : len(m)-1] // quita las llaves
		if v, ok := vars[clave]; ok {
			return v
		}
		return m
	})
}

// substMap aplica subst a todos los valores de un mapa de cabeceras.
func substMap(in map[string]string, vars map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = subst(v, vars)
	}
	return out
}

// isStatusCode replica la comprobación de scenario para uso interno del runner.
func isStatusCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s[0] >= '1' && s[0] <= '6'
}

// splitHostPort separa "sip:host:port" en host y puerto (texto). Tolerante: si no
// hay puerto, devuelve "5060".
func splitHostPort(uri string) (host, port string) {
	s := strings.TrimPrefix(uri, "sip:")
	s = strings.TrimPrefix(s, "sips:")
	// Quitamos posibles parámetros (;transport=...).
	if idx := strings.IndexByte(s, ';'); idx >= 0 {
		s = s[:idx]
	}
	// Quitamos user@ si lo hubiera.
	if idx := strings.IndexByte(s, '@'); idx >= 0 {
		s = s[idx+1:]
	}
	if idx := strings.LastIndexByte(s, ':'); idx >= 0 {
		return s[:idx], s[idx+1:]
	}
	return s, "5060"
}
