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
}

// Controller gestiona el motor y el registro de llamadas.
type Controller struct {
	core *sipcore.Core
	log  *slog.Logger
	ctx  context.Context // ciclo de vida de la aplicación

	mu    sync.RWMutex
	calls map[string]*CallRec
	order []string // ids en orden de creación (las más recientes al final)
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

// PlaceCall lanza una llamada UAC al destino 'to'. Si holdSeconds > 0, la cuelga
// automáticamente tras ese tiempo; si es 0, se mantiene hasta que se pida colgar.
// Devuelve el id de la llamada para seguirla.
func (c *Controller) PlaceCall(to string, holdSeconds int) string {
	id := genID()
	rec := &CallRec{
		ID:        id,
		To:        to,
		State:     StateDialing,
		StartedAt: now(),
		hangup:    make(chan struct{}),
	}

	c.mu.Lock()
	c.calls[id] = rec
	c.order = append(c.order, id)
	c.mu.Unlock()

	go c.run(rec, holdSeconds)
	return id
}

// run ejecuta el ciclo de vida completo de una llamada saliente.
func (c *Controller) run(rec *CallRec, holdSeconds int) {
	c.log.Info("lanzando llamada", "id", rec.ID, "to", rec.To)

	// Contexto de la llamada, hijo del de la app.
	callCtx, cancel := context.WithCancel(c.ctx)
	defer cancel()

	call, err := c.core.DialURI(callCtx, rec.To, nil)
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
