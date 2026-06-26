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

	"github.com/kadenas/dimitri-5000/internal/config"
	"github.com/kadenas/dimitri-5000/internal/control"
	"github.com/kadenas/dimitri-5000/internal/monitor"
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

// Agent es una instancia SIP gestionada por el Manager. Cada agente, además de
// señalizar, monitoriza sus TRUNKS (endpoints remotos) con OPTIONS originados
// desde SU PROPIO core: así el OPTIONS sale del puerto configurado del agente.
type Agent struct {
	spec   Spec
	monCfg config.MonitorConfig
	log    *slog.Logger

	mu       sync.Mutex
	state    string
	core     *sipcore.Core       // motor SIP; nil mientras está parado y no adoptado
	ownsCore bool                // true si el agente creó el Core (debe cerrarlo al parar)
	ctrl     *control.Controller // control de llamadas; existe mientras está running
	cancel   context.CancelFunc  // detiene el Serve del agente

	trunks []config.Target  // trunks remotos asignados (persisten entre stop/start)
	mon    *monitor.Monitor // faro de ESTE agente; existe mientras está running

	audio []int16 // audio (PCM 8 kHz mono) a enviar por RTP; persiste entre stop/start

	// Escenario UAS asignado (rol "contestador scriptado"): si está, su guion dirige
	// la respuesta a las llamadas entrantes en vez del auto-answer fijo. Persiste
	// entre stop/start. La traducción YAML -> guion la hace la web (runner); aquí solo
	// guardamos el nombre (para mostrarlo) y la política ya construida.
	uasScenario string
	uasPol      *sipcore.UASPolicy
}

// newAgent crea el objeto agente en estado "stopped" (sin tocar la red). Si core
// no es nil, el agente lo ADOPTA (no lo cerrará al parar: lo gestiona quien lo creó).
func newAgent(spec Spec, monCfg config.MonitorConfig, core *sipcore.Core, log *slog.Logger) *Agent {
	if log == nil {
		log = slog.Default()
	}
	return &Agent{
		spec:     spec,
		monCfg:   monCfg,
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

	// Comprobación de bind SÍNCRONA antes de nada: si el puerto está en uso o la
	// IP no es local de esta máquina, fallamos aquí con un error claro (en vez de
	// dejar un agente "running" que en realidad no escucha). El Serve de sipgo
	// abre el socket en una goroutine, así que sin esto el error pasaría inadvertido.
	if err := checkBindAvailable(a.spec.Transport, a.spec.BindIP, a.spec.SIPPort); err != nil {
		return fmt.Errorf("agente %q: no se puede escuchar en %s:%d (%s): %w",
			a.spec.ID, a.spec.BindIP, a.spec.SIPPort, a.spec.Transport, err)
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

	// Activamos el plano de media (RTP): las llamadas entrantes se contestan con SDP
	// y audio G.711, y las salientes (vía control) ofertan media.
	a.core.EnableMedia()
	// Política UAS: el guion de un escenario asignado (si lo hay) tiene prioridad
	// sobre el auto-answer fijo de la Spec.
	if a.uasPol != nil {
		a.core.SetUASPolicy(*a.uasPol)
	} else {
		a.core.SetUASPolicy(a.spec.policy())
	}
	a.ctrl = control.New(ctx, a.core, a.log)
	// Los MESSAGE entrantes se registran en el control del agente (antes de Serve).
	a.core.SetMessageHandler(a.ctrl.RecordIncomingMessage)
	// Reaplicamos el audio cargado (persiste entre paradas) a este control y core.
	if a.audio != nil {
		a.ctrl.SetAudio(a.audio)
		a.core.SetMediaAudio(a.audio)
	}

	addr := net.JoinHostPort(a.spec.BindIP, strconv.Itoa(a.spec.SIPPort))
	go func() {
		// Serve bloquea hasta que se cancele el contexto; el error solo importa si
		// no fue una parada ordenada.
		if err := a.core.Serve(ctx, a.spec.Transport, addr); err != nil && ctx.Err() == nil {
			a.log.Error("agente: servidor SIP terminó con error", "id", a.spec.ID, "error", err)
		}
	}()

	// Faro propio del agente sobre sus trunks (OPTIONS desde este core).
	a.mon = monitor.New(a.core, a.trunks, a.monCfg, a.log)
	a.mon.Start(ctx)

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
	a.mon = nil // sus goroutines de OPTIONS paran con la cancelación del contexto
	a.state = StateStopped
	a.log.Info("agente parado", "id", a.spec.ID)
}

// AddTrunk asigna un trunk remoto al agente y, si está corriendo, empieza a
// monitorizarlo con OPTIONS. Valida y rechaza ids duplicados.
func (a *Agent) AddTrunk(t config.Target) error {
	if err := t.Validate(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ex := range a.trunks {
		if ex.ID == t.ID {
			return fmt.Errorf("el agente %q ya tiene un trunk con id %q", a.spec.ID, t.ID)
		}
	}
	a.trunks = append(a.trunks, t)
	if a.mon != nil {
		return a.mon.AddTarget(t)
	}
	return nil
}

// RemoveTrunk quita un trunk del agente. Devuelve false si no existía.
func (a *Agent) RemoveTrunk(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	encontrado := false
	for i, t := range a.trunks {
		if t.ID == id {
			a.trunks = append(a.trunks[:i], a.trunks[i+1:]...)
			encontrado = true
			break
		}
	}
	if encontrado && a.mon != nil {
		a.mon.RemoveTarget(id)
	}
	return encontrado
}

// TrunksSnapshot devuelve el estado de los trunks del agente. Si está corriendo,
// con el estado vivo del faro; si está parado, como "unknown".
func (a *Agent) TrunksSnapshot() []monitor.TargetState {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.mon != nil {
		return a.mon.Snapshot()
	}
	out := make([]monitor.TargetState, 0, len(a.trunks))
	for _, t := range a.trunks {
		out = append(out, monitor.TargetState{
			ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port, Status: monitor.StatusUnknown,
		})
	}
	return out
}

// Control devuelve el controlador de llamadas del agente (nil si está parado).
// Lo usa la web para lanzar/colgar llamadas en ESTE agente.
func (a *Agent) Control() *control.Controller {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctrl
}

// SetAudio fija el audio (PCM 8 kHz mono) que este agente enviará por RTP, tanto en
// llamadas salientes como entrantes. Persiste entre paradas y se aplica en caliente
// si el agente está corriendo.
func (a *Agent) SetAudio(pcm []int16) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.audio = pcm
	if a.ctrl != nil {
		a.ctrl.SetAudio(pcm)
	}
	if a.core != nil {
		a.core.SetMediaAudio(pcm)
	}
}

// ClearAudio descarta el audio del agente (las llamadas vuelven al tono).
func (a *Agent) ClearAudio() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.audio = nil
	if a.ctrl != nil {
		a.ctrl.ClearAudio()
	}
	if a.core != nil {
		a.core.SetMediaAudio(nil)
	}
}

// AudioSamples devuelve cuántas muestras de audio tiene cargadas el agente (0 = tono).
func (a *Agent) AudioSamples() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.audio)
}

