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
	monitor *monitor.Monitor
	log     *slog.Logger
}

// New crea el servidor web (no lo arranca todavía).
func New(addr string, m *monitor.Monitor, log *slog.Logger) *Server {
	return &Server{addr: addr, monitor: m, log: log}
}

// Run arranca el servidor HTTP y lo detiene limpiamente cuando ctx se cancela.
func (s *Server) Run(ctx context.Context) error {
	mux := http.NewServeMux()

	// API: estado actual de las troncales en JSON.
	mux.HandleFunc("/api/status", s.handleStatus)

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

// handleStatus devuelve el snapshot del faro como JSON.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	snapshot := s.monitor.Snapshot()
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		s.log.Error("no se pudo serializar el estado", "error", err)
		http.Error(w, "error interno", http.StatusInternalServerError)
	}
}
