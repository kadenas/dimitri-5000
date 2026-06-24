// Paquete control: el "controlador" que une la interfaz web con el motor SIP.
// Posee el Core, lanza llamadas (rol UAC) en segundo plano y mantiene el estado
// de cada una para que la web lo muestre en vivo. La web no conoce SIP: solo
// pide estado a este controlador y le ordena lanzar o colgar llamadas.
package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"sync"
	"time"

	"github.com/kadenas/dimitri-5000/internal/runner"
	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// Estados de una llamada gestionada por el controlador.
const (
	StateDialing     = "dialing"     // INVITE enviado, sin respuesta final
	StateRinging     = "ringing"     // recibido 18x
	StateEstablished = "established" // contestada (200 + ACK)
	StateEnded       = "ended"       // finalizada con BYE
	StateFailed      = "failed"      // error o rechazo
)

// Estados de una ejecución de escenario gestionada por el controlador.
const (
	ScenarioRunning = "running" // el runner está ejecutando los pasos
	ScenarioOK      = "ok"      // el escenario terminó sin error
	ScenarioFailed  = "failed"  // el escenario falló (un recv esperado no llegó, etc.)
)

// CallRec es la foto del estado de una llamada. Las etiquetas json definen cómo
// viaja a la interfaz web.
type CallRec struct {
	ID         string `json:"id"`
	To         string `json:"to"`
	State      string `json:"state"`
	LastCode   int    `json:"last_code"`
	LastReason string `json:"last_reason"`
	Error      string `json:"error,omitempty"`
	StartedAt  string `json:"started_at"`
	AnsweredAt string `json:"answered_at,omitempty"`
	EndedAt    string `json:"ended_at,omitempty"`

	// interno: para solicitar el colgado manual desde la web.
	hangup chan struct{}
	// interno: los valores SIP con los que se lanzó la llamada.
	invite sipcore.RichInvite
	// interno: la llamada UAC viva (para acciones in-dialog como el desvío).
	call *sipcore.UACCall
}

// MessageRec es el registro de un MESSAGE (enviado o recibido) para mostrarlo.
type MessageRec struct {
	ID        string `json:"id"`
	Dir       string `json:"dir"`        // "out" (enviado) | "in" (recibido)
	Peer      string `json:"peer"`       // a quién/de quién (To si out, From si in)
	Body      string `json:"body"`       // texto del mensaje
	Code      int    `json:"code"`       // código de respuesta (solo out)
	Reason    string `json:"reason"`     // razón de la respuesta (solo out)
	Error     string `json:"error,omitempty"`
	Timestamp string `json:"timestamp"`  // hora del evento
}

// ScenarioRec es la foto de una ejecución de escenario para mostrarla en la web.
type ScenarioRec struct {
	ID        string `json:"id"`
	Name      string `json:"name"`             // 'name' del escenario ejecutado
	File      string `json:"file"`             // fichero YAML de origen
	Target    string `json:"target"`           // destino contra el que se ejecutó
	State     string `json:"state"`            // running | ok | failed
	Error     string `json:"error,omitempty"`  // motivo si falló
	StartedAt string `json:"started_at"`       // hora de inicio
	EndedAt   string `json:"ended_at,omitempty"` // hora de fin (vacío mientras corre)
}

// Controller gestiona el motor y el registro de llamadas y mensajes.
type Controller struct {
	core *sipcore.Core
	log  *slog.Logger
	ctx  context.Context // ciclo de vida de la aplicación

	mu    sync.RWMutex
	calls map[string]*CallRec
	order []string // ids en orden de creación (las más recientes al final)

	msgs []MessageRec // mensajes SIP enviados y recibidos (orden de aparición)

	scenarios []ScenarioRec // ejecuciones de escenario (orden de aparición)
}

// New crea el controlador. ctx es el contexto de vida de la app (al cancelarse,
// las llamadas en curso se cuelgan ordenadamente).
func New(ctx context.Context, core *sipcore.Core, log *slog.Logger) *Controller {
	if log == nil {
		log = slog.Default()
	}
	return &Controller{
		core:  core,
		log:   log,
		ctx:   ctx,
		calls: make(map[string]*CallRec),
	}
}

// CallSpec describe la llamada a lanzar: los valores SIP concretos (identidades,
// destino, cabeceras), el tiempo de sostenimiento y un texto para mostrar.
type CallSpec struct {
	Invite  sipcore.RichInvite // valores SIP de la llamada
	Hold    int                // segundos a mantener establecida (0 = hasta colgar a mano)
	Display string             // texto a mostrar en la columna TARGET de la web
}

