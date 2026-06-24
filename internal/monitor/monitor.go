// Paquete monitor: el "faro". Vigila cada troncal enviando OPTIONS de forma
// periódica y mantiene su estado (activa, degradada o caída) junto con
// contadores y la última latencia. Es el corazón de la v1.
//
// Faro DINÁMICO: cada troncal se vigila en su propia goroutine con su propia
// cancelación, de modo que se pueden añadir o quitar troncales EN CALIENTE
// (AddTarget / RemoveTarget / Sync) sin reiniciar la aplicación. Esto es la base
// del bloque "app configurable": la API web y el config.Store empujan cambios al
// faro mientras corre.
package monitor

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kadenas/dimitri-5000/internal/config"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// Estados posibles de una troncal. Usamos strings porque viajan tal cual a la
// interfaz web (JSON) y son legibles también en los logs.
const (
	StatusUnknown  = "unknown"  // aún no se ha sondeado
	StatusUp       = "up"       // respondió 200 OK
	StatusDegraded = "degraded" // respondió SIP, pero NO 200 (ej: 403, 503)
	StatusDown     = "down"     // sin respuesta tras varios intentos seguidos
)

// TargetState es la foto del estado de una troncal en un instante dado.
// Las etiquetas `json:"..."` definen cómo se serializa para la web.
type TargetState struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Status     string `json:"status"`
	LastCode   int    `json:"last_code"`
	LastReason string `json:"last_reason"`
	LastRTTms  int64  `json:"last_rtt_ms"`
	Sent       int    `json:"sent"`
	Ok         int    `json:"ok"`
	Other      int    `json:"other"`
	Timeout    int    `json:"timeout"`
	UpdatedAt  string `json:"updated_at"`

	consecutiveFails int // interno: no se exporta a JSON (empieza en minúscula)
}

// Monitor mantiene el estado de todas las troncales de forma segura entre
// goroutines (cada troncal escribe en su goroutine; la web lee).
type Monitor struct {
	core *sipcore.Core
	cfg  config.MonitorConfig
	log  *slog.Logger

	// rootCtx es el contexto padre del que cuelgan todas las goroutines de
	// vigilancia. Se fija en Start; al cancelarse (Ctrl+C), todas paran.
	rootCtx context.Context

	// mu protege TODO el estado interno (estados, cancelaciones, troncales y
	// orden). Usamos un único mutex porque las operaciones son cortas y poco
	// frecuentes; prioriza la claridad frente a optimizar el bloqueo.
	mu      sync.Mutex
	states  map[string]*TargetState        // estado vivo de cada troncal, por id
	cancels map[string]context.CancelFunc  // cómo parar la goroutine de cada troncal
	targets map[string]config.Target       // datos de cada troncal (host/puerto/transporte)
	order   []string                       // ids en orden de alta (Snapshot estable)
}

// New crea el faro con sus troncales iniciales, pero todavía NO lo arranca
// (eso lo hace Start). La firma se mantiene para no tocar la capa de arranque.
func New(core *sipcore.Core, targets []config.Target, cfg config.MonitorConfig, log *slog.Logger) *Monitor {
	m := &Monitor{
		core:    core,
		cfg:     cfg,
		log:     log,
		states:  make(map[string]*TargetState),
		cancels: make(map[string]context.CancelFunc),
		targets: make(map[string]config.Target),
	}
	// Registramos las troncales iniciales (sin arrancar goroutines todavía).
	for _, t := range targets {
		m.registerLocked(t)
	}
	return m
}

// Start fija el contexto raíz y lanza una goroutine por cada troncal ya
// registrada. A partir de aquí, AddTarget también arranca su goroutine al vuelo.
//
// Concepto Go: una "goroutine" es un hilo ligero (se lanza con la palabra clave
// `go`). Aquí cada troncal se vigila en paralelo sin que una bloquee a las demás.
func (m *Monitor) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rootCtx = ctx
	for _, id := range m.order {
		m.startWatcherLocked(id)
	}
}

