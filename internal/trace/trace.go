// Paquete trace: almacena la traza de mensajes SIP capturados y la prepara para
// el diagrama de escalera (ladder) de la web. No conoce sipgo: recibe los mensajes
// ya como bytes a través de Store.Record (lo cablea sipcore.EnableTracing).
//
// Hace un parseo MÍNIMO de cada mensaje (primera línea, Call-ID, CSeq) suficiente
// para agrupar por llamada y pintar el ladder; guarda también el mensaje completo
// por si se quiere inspeccionar.
package trace

import (
	"strconv"
	"strings"
	"sync"
	"time"
)

// Event es un mensaje SIP capturado con lo necesario para el ladder.
type Event struct {
	Seq       int    `json:"seq"`        // orden global de captura
	Time      string `json:"time"`       // hora (RFC3339 con ms)
	Dir       string `json:"dir"`        // "in" (recibido) | "out" (enviado)
	Transport string `json:"transport"`  // "UDP", "TCP"...
	Laddr     string `json:"laddr"`      // dirección local (nuestro agente)
	Raddr     string `json:"raddr"`      // dirección remota
	Kind      string `json:"kind"`       // "request" | "response"
	FirstLine string `json:"first_line"` // primera línea del mensaje
	Method    string `json:"method"`     // método (request) o método del CSeq (response)
	Code      int    `json:"code"`       // código (solo response)
	CallID    string `json:"call_id"`    // Call-ID (agrupa el diálogo)
	CSeq      string `json:"cseq"`       // valor de la cabecera CSeq
	Raw       string `json:"raw"`        // mensaje completo
}

// CallSummary resume una llamada/diálogo presente en la traza (para elegirla).
type CallSummary struct {
	CallID    string `json:"call_id"`
	Count     int    `json:"count"`      // nº de mensajes
	FirstLine string `json:"first_line"` // primera línea del primer mensaje
	LastTime  string `json:"last_time"`  // hora del último mensaje
}

// Store guarda los últimos eventos en un buffer acotado, seguro entre goroutines.
type Store struct {
	mu     sync.Mutex
	events []Event
	max    int
	seq    int
}

// NewStore crea el almacén con un máximo de eventos (los más antiguos se descartan).
func NewStore(max int) *Store {
	if max <= 0 {
		max = 2000
	}
	return &Store{max: max}
}

// Record parsea y guarda un mensaje. Es el callback que recibe sipcore (TraceFunc).
func (s *Store) Record(dir, transport, laddr, raddr string, raw []byte) {
	msg := string(raw)
	if strings.TrimSpace(msg) == "" {
		return // keepalives vacíos (CRLF) u otros paquetes sin contenido
	}

	ev := parse(msg)
	ev.Dir = dir
	ev.Transport = transport
	ev.Laddr = laddr
	ev.Raddr = raddr
	ev.Time = time.Now().Format("2006-01-02T15:04:05.000")
	ev.Raw = msg

	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	ev.Seq = s.seq
	s.events = append(s.events, ev)
	// Recorte del buffer: nos quedamos con los más recientes.
	if len(s.events) > s.max {
		s.events = s.events[len(s.events)-s.max:]
	}
}

// Snapshot devuelve una copia de todos los eventos guardados.
func (s *Store) Snapshot() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// ByCallID devuelve los eventos de una llamada concreta (en orden de captura).
func (s *Store) ByCallID(id string) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, 0, 16)
	for _, e := range s.events {
		if e.CallID == id {
			out = append(out, e)
		}
	}
	return out
}

// Calls resume las llamadas presentes, de la más reciente a la más antigua.
func (s *Store) Calls() []CallSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Mapa id -> resumen, preservando el orden de primera aparición.
	idx := make(map[string]int)
	out := make([]CallSummary, 0, 8)
	for _, e := range s.events {
		if e.CallID == "" {
			continue
		}
		if i, ok := idx[e.CallID]; ok {
			out[i].Count++
			out[i].LastTime = e.Time
		} else {
			idx[e.CallID] = len(out)
			out = append(out, CallSummary{
				CallID: e.CallID, Count: 1, FirstLine: e.FirstLine, LastTime: e.Time,
			})
		}
	}
	// Invertimos para mostrar las más recientes primero.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// Clear vacía la traza (botón "limpiar" en la web).
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = nil
}

// parse extrae la primera línea, el tipo (request/response), método/código,
// Call-ID y CSeq de un mensaje SIP en texto.
func parse(msg string) Event {
	ev := Event{}
	// Separamos cabeceras del cuerpo y cogemos líneas.
	head := msg
	if i := strings.Index(msg, "\r\n\r\n"); i >= 0 {
		head = msg[:i]
	}
	lines := strings.Split(head, "\r\n")
	if len(lines) == 0 {
		return ev
	}
	ev.FirstLine = strings.TrimSpace(lines[0])

	if strings.HasPrefix(ev.FirstLine, "SIP/2.0") {
		// Respuesta: "SIP/2.0 200 OK"
		ev.Kind = "response"
		partes := strings.SplitN(ev.FirstLine, " ", 3)
		if len(partes) >= 2 {
			ev.Code, _ = strconv.Atoi(partes[1])
		}
	} else {
		// Petición: "INVITE sip:... SIP/2.0"
		ev.Kind = "request"
		if sp := strings.IndexByte(ev.FirstLine, ' '); sp > 0 {
			ev.Method = ev.FirstLine[:sp]
		}
	}

	// Cabeceras que nos interesan (case-insensitive; admite formas cortas).
	for _, l := range lines[1:] {
		name, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "call-id", "i":
			if ev.CallID == "" {
				ev.CallID = strings.TrimSpace(val)
			}
		case "cseq":
			ev.CSeq = strings.TrimSpace(val)
			// En respuestas, el método viene en el CSeq ("1 INVITE").
			if ev.Method == "" {
				campos := strings.Fields(ev.CSeq)
				if len(campos) == 2 {
					ev.Method = campos[1]
				}
			}
		}
	}
	return ev
}
