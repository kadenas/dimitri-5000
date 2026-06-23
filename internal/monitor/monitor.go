// Paquete monitor: el "faro". Vigila cada troncal enviando OPTIONS de forma
// periódica y mantiene su estado (activa, degradada o caída) junto con
// contadores y la última latencia. Es el corazón de la v1.
package monitor

import (
	"context"
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

	mu      sync.RWMutex // protege el mapa de estados frente a accesos concurrentes
	states  map[string]*TargetState
	targets []config.Target
}

// New crea el faro con sus troncales, pero todavía NO lo arranca.
func New(core *sipcore.Core, targets []config.Target, cfg config.MonitorConfig, log *slog.Logger) *Monitor {
	m := &Monitor{
		core:    core,
		cfg:     cfg,
		log:     log,
		states:  make(map[string]*TargetState),
		targets: targets,
	}
	// Cada troncal arranca en estado "unknown" hasta el primer sondeo.
	for _, t := range targets {
		m.states[t.ID] = &TargetState{
			ID: t.ID, Name: t.Name, Host: t.Host, Port: t.Port,
			Status: StatusUnknown,
		}
	}
	return m
}

// Start lanza una goroutine por cada troncal. Cada una envía OPTIONS al ritmo
// configurado hasta que el contexto se cancele (Ctrl+C / parada del programa).
//
// Concepto Go: una "goroutine" es un hilo ligero (se lanza con la palabra clave
// `go`). Aquí cada troncal se vigila en paralelo sin que una bloquee a las demás.
func (m *Monitor) Start(ctx context.Context) {
	for _, t := range m.targets {
		go m.watch(ctx, t)
	}
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
			return // parada limpia
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

	// A partir de aquí tocamos el estado compartido: bloqueamos en escritura.
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.states[t.ID]
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
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]TargetState, 0, len(m.states))
	// Recorremos en el orden del fichero de config (no en el orden aleatorio
	// del mapa) para que la tabla salga siempre igual.
	for _, t := range m.targets {
		out = append(out, *m.states[t.ID]) // copia por valor
	}
	return out
}