// AddTarget añade y empieza a vigilar una troncal en caliente. Valida los datos,
// rechaza ids duplicados y, si el faro ya está arrancado, lanza su goroutine.
func (m *Monitor) AddTarget(t config.Target) error {
	// Validate normaliza el transporte y comprueba host/puerto. Mejor fallar aquí
	// (antes de tocar el estado) que dejar una troncal a medias.
	if err := t.Validate(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.targets[t.ID]; exists {
		return fmt.Errorf("ya se está vigilando un trunk con id %q", t.ID)
	}
	m.registerLocked(t)
	m.startWatcherLocked(t.ID)
	m.log.Info("troncal añadida al faro", "id", t.ID, "host", t.Host, "port", t.Port)
	return nil
}

// RemoveTarget deja de vigilar la troncal indicada (cancela su goroutine y olvida
// su estado). Devuelve false si no se estaba vigilando.
func (m *Monitor) RemoveTarget(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, known := m.targets[id]; !known {
		return false
	}
	if cancel, running := m.cancels[id]; running {
		cancel() // pide a su goroutine que termine
		delete(m.cancels, id)
	}
	delete(m.targets, id)
	delete(m.states, id)
	for i, x := range m.order {
		if x == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.log.Info("troncal retirada del faro", "id", id)
	return true
}

// Sync reconcilia las troncales vivas con la lista deseada (la que tiene el
// config.Store): añade las nuevas, retira las que sobran y reinicia las que han
// cambiado de host/puerto/transporte/nombre. Es la vía que usará la API web para
// aplicar de golpe el estado de la configuración.
func (m *Monitor) Sync(targets []config.Target) {
	// Construimos el conjunto deseado, descartando troncales inválidas (no deben
	// tumbar la sincronización del resto).
	desired := make(map[string]config.Target, len(targets))
	for _, t := range targets {
		if err := t.Validate(); err != nil {
			m.log.Warn("troncal inválida ignorada en Sync", "id", t.ID, "error", err)
			continue
		}
		desired[t.ID] = t
	}

	// Foto del estado actual bajo bloqueo (ids y datos), para luego decidir fuera
	// del lock y reutilizar AddTarget/RemoveTarget (que vuelven a bloquear).
	m.mu.Lock()
	current := make(map[string]config.Target, len(m.targets))
	for id, t := range m.targets {
		current[id] = t
	}
	m.mu.Unlock()

	// Retirar o reiniciar las existentes.
	for id, cur := range current {
		want, ok := desired[id]
		switch {
		case !ok:
			m.RemoveTarget(id) // ya no está en la configuración
		case cur != want:
			// Cambió algún dato: la forma simple y segura es recrearla.
			m.RemoveTarget(id)
			if err := m.AddTarget(want); err != nil {
				m.log.Warn("no se pudo reiniciar la troncal en Sync", "id", id, "error", err)
			}
		}
	}

	// Añadir las nuevas.
	for id, want := range desired {
		if _, exists := current[id]; !exists {
			if err := m.AddTarget(want); err != nil {
				m.log.Warn("no se pudo añadir la troncal en Sync", "id", id, "error", err)
			}
		}
	}
}

// registerLocked da de alta una troncal en el estado interno (sin arrancar su
// goroutine). Debe llamarse con m.mu tomado.
func (m *Monitor) registerLocked(t config.Target) {
	m.targets[t.ID] = t
	m.order = append(m.order, t.ID)
	m.states[t.ID] = &TargetState{
		ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port,
		Status: StatusUnknown, // hasta el primer sondeo
	}
}

// startWatcherLocked lanza la goroutine de vigilancia de una troncal si procede.
// No hace nada si el faro aún no está arrancado (Start la lanzará) o si ya hay
// una goroutine viva para ese id. Debe llamarse con m.mu tomado.
func (m *Monitor) startWatcherLocked(id string) {
	if m.rootCtx == nil {
		return // todavía no se ha llamado a Start
	}
	if _, running := m.cancels[id]; running {
		return // ya se está vigilando
	}
	t := m.targets[id]
	// Cada troncal cuelga del contexto raíz pero con su propia cancelación, para
	// poder pararla individualmente sin afectar a las demás.
	wctx, cancel := context.WithCancel(m.rootCtx)
	m.cancels[id] = cancel
	go m.watch(wctx, t)
}

// watch es el bucle de vigilancia de UNA troncal.
func (m *Monitor) watch(ctx context.Context, t config.Target) {
	// Un Ticker dispara en su canal cada `interval`. Es la forma idiomática en
	// Go de hacer algo "cada X segundos" sin ir acumulando desfases.
	ticker := time.NewTicker(m.cfg.Interval())
	defer ticker.Stop()

	// Primer sondeo inmediato (no esperamos al primer tick) para tener estado ya.
	m.probe(ctx, t)

	for {
		select {
		case <-ctx.Done():
			return // parada limpia (Ctrl+C o RemoveTarget)
		case <-ticker.C:
			m.probe(ctx, t)
		}
	}
}

// probe hace un único envío de OPTIONS y actualiza el estado de la troncal.
func (m *Monitor) probe(ctx context.Context, t config.Target) {
	// Timeout por envío: si la troncal no responde en este tiempo, cuenta como
	// fallo. context.WithTimeout deriva un contexto que se cancela solo al vencer.
	reqCtx, cancel := context.WithTimeout(ctx, m.cfg.Timeout())
	defer cancel()

	res, err := m.core.SendOptions(reqCtx, t.Host, t.Port, t.Transport)

	// A partir de aquí tocamos el estado compartido: bloqueamos.
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[t.ID]
	if st == nil {
		// La troncal se retiró mientras este sondeo estaba en vuelo: nada que hacer.
		return
	}
	st.Sent++
	st.UpdatedAt = time.Now().Format(time.RFC3339)

	switch {
	case err != nil:
		// Sin respuesta (timeout / inalcanzable): cuenta como caída de red.
		st.Timeout++
		st.consecutiveFails++
		st.LastCode = 0
		st.LastReason = "sin respuesta"
		// Solo marcamos DOWN tras varios fallos seguidos, para no alarmar por
		// un único paquete perdido.
		if st.consecutiveFails >= m.cfg.FailThreshold {
			st.Status = StatusDown
		}
	case res.Code == 200:
		// La troncal está viva y sana.
		st.Ok++
		st.consecutiveFails = 0
		st.LastCode = 200
		st.LastReason = res.Reason
		st.LastRTTms = res.RTT.Milliseconds()
		st.Status = StatusUp
	default:
		// Respondió SIP pero no 200: la troncal "está", pero algo pasa
		// (403, 503, etc.). Esto es info valiosa, no una caída.
		st.Other++
		st.consecutiveFails = 0 // hubo respuesta: no es caída de red
		st.LastCode = res.Code
		st.LastReason = res.Reason
		st.LastRTTms = res.RTT.Milliseconds()
		st.Status = StatusDegraded
	}
}

// Snapshot devuelve una copia del estado actual de todas las troncales, segura
// para entregar a la interfaz web. Devolvemos COPIAS para que el consumidor no
// toque la estructura interna mientras las goroutines la están actualizando.
func (m *Monitor) Snapshot() []TargetState {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TargetState, 0, len(m.states))
	// Recorremos en el orden de alta (no en el orden aleatorio del mapa) para que
	// la tabla salga siempre igual.
	for _, id := range m.order {
		out = append(out, *m.states[id]) // copia por valor
	}
	return out
}
