// Store: capa de configuración viva y persistente. Es la ÚNICA fuente de verdad
// de la configuración mientras la app corre. Permite leer y modificar (añadir o
// borrar trunks, cambiar ajustes) de forma segura entre goroutines, y guarda los
// cambios en el config.json automáticamente.
//
// Lo usarán el faro dinámico (para saber qué trunks vigilar) y la API web (para
// el alta/baja de trunks y la edición de ajustes).
package config

import (
	"fmt"
	"os"
	"sync"
)

// DefaultPath es el fichero de configuración por defecto cuando no se indica otro.
const DefaultPath = "config.json"

// Store guarda la configuración en memoria, protegida por un mutex, y la persiste
// en disco en cada cambio.
type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// NewStore carga la configuración desde path (o usa DefaultPath si está vacío). Si
// el fichero no existe, parte de los valores por defecto; el primer cambio creará
// el fichero. Devuelve el Store listo para usar.
func NewStore(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath
	}
	// Si el fichero no existe aún, partimos de los valores por defecto: el primer
	// cambio desde la web lo creará. Solo cargamos si existe.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return &Store{path: path, cfg: defaults()}, nil
		}
		return nil, fmt.Errorf("accediendo a %s: %w", path, err)
	}
	cfg, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, cfg: cfg}, nil
}

// Path devuelve la ruta del fichero de configuración.
func (s *Store) Path() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.path
}

// Get devuelve una copia de la configuración actual (segura para leer sin bloquear
// al resto). Las slices se copian para que el consumidor no toque el estado interno.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneConfig(s.cfg)
}

// Targets devuelve una copia de la lista de trunks.
func (s *Store) Targets() []Target {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneTargets(s.cfg.Targets)
}

// AddTarget valida y añade un trunk nuevo (id único) y persiste. Devuelve error si
// el trunk es inválido o el id ya existe.
func (s *Store) AddTarget(t Target) error {
	if err := t.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.cfg.Targets {
		if existing.ID == t.ID {
			return fmt.Errorf("ya existe un trunk con id %q", t.ID)
		}
	}
	s.cfg.Targets = append(s.cfg.Targets, t)
	return s.persistLocked()
}

// RemoveTarget borra el trunk con el id dado y persiste. Devuelve (false, nil) si
// no existía.
func (s *Store) RemoveTarget(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.cfg.Targets {
		if t.ID == id {
			s.cfg.Targets = append(s.cfg.Targets[:i], s.cfg.Targets[i+1:]...)
			return true, s.persistLocked()
		}
	}
	return false, nil
}

// SetSignaling actualiza nuestros parámetros de señalización (bind_ip, sip_port,
// transport) y persiste. Estos cambios se aplican al REINICIAR (no recreamos el
// socket en caliente para no cortar llamadas en curso).
func (s *Store) SetSignaling(bindIP string, sipPort int, transport string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sipPort < 1 || sipPort > 65535 {
		return fmt.Errorf("puerto SIP inválido: %d", sipPort)
	}
	s.cfg.BindIP = bindIP
	s.cfg.SIPPort = sipPort
	if transport != "" {
		s.cfg.Transport = transport
	}
	return s.persistLocked()
}

// persistLocked guarda en disco. Debe llamarse con el mutex ya tomado.
func (s *Store) persistLocked() error {
	return Save(s.path, s.cfg)
}

// --- helpers de copia y errores ---------------------------------------------

func cloneConfig(c Config) Config {
	c.Targets = cloneTargets(c.Targets)
	return c
}

func cloneTargets(in []Target) []Target {
	out := make([]Target, len(in))
	copy(out, in)
	return out
}
