// Paquete webui: sirve la interfaz web local y la API que la alimenta. La
// interfaz NO conoce SIP; solo le pide estado al faro y a los agentes y lo pinta.
// Esta separación es deliberada: el motor podría funcionar sin web, y la web
// podría cambiarse entera sin tocar el motor.
//
// Desde G2 la web habla con un Manager de agentes (no con un único control): puede
// listar agentes, darlos de alta/baja, arrancarlos/pararlos y lanzar llamadas
// eligiendo QUÉ agente las origina.
package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/kadenas/dimitri-5000/internal/agent"
	"github.com/kadenas/dimitri-5000/internal/config"
	"github.com/kadenas/dimitri-5000/internal/control"
	"github.com/kadenas/dimitri-5000/internal/load"
	"github.com/kadenas/dimitri-5000/internal/media"
	"github.com/kadenas/dimitri-5000/internal/monitor"
	"github.com/kadenas/dimitri-5000/internal/netutil"
	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
	"github.com/kadenas/dimitri-5000/internal/trace"
)

// go:embed mete los ficheros de la carpeta static/ DENTRO del binario, para que
// dimitri-5000 sea un único ejecutable sin ficheros sueltos que copiar. Es la
// pieza que hace posible "copiar un binario y ejecutar" en una máquina aislada.
//
//go:embed static
var staticFiles embed.FS

// Server es la interfaz web local.
type Server struct {
	addr         string
	monitor      *monitor.Monitor // puede ser nil (modo sin faro)
	manager      *agent.Manager   // puede ser nil (modo monitor sin agentes)
	trace        *trace.Store     // puede ser nil (sin captura de traza)
	scenariosDir string           // carpeta de disco con los escenarios YAML
	log          *slog.Logger
}

// New crea el servidor web (no lo arranca todavía). monitor, manager y trace son
// opcionales: el modo monitor pasa manager=nil; el modo web pasa todos.
// scenariosDir es la carpeta de donde se listan/cargan los escenarios.
func New(addr string, m *monitor.Monitor, mgr *agent.Manager, tr *trace.Store, scenariosDir string, log *slog.Logger) *Server {
	return &Server{addr: addr, monitor: m, manager: mgr, trace: tr, scenariosDir: scenariosDir, log: log}
}

