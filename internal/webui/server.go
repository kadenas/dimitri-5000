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
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/kadenas/dimitri-5000/internal/agent"
	"github.com/kadenas/dimitri-5000/internal/control"
	"github.com/kadenas/dimitri-5000/internal/monitor"
)

// go:embed mete los ficheros de la carpeta static/ DENTRO del binario, para que
// dimitri-5000 sea un único ejecutable sin ficheros sueltos que copiar. Es la
// pieza que hace posible "copiar un binario y ejecutar" en una máquina aislada.
//
//go:embed static
var staticFiles embed.FS

// Server es la interfaz web local.
type Server struct {
	addr    string
	monitor *monitor.Monitor // puede ser nil (modo sin faro)
	manager *agent.Manager   // puede ser nil (modo monitor sin agentes)
	log     *slog.Logger
}

// New crea el servidor web (no lo arranca todavía). monitor y manager son
// opcionales: el modo monitor pasa manager=nil; el modo web pasa ambos.
func New(addr string, m *monitor.Monitor, mgr *agent.Manager, log *slog.Logger) *Server {
	return &Server{addr: addr, monitor: m, manager: mgr, log: log}
}

// Run arranca el servidor HTTP y lo detiene limpiamente cuando ctx se cancela.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// API: estado de troncales (faro).
	mux.HandleFunc("/api/status", s.handleStatus)

	// API de agentes (instancias SIP).
	mux.HandleFunc("/api/agents", s.handleAgents)             // GET lista | POST alta
	mux.HandleFunc("/api/agents/start", s.handleAgentStart)   // POST {id}
	mux.HandleFunc("/api/agents/stop", s.handleAgentStop)     // POST {id}
	mux.HandleFunc("/api/agents/remove", s.handleAgentRemove) // POST {id}

	// API de control de llamadas (por agente).
	mux.HandleFunc("/api/calls", s.handleCalls)        // GET: estado de las llamadas
	mux.HandleFunc("/api/call", s.handlePlaceCall)     // POST: lanzar una llamada
	mux.HandleFunc("/api/call/hangup", s.handleHangup) // POST: colgar una llamada

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

// placeCallReq es el cuerpo JSON para lanzar una llamada.
type placeCallReq struct {
	AgentID string `json:"agent_id"` // qué agente origina (vacío = "default")
	To      string `json:"to"`       // destino, p. ej. "sip:192.168.1.10:5060"
	Hold    int    `json:"hold"`     // segundos a mantener la llamada (0 = hasta colgar a mano)
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

// handlePlaceCall lanza una llamada UAC desde el agente indicado.
func (s *Server) handlePlaceCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	var req placeCallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		http.Error(w, "JSON inválido: se requiere 'to'", http.StatusBadRequest)
		return
	}
	ctrl := s.controlFor(req.AgentID)
	if ctrl == nil {
		http.Error(w, "agente no disponible o parado", http.StatusServiceUnavailable)
		return
	}
	id := ctrl.PlaceCall(req.To, req.Hold)
	s.writeJSON(w, map[string]string{"id": id})
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