// PlaceCall lanza una llamada UAC según la especificación dada. Devuelve el id de
// la llamada para seguirla.
func (c *Controller) PlaceCall(spec CallSpec) string {
	id := genID()
	rec := &CallRec{
		ID:        id,
		To:        spec.Display,
		State:     StateDialing,
		StartedAt: now(),
		hangup:    make(chan struct{}),
		invite:    spec.Invite,
	}

	c.mu.Lock()
	c.calls[id] = rec
	c.order = append(c.order, id)
	c.mu.Unlock()

	go c.run(rec, spec.Hold)
	return id
}

// run ejecuta el ciclo de vida completo de una llamada saliente.
func (c *Controller) run(rec *CallRec, holdSeconds int) {
	c.log.Info("lanzando llamada", "id", rec.ID, "to", rec.To)

	// Contexto de la llamada, hijo del de la app.
	callCtx, cancel := context.WithCancel(c.ctx)
	defer cancel()

	call, err := c.core.DialInvite(callCtx, rec.invite)
	if err != nil {
		c.fail(rec, "no se pudo enviar el INVITE: "+err.Error())
		return
	}

	// Observamos las respuestas para reflejar 'ringing' en la web.
	err = call.WaitAnswerObserved(callCtx, func(code int, reason string) {
		c.update(rec, func(r *CallRec) {
			r.LastCode = code
			r.LastReason = reason
			if code >= 180 && code < 200 {
				r.State = StateRinging
			}
		})
	})
	if err != nil {
		c.fail(rec, "la llamada no fue contestada: "+err.Error())
		return
	}

	if err := call.Ack(callCtx); err != nil {
		c.fail(rec, "error en ACK: "+err.Error())
		return
	}
	c.update(rec, func(r *CallRec) {
		r.State = StateEstablished
		r.AnsweredAt = now()
		r.call = call // disponible para acciones in-dialog (desvío)
	})
	c.log.Info("llamada establecida", "id", rec.ID)

	// Mantenemos la llamada: hasta holdSeconds, hasta colgado manual o hasta parada.
	var holdCh <-chan time.Time
	if holdSeconds > 0 {
		t := time.NewTimer(time.Duration(holdSeconds) * time.Second)
		defer t.Stop()
		holdCh = t.C
	}
	select {
	case <-holdCh:
	case <-rec.hangup:
	case <-c.ctx.Done():
	}

	// Colgamos con BYE (contexto propio para que funcione aunque la app pare).
	byeCtx, byeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer byeCancel()
	if err := call.Hangup(byeCtx); err != nil {
		c.fail(rec, "error en BYE: "+err.Error())
		return
	}
	c.update(rec, func(r *CallRec) {
		r.State = StateEnded
		r.EndedAt = now()
	})
	c.log.Info("llamada finalizada", "id", rec.ID)
}

// SendMessage envía un MESSAGE SIP con los valores dados y registra el resultado.
// display es el texto a mostrar como destinatario (p. ej. "2000@sbc"). Devuelve el
// id del registro.
func (c *Controller) SendMessage(spec sipcore.MessageSpec, display string) string {
	id := genID()
	rec := MessageRec{
		ID: id, Dir: "out", Peer: display, Body: spec.Body, Timestamp: now(),
	}

	// Enviamos con un timeout acotado, en segundo plano para no bloquear la web.
	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()
		res, err := c.core.SendMessage(ctx, spec)
		c.mu.Lock()
		defer c.mu.Unlock()
		for i := range c.msgs {
			if c.msgs[i].ID == id {
				if err != nil {
					c.msgs[i].Error = err.Error()
				} else {
					c.msgs[i].Code = res.Code
					c.msgs[i].Reason = res.Reason
				}
				break
			}
		}
	}()

	c.mu.Lock()
	c.msgs = append(c.msgs, rec)
	c.mu.Unlock()
	return id
}

// RecordIncomingMessage guarda un MESSAGE entrante (lo invoca el motor SIP).
func (c *Controller) RecordIncomingMessage(ev sipcore.MessageEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, MessageRec{
		ID: genID(), Dir: "in", Peer: ev.From, Body: ev.Body, Timestamp: now(),
	})
}

// MessagesSnapshot devuelve una copia del registro de mensajes.
func (c *Controller) MessagesSnapshot() []MessageRec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]MessageRec, len(c.msgs))
	copy(out, c.msgs)
	return out
}

