// Paquete agent: introduce el concepto de "agente" para que UNA sola aplicación
// (un proceso) pueda gestionar VARIAS instancias SIP independientes a la vez.
//
// Un Agent es una unidad SIP completa: un Core (su ip:puerto y transporte) con su
// rol/política de respuesta (UAC/UAS) y su control de llamadas. El Manager (ver
// manager.go) posee y orquesta los agentes: los crea, arranca, para y elimina en
// caliente, igual que el faro hace con las troncales.
//
// Diseño clave: un Agent puede CREAR su propio Core a partir de su Spec, o ADOPTAR
// uno ya creado. La adopción es lo que permite migrar sin romper nada: el agente
// "por defecto" adopta el Core que la capa de arranque ya tenía montado, evitando
// abrir dos veces el mismo puerto.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/kadenas/dimitri-5000/internal/control"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// Estados del ciclo de vida de un agente.
const (
	StateStopped = "stopped" // creado pero sin escuchar
	StateRunning = "running" // sirviendo (escucha y puede lanzar llamadas)
)

// Spec describe la configuración deseada de un agente. Es lo que la web enviará
// para dar de alta uno nuevo.
type Spec struct {
	ID         string // identificador único y corto (ej: "uac-pruebas")
	Name       string // nombre legible para la interfaz
	BindIP     string // IP local de señalización (Via/Contact y origen)
	SIPPort    int    // puerto SIP local (escucha UAS y origen UAC)
	Transport  string // "udp" | "tcp" (de momento udp)
	FromDomain string // dominio del From saliente (vacío = BindIP)
	UserAgent  string // valor de la cabecera User-Agent

	// Política de respuesta a INVITE entrantes (el "rol" como configuración).
	RingDelay  time.Duration // espera antes de la respuesta final (180 Ringing)
	AnswerCode int           // 200 = contestar; 486 = ocupado; 603 = rechazar
	HoldTime   time.Duration // tras contestar, cuánto sostener antes de colgar (0 = esperar BYE remoto)
}

// policy traduce la Spec a la política UAS que entiende el Core.
func (s Spec) policy() sipcore.UASPolicy {
	return sipcore.UASPolicy{
		RingDelay:  s.RingDelay,
		AnswerCode: s.AnswerCode,
		HoldTime:   s.HoldTime,
	}
}

// Agent es una instancia SIP gestionada por el Manager.
type Agent struct {
	spec Spec
	log  *slog.Logger

	mu       sync.Mutex
	state    string
	core     *sipcore.Core       // motor SIP; nil mientras está parado y no adoptado
	ownsCore bool                // true si el agente creó el Core (debe cerrarlo al parar)
	ctrl     *control.Controller // control de llamadas; existe mientras está running
	cancel   context.CancelFunc  // detiene el Serve del agente
}

// newAgent crea el objeto agente en estado "stopped" (sin tocar la red). Si core
// no es nil, el agente lo ADOPTA (no lo cerrará al parar: lo gestiona quien lo creó).
func newAgent(spec Spec, core *sipcore.Core, log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	return &Agent{
		spec:     spec,
		log:      log,
		state:    StateStopped,
		core:     core,
		ownsCore: false, // por defecto adopta; si lo crea en Start, se marca a true
	}
}

// Start arranca el agente: crea el Core si hace falta, fija la política UAS, monta
// el control de llamadas y lanza el servidor SIP (Serve) en segundo plano. Es
// idempotente: si ya está corriendo, no hace nada.
func (a *Agent) Start(parent context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state == StateRunning {
		return nil
	}

	// Si no adoptamos un Core, lo creamos a partir de la Spec (y lo cerraremos al parar).
	if a.core == nil {
		core, err := sipcore.New(a.spec.BindIP, a.spec.SIPPort, a.spec.UserAgent, a.spec.FromDomain, a.log)
		if err != nil {
			return fmt.Errorf("agente %q: creando núcleo SIP: %w", a.spec.ID, err)
		}
		a.core = core
		a.ownsCore = true
	}

	// Contexto propio del agente (hijo del de la app) para poder pararlo solo.
	ctx, cancel := context.WithCancel(parent)
	a.cancel = cancel

	a.core.SetUASPolicy(a.spec.policy())
	a.ctrl = control.New(ctx, a.core, a.log)

	addr := net.JoinHostPort(a.spec.BindIP, strconv.Itoa(a.spec.SIPPort))
	go func() {
		// Serve bloquea hasta que se cancele el contexto; el error solo importa si
		// no fue una parada ordenada.
		if err := a.core.Serve(ctx, a.spec.Transport, addr); err != nil && ctx.Err() == nil {
			a.log.Error("agente: servidor SIP terminó con error", "id", a.spec.ID, "error", err)
		}
	}()

	a.state = StateRunning
	a.log.Info("agente arrancado", "id", a.spec.ID, "addr", addr, "transport", a.spec.Transport)
	return nil
}

// Stop para el agente: cancela su Serve y, si creó el Core, lo cierra. Si adoptó
// un Core ajeno, NO lo cierra (lo gestiona su dueño). Es idempotente.
func (a *Agent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != StateRunning {
		return
	}
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.ownsCore && a.core != nil {
		a.core.Close()
		a.core = nil
	}
	a.ctrl = nil
	a.state = StateStopped
	a.log.Info("agente parado", "id", a.spec.ID)
}

// Control devuelve el controlador de llamadas del agente (nil si está parado).
// Lo usa la web para lanzar/colgar llamadas en ESTE agente.
func (a *Agent) Control() *control.Controller {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctrl
}

// Core devuelve el núcleo SIP del agente (nil si está parado y no adoptado).
func (a *Agent) Core() *sipcore.Core {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.core
}

// Info es la foto del estado de un agente para la interfaz web.
type Info struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	BindIP    string `json:"bind_ip"`
	SIPPort   int    `json:"sip_port"`
	Transport string `json:"transport"`
	State     string `json:"state"`
}

// info construye la foto del agente bajo bloqueo.
func (a *Agent) info() Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Info{
		ID:        a.spec.ID,
		Name:      a.spec.Name,
		BindIP:    a.spec.BindIP,
		SIPPort:   a.spec.SIPPort,
		Transport: a.spec.Transport,
		State:     a.state,
	}
}
