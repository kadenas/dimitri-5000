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
	ReqURI    string `json:"req_uri"`    // Request-URI (solo request)
	FromURI   string `json:"from_uri"`   // cabecera From (tal cual)
	ToURI     string `json:"to_uri"`     // cabecera To (tal cual)
	Raw       string `json:"raw"`        // mensaje completo
}

// CallSummary resume una llamada/diálogo presente en la traza, con los campos
// derivados que necesita el visor tipo SBC del panel 06. Conserva los campos
// originales (CallID/Count/FirstLine/LastTime) por compatibilidad.
type CallSummary struct {
	CallID    string `json:"call_id"`
	Count     int    `json:"count"`      // nº de mensajes
	FirstLine string `json:"first_line"` // primera línea del primer mensaje
	LastTime  string `json:"last_time"`  // hora del último mensaje

	// Derivados para la tabla de diálogos.
	StartTime   string `json:"start_time"`   // hora del primer mensaje
	State       string `json:"state"`        // ESTABLISHED / FAILED-xxx / TERMINATED-200 / RINGING / EARLY
	Method      string `json:"method"`       // método del primer request (INVITE, OPTIONS, MESSAGE...)
	ReqURI      string `json:"req_uri"`      // Request-URI del primer request
	FromURI     string `json:"from_uri"`     // From del diálogo
	ToURI       string `json:"to_uri"`       // To del diálogo
	Laddr       string `json:"laddr"`        // IP:puerto local (nuestro extremo)
	Raddr       string `json:"raddr"`        // IP:puerto remoto
	DurationSec int    `json:"duration_sec"` // segundos entre 200 (INVITE) y BYE
	// Rellenados por la capa web (trace no conoce agentes).
	OrigAgent string `json:"orig_agent"` // agente que maneja el extremo local
	DestAgent string `json:"dest_agent"` // agente/destino del extremo remoto
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

// timeLayout es el formato con el que se guarda Event.Time (para calcular duraciones).
const timeLayout = "2006-01-02T15:04:05.000"

// Calls resume las llamadas presentes, de la más reciente a la más antigua, con
// los campos derivados (estado, duración, identidades) que pinta el visor SBC.
func (s *Store) Calls() []CallSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Mapa id -> resumen, preservando el orden de primera aparición.
	idx := make(map[string]int)
	out := make([]CallSummary, 0, 8)
	// Estado intermedio por diálogo para derivar State/DurationSec al final.
	type acc struct {
		sawBye     bool
		finalCode  int    // mayor código final (>=200) visto a un INVITE
		prov       int    // mejor provisional (180/183/100) visto a un INVITE
		answerTime string // hora del 200 a INVITE
		byeTime    string // hora del BYE
	}
	accs := make([]acc, 0, 8)

	for _, e := range s.events {
		if e.CallID == "" {
			continue
		}
		i, ok := idx[e.CallID]
		if !ok {
			i = len(out)
			idx[e.CallID] = i
			out = append(out, CallSummary{
				CallID: e.CallID, FirstLine: e.FirstLine,
				StartTime: e.Time, Method: e.Method,
				ReqURI: e.ReqURI, FromURI: e.FromURI, ToURI: e.ToURI,
				Laddr: e.Laddr, Raddr: e.Raddr,
			})
			accs = append(accs, acc{})
		}
		out[i].Count++
		out[i].LastTime = e.Time
		a := &accs[i]

		// Completar identidades con el primer request que las traiga (el INVITE
		// inicial suele ir antes que las respuestas).
		if e.Kind == "request" {
			if out[i].ReqURI == "" {
				out[i].ReqURI = e.ReqURI
			}
			if out[i].Method == "" {
				out[i].Method = e.Method
			}
		}
		if out[i].FromURI == "" {
			out[i].FromURI = e.FromURI
		}
		if out[i].ToURI == "" {
			out[i].ToURI = e.ToURI
		}

		switch {
		case e.Kind == "request" && e.Method == "BYE":
			a.sawBye = true
			if a.byeTime == "" {
				a.byeTime = e.Time
			}
		case e.Kind == "response" && e.Method == "INVITE":
			switch {
			case e.Code >= 200:
				if e.Code > a.finalCode {
					a.finalCode = e.Code
				}
				if e.Code/100 == 2 && a.answerTime == "" {
					a.answerTime = e.Time
				}
			case e.Code >= 100:
				if e.Code > a.prov {
					a.prov = e.Code
				}
			}
		}
	}

	// Derivar State y DurationSec por diálogo.
	for i := range out {
		a := accs[i]
		switch {
		case a.sawBye:
			out[i].State = "TERMINATED-200"
		case a.finalCode >= 300:
			out[i].State = "FAILED-" + strconv.Itoa(a.finalCode)
		case a.finalCode/100 == 2:
			out[i].State = "ESTABLISHED"
		case a.prov == 180:
			out[i].State = "RINGING"
		case a.prov >= 100:
			out[i].State = "EARLY"
		}
		if a.answerTime != "" && a.byeTime != "" {
			t1, e1 := time.Parse(timeLayout, a.answerTime)
			t2, e2 := time.Parse(timeLayout, a.byeTime)
			if e1 == nil && e2 == nil {
				if d := int(t2.Sub(t1).Seconds()); d >= 0 {
					out[i].DurationSec = d
				}
			}
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
		// Petición: "INVITE sip:2000@host SIP/2.0" -> método y Request-URI.
		ev.Kind = "request"
		campos := strings.Fields(ev.FirstLine)
		if len(campos) >= 1 {
			ev.Method = campos[0]
		}
		if len(campos) >= 2 {
			ev.ReqURI = campos[1]
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
		case "from", "f":
			if ev.FromURI == "" {
				ev.FromURI = strings.TrimSpace(val)
			}
		case "to", "t":
			if ev.ToURI == "" {
				ev.ToURI = strings.TrimSpace(val)
			}
		}
	}
	return ev
}