// RunScenario ejecuta un escenario en segundo plano sobre el Core de este control,
// contra el 'target' indicado, y registra el resultado para que la web lo siga.
// 'file' es solo el nombre del fichero de origen (para mostrarlo). Devuelve el id
// del registro. Mismo patrón asíncrono que PlaceCall/SendMessage: la web no se
// bloquea mientras el escenario corre.
func (c *Controller) RunScenario(sc *scenario.Scenario, file, target string) string {
	id := genID()
	rec := ScenarioRec{
		ID:        id,
		Name:      sc.Name,
		File:      file,
		Target:    target,
		State:     ScenarioRunning,
		StartedAt: now(),
	}

	c.mu.Lock()
	c.scenarios = append(c.scenarios, rec)
	c.mu.Unlock()

	go func() {
		// Timeout amplio: un escenario con pausas puede durar. Mismo límite que el
		// modo CLI (2 minutos), acotado para no dejar ejecuciones colgadas.
		runCtx, cancel := context.WithTimeout(c.ctx, 2*time.Minute)
		defer cancel()

		r := runner.New(c.core, target, c.log)
		err := r.Run(runCtx, sc)

		c.mu.Lock()
		defer c.mu.Unlock()
		for i := range c.scenarios {
			if c.scenarios[i].ID == id {
				c.scenarios[i].EndedAt = now()
				if err != nil {
					c.scenarios[i].State = ScenarioFailed
					c.scenarios[i].Error = err.Error()
				} else {
					c.scenarios[i].State = ScenarioOK
				}
				break
			}
		}
	}()

	return id
}

// ScenariosSnapshot devuelve una copia del registro de ejecuciones de escenario.
func (c *Controller) ScenariosSnapshot() []ScenarioRec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ScenarioRec, len(c.scenarios))
	copy(out, c.scenarios)
	return out
}

// Transfer desvía (REFER) una llamada establecida hacia 'referTo'. Devuelve false
// si no existe la llamada; el resultado del REFER se refleja en LastCode/LastReason.
func (c *Controller) Transfer(id, referTo string) bool {
	c.mu.Lock()
	rec, ok := c.calls[id]
	var call *sipcore.UACCall
	if ok {
		call = rec.call
	}
	c.mu.Unlock()
	if !ok || call == nil {
		return false
	}

	go func() {
		ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
		defer cancel()
		res, err := call.Refer(ctx, referTo)
		if err != nil {
			c.log.Warn("desvío (REFER) falló", "id", id, "error", err)
			c.update(rec, func(r *CallRec) { r.LastReason = "REFER: " + err.Error() })
			return
		}
		c.log.Info("desvío (REFER) enviado", "id", id, "to", referTo, "code", res.Code)
		c.update(rec, func(r *CallRec) {
			r.LastCode = res.Code
			r.LastReason = "REFER " + res.Reason
		})
	}()
	return true
}

// Hangup solicita colgar una llamada en curso. Devuelve true si existía.
func (c *Controller) Hangup(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.calls[id]
	if !ok {
		return false
	}
	// Cierre no bloqueante y único del canal de colgado.
	select {
	case <-rec.hangup:
		// ya cerrado
	default:
		close(rec.hangup)
	}
	return true
}

// Snapshot devuelve una copia del estado de todas las llamadas, en orden de
// creación, segura para entregar a la web.
func (c *Controller) Snapshot() []CallRec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]CallRec, 0, len(c.order))
	for _, id := range c.order {
		if rec := c.calls[id]; rec != nil {
			out = append(out, *rec) // copia por valor (sin el canal interno)
		}
	}
	return out
}

// --- helpers internos --------------------------------------------------------

// update aplica una modificación al registro bajo bloqueo de escritura.
func (c *Controller) update(rec *CallRec, fn func(*CallRec)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(rec)
}

// fail marca la llamada como fallida con un mensaje.
func (c *Controller) fail(rec *CallRec, msg string) {
	c.log.Warn("llamada fallida", "id", rec.ID, "motivo", msg)
	c.update(rec, func(r *CallRec) {
		r.State = StateFailed
		r.Error = msg
		r.EndedAt = now()
	})
}

// now devuelve la hora actual en formato RFC3339 (legible y ordenable).
func now() string { return time.Now().Format(time.RFC3339) }

// genID genera un identificador corto y único para una llamada.
func genID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().Format("150405.000")
	}
	return hex.EncodeToString(b[:])
}