// SetUASScenario asigna un escenario UAS al agente: su guion (ya traducido por la
// web) dirigirá la respuesta a las llamadas entrantes. Persiste entre paradas y se
// aplica EN CALIENTE si el agente está corriendo (las llamadas nuevas lo usan).
func (a *Agent) SetUASScenario(name string, pol sipcore.UASPolicy) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.uasScenario = name
	p := pol
	a.uasPol = &p
	if a.core != nil {
		a.core.SetUASPolicy(pol)
	}
}

// ClearUASScenario quita el escenario UAS: el agente vuelve a su auto-answer fijo
// (la política de su Spec). Aplica en caliente si está corriendo.
func (a *Agent) ClearUASScenario() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.uasScenario = ""
	a.uasPol = nil
	if a.core != nil {
		a.core.SetUASPolicy(a.spec.policy())
	}
}

// UASScenario devuelve el nombre del escenario UAS asignado ("" = auto-answer fijo).
func (a *Agent) UASScenario() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.uasScenario
}

// Core devuelve el núcleo SIP del agente (nil si está parado y no adoptado).
func (a *Agent) Core() *sipcore.Core {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.core
}

// checkBindAvailable comprueba que podemos abrir realmente ese ip:puerto en esta
// máquina: detecta "puerto en uso" y "IP no asignable" (no es local). Abre el
// socket un instante y lo cierra; el Serve posterior lo reabrirá.
//
// Hay una ventana de carrera mínima (otro proceso podría coger el puerto entre el
// cierre y el Serve), aceptable para una herramienta de pruebas; el Serve lo
// reportaría igualmente en el log.
func checkBindAvailable(transport, ip string, port int) error {
	addr := net.JoinHostPort(ip, strconv.Itoa(port))
	if transport == "tcp" {
		l, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}
		return l.Close()
	}
	c, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	return c.Close()
}

// Info es la foto del estado de un agente para la interfaz web.
type Info struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	BindIP      string `json:"bind_ip"`
	SIPPort     int    `json:"sip_port"`
	Transport   string `json:"transport"`
	State       string `json:"state"`
	UASScenario string `json:"uas_scenario,omitempty"` // escenario UAS asignado ("" = auto-answer fijo)
}

// info construye la foto del agente bajo bloqueo.
func (a *Agent) info() Info {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Info{
		ID:          a.spec.ID,
		Name:        a.spec.Name,
		BindIP:      a.spec.BindIP,
		SIPPort:     a.spec.SIPPort,
		Transport:   a.spec.Transport,
		State:       a.state,
		UASScenario: a.uasScenario,
	}
}
