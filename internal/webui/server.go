// Paquete webui: sirve la interfaz web local y la API que la alimenta. La
// interfaz NO conoce SIP; solo le pide al faro su estado y lo pinta. Esta
// separación es deliberada: el motor podría funcionar sin web, y la web podría
// cambiarse entera sin tocar el motor.
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
	monitor *monitor.Monitor    // puede ser nil (modo web sin faro)
	control *control.Controller // puede ser nil (modo monitor sin control)
	log     *slog.Logger
}

// New crea el servidor web (no lo arranca todavía). monitor y control son opcionales:
// el modo monitor pasa control=nil; el modo web pasa ambos.
func New(addr string, m *monitor.Monitor, ctrl *control.Controller, log *slog.Logger) *Server {
	return &Server{addr: addr, monitor: m, control: ctrl, log: log}
}

// Run arranca el servidor HTTP y lo detiene limpiamente cuando ctx se cancela.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// API: estado actual de las troncales en JSON.
	mux.HandleFunc("/api/status", s.handleStatus)
	// API de control de llamadas.
	mux.HandleFunc("/api/calls", s.handleCalls)        // GET: estado de las llamadas
	mux.HandleFunc("/api/call", s.handlePlaceCall)     // POST: lanzar una llamada
	mux.HandleFunc("/api/call/hangup", s.handleHangup) // POST: colgar una llamada

	// Ficheros estáticos (index.html, css, js) servidos desde el embed.
	// fs.Sub quita el prefijo "static" para que "/" sirva static/index.html.
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return err
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

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

// handleStatus devuelve el snapshot del faro como JSON. Si no hay faro (modo web),
// devuelve una lista vacía para que la web no falle.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if s.monitor == nil {
		_, _ = w.Write([]byte("[]"))
		return
	}
	if err := json.NewEncoder(w).Encode(s.monitor.Snapshot()); err != nil {
		s.log.Error("no se pudo serializar el estado", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
	}
}

// handleCalls devuelve el estado de las llamadas gestionadas por el controlador.
func (s *Server) handleCalls(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if s.control == nil {
		_, _ = w.Write([]byte("[]"))
		return
	}
	if err := json.NewEncoder(w).Encode(s.control.Snapshot()); err != nil {
		s.log.Error("no se pudo serializar las llamadas", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
	}
}

// placeCallReq es el cuerpo JSON para lanzar una llamada.
type placeCallReq struct {
	To   string `json:"to"`   // destino, p. ej. "sip:192.168.1.10:5060"
	Hold int    `json:"hold"` // segundos a mantener la llamada (0 = hasta colgar a mano)
}

// handlePlaceCall lanza una llamada UAC desde la web.
func (s *Server) handlePlaceCall(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	if s.control == nil {
		http.Error(w, "control de llamadas no disponible (arranca en --mode web)", http.StatusServiceUnavailable)
		return
	}
	var req placeCallReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.To == "" {
		http.Error(w, "JSON inválido: se requiere 'to'", http.StatusBadRequest)
		return
	}
	id := s.control.PlaceCall(req.To, req.Hold)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

// handleHangup cuelga una llamada en curso.
func (s *Server) handleHangup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "usa POST", http.StatusMethodNotAllowed)
		return
	}
	if s.control == nil {
		http.Error(w, "control no disponible", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
		http.Error(w, "JSON inválido: se requiere 'id'", http.StatusBadRequest)
		return
	}
	if !s.control.Hangup(req.ID) {
		http.Error(w, "llamada no encontrada", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
