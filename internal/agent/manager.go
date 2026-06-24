// Manager: posee y orquesta los agentes SIP de la aplicación. Es la pieza que
// hace posible "un proceso, varias instancias": da de alta, arranca, para y
// elimina agentes en caliente, de forma segura entre goroutines.
//
// Mismo patrón que el faro dinámico (internal/monitor): un mapa protegido por
// mutex + una slice de orden para que la lista salga siempre igual en la web.
package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/kadenas/dimitri-5000/internal/config"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// Manager gestiona el conjunto de agentes.
type Manager struct {
	log    *slog.Logger
	monCfg config.MonitorConfig // parámetros del faro que heredan los agentes

	// root es el contexto de vida de la app; del que cuelgan los Serve de los
	// agentes. Se fija con Bind antes de arrancar ninguno.
	root context.Context

	mu     sync.Mutex
	agents map[string]*Agent
	order  []string // ids en orden de alta (Snapshot estable)
}

// NewManager crea un gestor vacío. monCfg son los parámetros de monitorización
// (intervalo/timeout/umbral) que usarán los faros de los agentes.
func NewManager(monCfg config.MonitorConfig, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		log:    log,
		monCfg: monCfg,
		agents: make(map[string]*Agent),
	}
}

// Bind fija el contexto raíz del que colgarán los agentes al arrancar. Debe
// llamarse una vez, antes de Start.
func (m *Manager) Bind(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.root = ctx
}

// validateSpec normaliza y comprueba una Spec antes de dar de alta el agente.
func validateSpec(s *Spec) error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("el agente necesita un 'id'")
	}
	if strings.TrimSpace(s.BindIP) == "" {
		return fmt.Errorf("el agente %q necesita una IP de bind", s.ID)
	}
	if s.SIPPort < 1 || s.SIPPort > 65535 {
		return fmt.Errorf("puerto SIP inválido en el agente %q: %d", s.ID, s.SIPPort)
	}
	s.Transport = strings.ToLower(strings.TrimSpace(s.Transport))
	if s.Transport != "tcp" {
		s.Transport = "udp" // por defecto y para cualquier valor no reconocido
	}
	if s.AnswerCode == 0 {
		s.AnswerCode = 200 // por defecto contesta
	}
	if strings.TrimSpace(s.UserAgent) == "" {
		s.UserAgent = "dimitri-5000"
	}
	return nil
}

// Add da de alta un agente (en estado "stopped") que CREARÁ su propio Core al
// arrancar. Rechaza ids duplicados.
func (m *Manager) Add(spec Spec) (*Agent, error) {
	return m.add(spec, nil)
}

// AddWithCore da de alta un agente que ADOPTA un Core ya existente. Es la vía que
// usa la capa de arranque para el agente por defecto, reutilizando el Core que ya
// tenía montado (sin volver a abrir el puerto).
func (m *Manager) AddWithCore(spec Spec, core *sipcore.Core) (*Agent, error) {
	return m.add(spec, core)
}

func (m *Manager) add(spec Spec, core *sipcore.Core) (*Agent, error) {
	if err := validateSpec(&spec); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.agents[spec.ID]; exists {
		return nil, fmt.Errorf("ya existe un agente con id %q", spec.ID)
	}
	a := newAgent(spec, m.monCfg, core, m.log)
	m.agents[spec.ID] = a
	m.order = append(m.order, spec.ID)
	return a, nil
}

// Start arranca el agente indicado.
func (m *Manager) Start(id string) error {
	m.mu.Lock()
	a, ok := m.agents[id]
	root := m.root
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no existe el agente %q", id)
	}
	if root == nil {
		return fmt.Errorf("manager sin contexto: llama a Bind antes de Start")
	}
	return a.Start(root)
}

// Stop para el agente indicado (sin eliminarlo: se puede volver a arrancar).
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	a, ok := m.agents[id]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("no existe el agente %q", id)
	}
	a.Stop()
	return nil
}

// Remove para y elimina el agente indicado. Devuelve false si no existía.
func (m *Manager) Remove(id string) bool {
	m.mu.Lock()
	a, ok := m.agents[id]
	if ok {
		delete(m.agents, id)
		for i, x := range m.order {
			if x == id {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	a.Stop() // fuera del lock: Stop puede tardar (cancela Serve)
	return true
}

// Get devuelve el agente con el id dado (nil si no existe).
func (m *Manager) Get(id string) *Agent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.agents[id]
}

// Snapshot devuelve la foto de todos los agentes en orden de alta.
func (m *Manager) Snapshot() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.order))
	for _, id := range m.order {
		if a := m.agents[id]; a != nil {
			out = append(out, a.info())
		}
	}
	return out
}

// StopAll para todos los agentes (parada ordenada de la app).
func (m *Manager) StopAll() {
	m.mu.Lock()
	agents := make([]*Agent, 0, len(m.order))
	for _, id := range m.order {
		if a := m.agents[id]; a != nil {
			agents = append(agents, a)
		}
	}
	m.mu.Unlock()
	for _, a := range agents {
		a.Stop()
	}
}