// Run arranca el servidor HTTP y lo detiene limpiamente cuando ctx se cancela.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// API: estado de troncales del faro global (modo monitor legacy).
	mux.HandleFunc("/api/status", s.handleStatus)

	// API: información de red local (IPs detectadas) para sugerir el BIND IP.
	mux.HandleFunc("/api/netinfo", s.handleNetInfo) // GET: {local_ip, ips}

	// API de trunks por agente (modo web): lista global + alta/baja.
	mux.HandleFunc("/api/trunks", s.handleTrunks)             // GET lista | POST alta
	mux.HandleFunc("/api/trunks/remove", s.handleTrunkRemove) // POST {agent_id, id}

	// API de agentes (instancias SIP).
	mux.HandleFunc("/api/agents", s.handleAgents)             // GET lista | POST alta
	mux.HandleFunc("/api/agents/start", s.handleAgentStart)   // POST {id}
	mux.HandleFunc("/api/agents/stop", s.handleAgentStop)     // POST {id}
	mux.HandleFunc("/api/agents/remove", s.handleAgentRemove) // POST {id}

	// API de control de llamadas (por agente).
	mux.HandleFunc("/api/calls", s.handleCalls)            // GET: estado de las llamadas
	mux.HandleFunc("/api/call", s.handlePlaceCall)         // POST: lanzar una llamada
	mux.HandleFunc("/api/call/hangup", s.handleHangup)     // POST: colgar una llamada
	mux.HandleFunc("/api/call/transfer", s.handleTransfer) // POST: desviar (REFER)

	// API de mensajería SIP (MESSAGE).
	mux.HandleFunc("/api/messages", s.handleMessages)   // GET: enviados y recibidos
	mux.HandleFunc("/api/message", s.handleSendMessage) // POST: enviar un MESSAGE

	// API de media: audio (WAV) que el agente envía por RTP en vez del tono.
	mux.HandleFunc("/api/media", s.handleMedia)            // GET estado | POST subir WAV
	mux.HandleFunc("/api/media/clear", s.handleMediaClear) // POST {agent_id}: volver al tono

	// API de la traza SIP (diagrama de escalera).
	mux.HandleFunc("/api/trace/calls", s.handleTraceCalls) // GET: llamadas en la traza
	mux.HandleFunc("/api/trace/clear", s.handleTraceClear) // POST: vaciar la traza
	mux.HandleFunc("/api/trace", s.handleTrace)            // GET ?call_id=: eventos

	// API de pruebas de carga (Fase 3): arrancar/parar y stats agregadas por agente.
	mux.HandleFunc("/api/load", s.handleLoad)            // GET: stats por agente
	mux.HandleFunc("/api/load/start", s.handleLoadStart) // POST: arrancar carga
	mux.HandleFunc("/api/load/stop", s.handleLoadStop)   // POST {agent_id}: parar carga

	// API de escenarios (Fase 2 en la web).
	mux.HandleFunc("/api/scenarios", s.handleScenarios)         // GET: disponibles en disco
	mux.HandleFunc("/api/scenarios/run", s.handleScenarioRun)   // POST: ejecutar uno
	mux.HandleFunc("/api/scenarios/runs", s.handleScenarioRuns) // GET: ejecuciones y su estado

	// Ficheros estáticos (index.html, css, js) servidos desde el embed.
	// fs.Sub quita el prefijo "static" para que "/" sirva static/index.html.
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	// noCache evita que el navegador sirva versiones viejas de la UI durante el
	// desarrollo (los estáticos van embebidos y cambian con cada compilación).
	mux.Handle("/", noCache(http.FileServer(http.FS(sub))))

	srv := &http.Server{Addr: s.addr, Handler: mux}

	// Goroutine que espera la cancelación del contexto para apagar el servidor
	// de forma ordenada (deja terminar las peticiones en curso).
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	// ListenAndServe bloquea hasta que se apaga; ErrServerClosed es el cierre
	// normal y no debe tratarse como error.
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// noCache envuelve un handler para que el navegador no cachee la respuesta. Útil
// para los estáticos embebidos: así, al recompilar, siempre se sirve lo último.
func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		h.ServeHTTP(w, r)
	})
}

// writeJSON serializa v como JSON con la cabecera adecuada.
func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		s.log.Error("no se pudo serializar la respuesta", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
	}
}

// handleStatus devuelve el snapshot del faro como JSON. Si no hay faro, devuelve
// una lista vacía para que la web no falle.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if s.monitor == nil {
		s.writeJSON(w, []any{})
		return
	}
	s.writeJSON(w, s.monitor.Snapshot())
}

// netIP describe una IPv4 local detectada: la dirección a secas (para volcarla en
// un campo) y una etiqueta legible con la interfaz (para mostrarla en la lista).
type netIP struct {
	IP    string `json:"ip"`
	Label string `json:"label"`
}

// handleNetInfo devuelve la IP local principal (la que el sistema usaría para
// salir) y todas las IPv4 detectadas, para que la web pueda SUGERIR el BIND IP
// sin que el usuario tenga que teclearlo ni conocerlo de memoria.
func (s *Server) handleNetInfo(w http.ResponseWriter, r *http.Request) {
	local, _ := netutil.LocalIP() // "" si no se pudo determinar; la web lo tolera

	ips := make([]netIP, 0)
	for _, entry := range netutil.ListIPv4() {
		// ListIPv4 devuelve "192.168.0.137 (eth0)": separamos IP y etiqueta.
		ip := entry
		if i := strings.IndexByte(entry, ' '); i > 0 {
			ip = entry[:i]
		}
		ips = append(ips, netIP{IP: ip, Label: entry})
	}

	s.writeJSON(w, map[string]any{"local_ip": local, "ips": ips})
}

// --- Trunks por agente -------------------------------------------------------

