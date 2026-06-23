// Comando dimitri-5000: arranca el faro SIP y su interfaz web local.
//
// Responsabilidad de ESTE fichero (capa de arranque): leer la configuración,
// montar las piezas (núcleo SIP -> faro -> interfaz web) y orquestar el
// arranque y la parada limpia. No contiene lógica de SIP ni de negocio: solo
// "cablea" los módulos. Mantenerlo así de fino es justo lo que evita el
// monolito que tuvo el proyecto anterior.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kadenas/dimitri-5000/internal/config"
	"github.com/kadenas/dimitri-5000/internal/monitor"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
	"github.com/kadenas/dimitri-5000/internal/webui"
)

func main() {
	// --- 1. Flags de línea de comandos -------------------------------------
	// Solo lo imprescindible para la v1; el resto vive en el fichero de config.
	cfgPath := flag.String("config", "", "Ruta a un fichero de configuración JSON (opcional)")
	webAddr := flag.String("web", "127.0.0.1:8080", "Dirección de la interfaz web local")
	bindIP := flag.String("bind-ip", "", "IP local de origen para el tráfico SIP (vacío = usa la del config)")
	flag.Parse()

	// Logger estructurado de la librería estándar (slog). Lo usamos en todo el
	// proyecto para tener logs consistentes y fáciles de filtrar.
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// --- 2. Cargar configuración -------------------------------------------
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("no se pudo cargar la configuración", "error", err)
		os.Exit(1)
	}
	// El flag, si se indica, tiene prioridad sobre el fichero.
	if *bindIP != "" {
		cfg.BindIP = *bindIP
	}

	// --- 3. Núcleo SIP (única capa que habla con sipgo) --------------------
	core, err := sipcore.New(cfg.BindIP, cfg.SIPPort, "dimitri-5000", log)
	if err != nil {
		log.Error("no se pudo inicializar el núcleo SIP", "error", err)
		os.Exit(1)
	}
	defer core.Close()

	// --- 4. El faro: vigila las troncales con OPTIONS periódicos -----------
	farol := monitor.New(core, cfg.Targets, cfg.Monitor, log)

	// Contexto raíz que se cancela con Ctrl+C o SIGTERM: así TODAS las
	// goroutines del faro y el servidor web paran de forma ordenada.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	farol.Start(ctx)
	log.Info("faro iniciado", "troncales", len(cfg.Targets), "bind_ip", cfg.BindIP)

	// --- 5. Interfaz web local ---------------------------------------------
	srv := webui.New(*webAddr, farol, log)
	log.Info("interfaz web disponible", "url", "http://"+*webAddr)
	if err := srv.Run(ctx); err != nil {
		log.Error("la interfaz web terminó con error", "error", err)
	}

	log.Info("dimitri-5000 detenido")
}