// trunkView es el estado de un trunk etiquetado con el agente que lo monitoriza.
type trunkView struct {
	AgentID string `json:"agent_id"`
	monitor.TargetState
}

// trunkReq es el cuerpo JSON para dar de alta un trunk.
type trunkReq struct {
	AgentID   string `json:"agent_id"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Transport string `json:"transport"`
}

// handleTrunks: GET lista (agregada de todos los agentes) | POST alta en un agente.
func (s *Server) handleTrunks(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		s.writeJSON(w, []any{})
		return
	}
	switch r.Method {
	case http.MethodGet:
		out := make([]trunkView, 0)
		for _, info := range s.manager.Snapshot() {
			a := s.manager.Get(info.ID)
			if a == nil {
				continue
			}
			for _, ts := range a.TrunksSnapshot() {
				out = append(out, trunkView{AgentID: info.ID, TargetState: ts})
			}
		}
		s.writeJSON(w, out)
	case http.MethodPost:
		var req trunkReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		a := s.manager.Get(req.AgentID)
		if a == nil {
			http.Error(w, "agente no encontrado", http.StatusBadRequest)
			return
		}
		t := config.Target{ID: req.ID, Name: req.Name, Host: req.Host, Port: req.Port, Transport: req.Transport}
		if err := a.AddTrunk(t); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		s.writeJSON(w, map[string]string{"id": req.ID})
	default:
		http.Error(w, "usa GET o POST", http.StatusMethodNotAllowed)
	}
}

// handleTrunkRemove quita un trunk de su agente.
func (s *Server) handleTrunkRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "gestor de agentes no disponible", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		AgentID string `json:"agent_id"`
		ID      string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.AgentID == "" {
		http.Error(w, "JSON inválido: se requieren 'agent_id' e 'id'", http.StatusBadRequest)
		return
	}
	a := s.manager.Get(req.AgentID)
	if a == nil || !a.RemoveTrunk(req.ID) {
		http.Error(w, "trunk no encontrado", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Agentes -----------------------------------------------------------------

// agentReq es el cuerpo JSON para dar de alta un agente.
type agentReq struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	BindIP     string `json:"bind_ip"`
	SIPPort    int    `json:"sip_port"`
	Transport  string `json:"transport"`
	FromDomain string `json:"from_domain"`
	AnswerCode int    `json:"answer_code"` // 200 contestar, 486 ocupado, 603 rechazar
}

// handleAgents: GET lista los agentes; POST da de alta uno nuevo y lo arranca.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		s.writeJSON(w, []any{})
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.writeJSON(w, s.manager.Snapshot())
	case http.MethodPost:
		var req agentReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "JSON inválido", http.StatusBadRequest)
			return
		}
		spec := agent.Spec{
			ID:         req.ID,
			Name:       req.Name,
			BindIP:     req.BindIP,
			SIPPort:    req.SIPPort,
			Transport:  req.Transport,
			FromDomain: req.FromDomain,
			UserAgent:  "dimitri-5000",
			AnswerCode: req.AnswerCode,
		}
		if _, err := s.manager.Add(spec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		// Arrancamos el agente recién creado (abre su socket y empieza a servir).
		if err := s.manager.Start(spec.ID); err != nil {
			// Si no arranca, lo retiramos para no dejar un agente "fantasma".
			s.manager.Remove(spec.ID)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		s.writeJSON(w, map[string]string{"id": spec.ID})
	default:
		http.Error(w, "usa GET o POST", http.StatusMethodNotAllowed)
	}
}

// idReq es el cuerpo JSON de las acciones sobre un agente concreto.
type idReq struct {
	ID string `json:"id"`
}

// decodeID lee {"id": "..."} del cuerpo; responde error y devuelve false si falta.
func (s *Server) decodeID(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return "", false
	}
	if s.manager == nil {
		http.Error(w, "gestor de agentes no disponible", http.StatusServiceUnavailable)
		return "", false
	}
	var req idReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "JSON inválido: se requiere 'id'", http.StatusBadRequest)
		return "", false
	}
	return req.ID, true
}

func (s *Server) handleAgentStart(w http.ResponseWriter, r *http.Request) {
	id, ok := s.decodeID(w, r)
	if !ok {
		return
	}
	if err := s.manager.Start(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentStop(w http.ResponseWriter, r *http.Request) {
	id, ok := s.decodeID(w, r)
	if !ok {
		return
	}
	if err := s.manager.Stop(id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAgentRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := s.decodeID(w, r)
	if !ok {
		return
	}
	if !s.manager.Remove(id) {
		http.Error(w, "agente no encontrado", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Llamadas ----------------------------------------------------------------

// callView es una llamada etiquetada con el agente que la gestiona. El embebido
// anónimo hace que los campos de CallRec se serialicen "en plano" junto a agent_id.
type callView struct {
	AgentID string `json:"agent_id"`
	control.CallRec
}

// handleCalls agrega las llamadas de TODOS los agentes en una sola lista.
func (s *Server) handleCalls(w http.ResponseWriter, r *http.Request) {
	out := make([]callView, 0)
	if s.manager != nil {
		for _, info := range s.manager.Snapshot() {
			a := s.manager.Get(info.ID)
			if a == nil {
				continue
			}
			ctrl := a.Control()
			if ctrl == nil {
				continue // agente parado: sin control de llamadas
			}
			for _, rec := range ctrl.Snapshot() {
				out = append(out, callView{AgentID: info.ID, CallRec: rec})
			}
		}
	}
	s.writeJSON(w, out)
}

// placeCallReq es el cuerpo JSON para lanzar una llamada. Admite dos modos:
//   - simple: solo 'to' (URI completa), p. ej. "sip:192.168.1.10:5060".
//   - enriquecido: destino + identidades + PAI + cabeceras (para pruebas por SBC).
type placeCallReq struct {
	AgentID string `json:"agent_id"` // qué agente origina (vacío = "default")
	Hold    int    `json:"hold"`     // segundos a mantener (0 = hasta colgar a mano)

	To string `json:"to"` // modo simple: destino como URI completa

	// Modo enriquecido (todos opcionales; si vienen, mandan sobre 'to').
	DestHost    string            `json:"dest_host"`    // a dónde se envía (SBC o peer)
	DestPort    int               `json:"dest_port"`    // puerto del destino real
	FromUser    string            `json:"from_user"`    // número origen
	FromDomain  string            `json:"from_domain"`  // dominio del From
	FromDisplay string            `json:"from_display"` // nombre visible del llamante
	ToUser      string            `json:"to_user"`      // número destino
	ToDomain    string            `json:"to_domain"`    // dominio del To
	PaiUser     string            `json:"pai_user"`     // P-Asserted-Identity (número)
	Headers     map[string]string `json:"headers"`      // cabeceras arbitrarias
}

// controlFor devuelve el control de llamadas del agente indicado (o "default").
func (s *Server) controlFor(agentID string) *control.Controller {
	if s.manager == nil {
		return nil
	}
	if agentID == "" {
		agentID = "default"
	}
	a := s.manager.Get(agentID)
	if a == nil {
		return nil
	}
	return a.Control()
}

// handlePlaceCall lanza una llamada UAC desde el agente indicado. Construye el
// INVITE a partir de los campos enriquecidos o, si solo llega 'to', parseando la URI.
func (s *Server) handlePlaceCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	var req placeCallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}

	spec, err := buildCallSpec(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctrl := s.controlFor(req.AgentID)
	if ctrl == nil {
		http.Error(w, "agente no disponible o parado", http.StatusServiceUnavailable)
		return
	}
	id := ctrl.PlaceCall(spec)
	s.writeJSON(w, map[string]string{"id": id})
}

// buildCallSpec traduce la petición web a la especificación de llamada. El destino
// real (DestHost:DestPort) sale de los campos enriquecidos o de parsear 'to'.
func buildCallSpec(req placeCallReq) (control.CallSpec, error) {
	inv := sipcore.RichInvite{
		DestHost:    req.DestHost,
		DestPort:    req.DestPort,
		FromUser:    req.FromUser,
		FromDomain:  req.FromDomain,
		FromDisplay: req.FromDisplay,
		ToUser:      req.ToUser,
		ToDomain:    req.ToDomain,
		PAIUser:     req.PaiUser,
		Headers:     req.Headers,
	}

	// Si no se indicó destino explícito, lo tomamos de la URI simple 'to'.
	if inv.DestHost == "" {
		if req.To == "" {
			return control.CallSpec{}, errors.New("indica un destino: 'to' (URI) o 'dest_host'")
		}
		host, port, user, err := sipcore.SplitURI(req.To)
		if err != nil {
			return control.CallSpec{}, err
		}
		inv.DestHost = host
		inv.DestPort = port
		if inv.ToUser == "" {
			inv.ToUser = user // el número del Request-URI, si lo traía
		}
	}
	if inv.DestPort == 0 {
		inv.DestPort = 5060 // puerto SIP por defecto
	}

	// Texto a mostrar en la tabla de llamadas.
	display := req.To
	if display == "" {
		if inv.ToUser != "" {
			display = "sip:" + inv.ToUser + "@" + inv.DestHost
		} else {
			display = "sip:" + inv.DestHost
		}
	}

	return control.CallSpec{Invite: inv, Hold: req.Hold, Display: display}, nil
}

// --- Pruebas de carga (Fase 3) -----------------------------------------------

// loadReq es el cuerpo JSON para arrancar una prueba de carga. El destino sigue el
// MISMO modelo que PLACE CALL (URI simple o valores enriquecidos hacia un SBC).
type loadReq struct {
	AgentID    string  `json:"agent_id"`   // qué agente origina la carga
	Concurrent int     `json:"concurrent"` // N llamadas simultáneas a sostener
	CPS        float64 `json:"cps"`        // ritmo de lanzamiento/reposición (llamadas/seg)
	MaxCalls   int64   `json:"max_calls"`  // tope total de INVITEs (0 = sin tope)
	WithMedia  bool    `json:"with_media"` // enviar RTP en cada llamada

	// Destino (igual que PLACE CALL).
	To         string            `json:"to"`
	DestHost   string            `json:"dest_host"`
	DestPort   int               `json:"dest_port"`
	FromUser   string            `json:"from_user"`
	FromDomain string            `json:"from_domain"`
	ToUser     string            `json:"to_user"`
	ToDomain   string            `json:"to_domain"`
	PaiUser    string            `json:"pai_user"`
	Headers    map[string]string `json:"headers"`
}

// loadView etiqueta las stats de carga con el agente que las origina.
type loadView struct {
	AgentID string `json:"agent_id"`
	load.Stats
}

// handleLoad: GET con las stats de carga de cada agente (running o no).
func (s *Server) handleLoad(w http.ResponseWriter, r *http.Request) {
	out := make([]loadView, 0)
	if s.manager != nil {
		for _, info := range s.manager.Snapshot() {
			a := s.manager.Get(info.ID)
			if a == nil {
				continue
			}
			ctrl := a.Control()
			if ctrl == nil {
				continue
			}
			out = append(out, loadView{AgentID: info.ID, Stats: ctrl.LoadStats()})
		}
	}
	s.writeJSON(w, out)
}

// handleLoadStart arranca una prueba de carga en el agente indicado.
func (s *Server) handleLoadStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req loadReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	ctrl := s.controlFor(req.AgentID)
	if ctrl == nil {
		http.Error(w, "agente no disponible o parado", http.StatusServiceUnavailable)
		return
	}

	// Reutilizamos buildCallSpec para fabricar el INVITE (mismo modelo que PLACE CALL).
	cs, err := buildCallSpec(placeCallReq{
		To: req.To, DestHost: req.DestHost, DestPort: req.DestPort,
		FromUser: req.FromUser, FromDomain: req.FromDomain,
		ToUser: req.ToUser, ToDomain: req.ToDomain, PaiUser: req.PaiUser, Headers: req.Headers,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	spec := load.Spec{
		Invite:     cs.Invite,
		Concurrent: req.Concurrent,
		CPS:        req.CPS,
		MaxCalls:   req.MaxCalls,
		WithMedia:  req.WithMedia,
	}
	if err := ctrl.StartLoad(spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.writeJSON(w, map[string]string{"status": "started"})
}

// handleLoadStop detiene la prueba de carga del agente indicado.
func (s *Server) handleLoadStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	ctrl := s.controlFor(req.AgentID)
	if ctrl == nil {
		http.Error(w, "agente no disponible o parado", http.StatusServiceUnavailable)
		return
	}
	ctrl.StopLoad()
	s.writeJSON(w, map[string]string{"status": "stopping"})
}

// --- Traza SIP (ladder) ------------------------------------------------------

// handleTraceCalls devuelve el resumen de llamadas presentes en la traza.
func (s *Server) handleTraceCalls(w http.ResponseWriter, r *http.Request) {
	if s.trace == nil {
		s.writeJSON(w, []any{})
		return
	}
	s.writeJSON(w, s.trace.Calls())
}

// handleTrace devuelve los eventos: de una llamada (?call_id=) o todos.
func (s *Server) handleTrace(w http.ResponseWriter, r *http.Request) {
	if s.trace == nil {
		s.writeJSON(w, []any{})
		return
	}
	if id := r.URL.Query().Get("call_id"); id != "" {
		s.writeJSON(w, s.trace.ByCallID(id))
		return
	}
	s.writeJSON(w, s.trace.Snapshot())
}

// handleTraceClear vacía la traza.
func (s *Server) handleTraceClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	if s.trace != nil {
		s.trace.Clear()
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Escenarios (Fase 2 en la web) -------------------------------------------

// handleScenarios lista los escenarios disponibles en la carpeta de disco. Si la
// carpeta no existe o no se puede leer, devolvemos lista vacía (no es fatal para
// la web) y lo dejamos anotado en el log.
func (s *Server) handleScenarios(w http.ResponseWriter, r *http.Request) {
	list, err := scenario.List(s.scenariosDir)
	if err != nil {
		s.log.Warn("no se pudo listar la carpeta de escenarios", "dir", s.scenariosDir, "error", err)
		s.writeJSON(w, []any{})
		return
	}
	s.writeJSON(w, list)
}

// scenarioRunReq es el cuerpo JSON para ejecutar un escenario.
type scenarioRunReq struct {
	AgentID string `json:"agent_id"` // qué agente lo ejecuta (vacío = "default")
	File    string `json:"file"`     // nombre del fichero dentro de la carpeta de escenarios
	Target  string `json:"target"`   // destino (URI) para escenarios uac
}

// handleScenarioRun carga el escenario indicado y lo ejecuta en el agente elegido.
func (s *Server) handleScenarioRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "gestor de agentes no disponible", http.StatusServiceUnavailable)
		return
	}
	var req scenarioRunReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	if req.File == "" {
		http.Error(w, "se requiere 'file' (nombre del escenario)", http.StatusBadRequest)
		return
	}

	// Seguridad: usamos SOLO el nombre base del fichero, dentro de la carpeta de
	// escenarios. Así un 'file' con "../" no puede salir de la carpeta (evita leer
	// ficheros arbitrarios del disco a través de la API).
	base := filepath.Base(req.File)
	sc, err := scenario.Load(filepath.Join(s.scenariosDir, base))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Un escenario UAC necesita un destino contra el que llamar.
	if sc.Role == scenario.RoleUAC && req.Target == "" {
		http.Error(w, "un escenario uac requiere 'target' (destino), p. ej. sip:192.168.1.10:5060", http.StatusBadRequest)
		return
	}

	ctrl := s.controlFor(req.AgentID)
	if ctrl == nil {
		http.Error(w, "agente no disponible o parado", http.StatusServiceUnavailable)
		return
	}
	id := ctrl.RunScenario(sc, base, req.Target)
	s.writeJSON(w, map[string]string{"id": id})
}

// scenarioRunView es una ejecución de escenario etiquetada con su agente.
type scenarioRunView struct {
	AgentID string `json:"agent_id"`
	control.ScenarioRec
}

// handleScenarioRuns agrega las ejecuciones de escenario de todos los agentes.
func (s *Server) handleScenarioRuns(w http.ResponseWriter, r *http.Request) {
	out := make([]scenarioRunView, 0)
	if s.manager != nil {
		for _, info := range s.manager.Snapshot() {
			a := s.manager.Get(info.ID)
			if a == nil {
				continue
			}
			ctrl := a.Control()
			if ctrl == nil {
				continue
			}
			for _, rec := range ctrl.ScenariosSnapshot() {
				out = append(out, scenarioRunView{AgentID: info.ID, ScenarioRec: rec})
			}
		}
	}
	s.writeJSON(w, out)
}

// --- Mensajería SIP (MESSAGE) ------------------------------------------------

// messageView es un mensaje etiquetado con el agente que lo gestiona.
type messageView struct {
	AgentID string `json:"agent_id"`
	control.MessageRec
}

// handleMessages agrega los mensajes (enviados y recibidos) de todos los agentes.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	out := make([]messageView, 0)
	if s.manager != nil {
		for _, info := range s.manager.Snapshot() {
			a := s.manager.Get(info.ID)
			if a == nil {
				continue
			}
			ctrl := a.Control()
			if ctrl == nil {
				continue
			}
			for _, m := range ctrl.MessagesSnapshot() {
				out = append(out, messageView{AgentID: info.ID, MessageRec: m})
			}
		}
	}
	s.writeJSON(w, out)
}

// sendMessageReq es el cuerpo JSON para enviar un MESSAGE.
type sendMessageReq struct {
	AgentID     string            `json:"agent_id"`
	To          string            `json:"to"`        // modo simple: URI completa
	DestHost    string            `json:"dest_host"` // modo enriquecido
	DestPort    int               `json:"dest_port"`
	FromUser    string            `json:"from_user"`
	FromDomain  string            `json:"from_domain"`
	FromDisplay string            `json:"from_display"`
	ToUser      string            `json:"to_user"`
	ToDomain    string            `json:"to_domain"`
	Body        string            `json:"body"`
	Headers     map[string]string `json:"headers"`
}

// handleSendMessage envía un MESSAGE desde el agente indicado.
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	var req sendMessageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "JSON inválido", http.StatusBadRequest)
		return
	}
	if req.Body == "" {
		http.Error(w, "el mensaje no puede ir vacío (body)", http.StatusBadRequest)
		return
	}

	spec := sipcore.MessageSpec{
		DestHost:    req.DestHost,
		DestPort:    req.DestPort,
		FromUser:    req.FromUser,
		FromDomain:  req.FromDomain,
		FromDisplay: req.FromDisplay,
		ToUser:      req.ToUser,
		ToDomain:    req.ToDomain,
		Body:        req.Body,
		Headers:     req.Headers,
	}

	// Destino: enriquecido o parseado de la URI simple 'to'.
	if spec.DestHost == "" {
		if req.To == "" {
			http.Error(w, "indica un destino: 'to' (URI) o 'dest_host'", http.StatusBadRequest)
			return
		}
		host, port, user, err := sipcore.SplitURI(req.To)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		spec.DestHost = host
		spec.DestPort = port
		if spec.ToUser == "" {
			spec.ToUser = user
		}
	}
	if spec.DestPort == 0 {
		spec.DestPort = 5060
	}

	display := req.To
	if display == "" {
		if spec.ToUser != "" {
			display = "sip:" + spec.ToUser + "@" + spec.DestHost
		} else {
			display = "sip:" + spec.DestHost
		}
	}

	ctrl := s.controlFor(req.AgentID)
	if ctrl == nil {
		http.Error(w, "agente no disponible o parado", http.StatusServiceUnavailable)
		return
	}
	id := ctrl.SendMessage(spec, display)
	s.writeJSON(w, map[string]string{"id": id})
}

// --- Media (audio que se envía por RTP) --------------------------------------

// audioView es el estado del audio cargado en un agente.
type audioView struct {
	AgentID string  `json:"agent_id"`
	Samples int     `json:"samples"`
	Seconds float64 `json:"seconds"`
}

// handleMedia: GET lista el audio por agente; POST sube un WAV (multipart con
// 'agent_id' y 'file') que se decodifica a PCM 8 kHz mono y se carga en el agente.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		s.writeJSON(w, []any{})
		return
	}
	switch r.Method {
	case http.MethodGet:
		out := make([]audioView, 0)
		for _, info := range s.manager.Snapshot() {
			a := s.manager.Get(info.ID)
			if a == nil {
				continue
			}
			n := a.AudioSamples()
			out = append(out, audioView{AgentID: info.ID, Samples: n, Seconds: secondsOf(n)})
		}
		s.writeJSON(w, out)
	case http.MethodPost:
		if err := r.ParseMultipartForm(32 << 20); err != nil { // hasta 32 MiB en memoria
			http.Error(w, "formulario multipart inválido", http.StatusBadRequest)
			return
		}
		agentID := r.FormValue("agent_id")
		if agentID == "" {
			agentID = "default"
		}
		a := s.manager.Get(agentID)
		if a == nil {
			http.Error(w, "agente no encontrado", http.StatusBadRequest)
			return
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "falta el fichero 'file' (un WAV)", http.StatusBadRequest)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 64<<20)) // límite duro 64 MiB
		if err != nil {
			http.Error(w, "no se pudo leer el fichero", http.StatusBadRequest)
			return
		}
		pcm, err := media.DecodeWAV(data)
		if err != nil {
			http.Error(w, "WAV inválido: "+err.Error(), http.StatusBadRequest)
			return
		}
		a.SetAudio(pcm)
		s.log.Info("audio cargado", "agent", agentID, "file", hdr.Filename, "samples", len(pcm))
		s.writeJSON(w, audioView{AgentID: agentID, Samples: len(pcm), Seconds: secondsOf(len(pcm))})
	default:
		http.Error(w, "usa GET o POST", http.StatusMethodNotAllowed)
	}
}

// handleMediaClear descarta el audio de un agente (vuelve a enviar el tono).
func (s *Server) handleMediaClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "gestor de agentes no disponible", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		AgentID string `json:"agent_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.AgentID == "" {
		req.AgentID = "default"
	}
	a := s.manager.Get(req.AgentID)
	if a == nil {
		http.Error(w, "agente no encontrado", http.StatusNotFound)
		return
	}
	a.ClearAudio()
	w.WriteHeader(http.StatusNoContent)
}

// secondsOf convierte un nº de muestras a segundos a 8 kHz.
func secondsOf(samples int) float64 {
	return float64(samples) / float64(media.SampleRate)
}

// handleTransfer desvía (REFER) una llamada en curso. Busca el id en todos los
// agentes y aplica el desvío en el que la tenga.
func (s *Server) handleTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	if s.manager == nil {
		http.Error(w, "gestor de agentes no disponible", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		ID      string `json:"id"`
		ReferTo string `json:"refer_to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" || req.ReferTo == "" {
		http.Error(w, "JSON inválido: se requieren 'id' y 'refer_to'", http.StatusBadRequest)
		return
	}
	for _, info := range s.manager.Snapshot() {
		a := s.manager.Get(info.ID)
		if a == nil {
			continue
		}
		if ctrl := a.Control(); ctrl != nil && ctrl.Transfer(req.ID, req.ReferTo) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.Error(w, "llamada no encontrada o no establecida", http.StatusNotFound)
}

// handleHangup cuelga una llamada en curso. Busca el id en todos los agentes.
func (s *Server) handleHangup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	id, ok := s.decodeID(w, r)
	if !ok {
		return
	}
	for _, info := range s.manager.Snapshot() {
		a := s.manager.Get(info.ID)
		if a == nil {
			continue
		}
		if ctrl := a.Control(); ctrl != nil && ctrl.Hangup(id) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	http.Error(w, "llamada no encontrada", http.StatusNotFound)
}
