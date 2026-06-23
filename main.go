// Comando dimitri-5000: herramienta de pruebas SIP. Puede arrancar en tres modos:
//
//   - monitor: el "faro" original, vigila troncales con OPTIONS y muestra una web.
//   - uas:     actúa como User Agent Server (recibe llamadas y las contesta).
//   - uac:     actúa como User Agent Client (lanza una llamada a un destino).
//
// Responsabilidad de ESTE fichero (capa de arranque): leer la configuración y los
// flags, resolver la IP/puerto de señalización, montar las piezas y orquestar el
// arranque y la parada limpia. No contiene lógica SIP: solo "cablea" los módulos.
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kadenas/dimitri-5000/internal/config"
	"github.com/kadenas/dimitri-5000/internal/monitor"
	"github.com/kadenas/dimitri-5000/internal/netutil"
	"github.com/kadenas/dimitri-5000/internal/runner"
	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
	"github.com/kadenas/dimitri-5000/internal/webui"
)

func main() {
	// --- 1. Flags de línea de comandos -------------------------------------
	mode := flag.String("mode", "monitor", "Modo de ejecución: monitor | uas | uac | scenario")
	scenarioFile := flag.String("file", "", "Ruta a un escenario YAML (modo scenario)")
	cfgPath := flag.String("config", "", "Ruta a un fichero de configuración JSON (opcional)")
	webAddr := flag.String("web", "127.0.0.1:8080", "Dirección de la interfaz web local (modos monitor/uas)")

	// Señalización SIP (común a uas/uac). Si quedan vacíos/0, se toman del config
	// y, en última instancia, de la autodetección.
	bindIP := flag.String("bind-ip", "", "IP local para la señalización SIP (vacío = autodetectar la de la tarjeta de red)")
	sipPort := flag.Int("sip-port", 0, "Puerto SIP local (0 = usar config; por defecto 5060)")
	transport := flag.String("transport", "", "Transporte SIP: udp | tcp (vacío = usar config; por defecto udp)")

	// Específicos del modo uac (lanzar llamada).
	to := flag.String("to", "", "Destino de la llamada en modo uac, p. ej.: sip:192.168.1.10:5060")
	hold := flag.Duration("hold", 5*time.Second, "Tiempo que se mantiene la llamada establecida antes de colgar (uac/uas)")

	// Específicos del modo uas (recibir llamada).
	answerCode := flag.Int("answer-code", 200, "Código de respuesta del UAS (200=contestar, 486=ocupado, 603=rechazar)")
	ringDelay := flag.Duration("ring-delay", 1*time.Second, "Tiempo de 'Ringing' antes de la respuesta final (uas)")

	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// --- 2. Cargar configuración -------------------------------------------
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("no se pudo cargar la configuración", "error", err)
		os.Exit(1)
	}

	// --- 3. Resolver IP, puerto y transporte de señalización ---------------
	// Prioridad: flag > config > valor por defecto/autodetección.
	resolvedIP := resolveBindIP(*bindIP, cfg.BindIP, log)
	resolvedPort := cfg.SIPPort
	if *sipPort != 0 {
		resolvedPort = *sipPort
	}
	if resolvedPort == 0 {
		resolvedPort = 5060
	}
	resolvedTransport := strings.ToLower(cfg.Transport)
	if *transport != "" {
		resolvedTransport = strings.ToLower(*transport)
	}
	if resolvedTransport != "tcp" {
		resolvedTransport = "udp" // por defecto y para cualquier valor no reconocido
	}

	log.Info("señalización SIP",
		"bind_ip", resolvedIP,
		"sip_port", resolvedPort,
		"transport", resolvedTransport,
	)

	// --- 4. Núcleo SIP (única capa que habla con sipgo) --------------------
	core, err := sipcore.New(resolvedIP, resolvedPort, "dimitri-5000", log)
	if err != nil {
		log.Error("no se pudo inicializar el núcleo SIP", "error", err)
		os.Exit(1)
	}
	defer core.Close()

	// Contexto raíz que se cancela con Ctrl+C / SIGTERM: para TODO de forma ordenada.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// --- 5. Despachar según el modo ----------------------------------------
	switch *mode {
	case "monitor":
		runMonitor(ctx, core, cfg, *webAddr, log)
	case "uas":
		runUAS(ctx, core, resolvedIP, resolvedPort, resolvedTransport, *answerCode, *ringDelay, *hold, log)
	case "uac":
		runUAC(ctx, core, resolvedIP, resolvedPort, resolvedTransport, *to, *hold, log)
	case "scenario":
		runScenario(ctx, core, resolvedIP, resolvedPort, resolvedTransport, *scenarioFile, *to, log)
	default:
		log.Error("modo desconocido", "mode", *mode, "válidos", "monitor|uas|uac")
		os.Exit(2)
	}

	log.Info("dimitri-5000 detenido")
}

// resolveBindIP decide la IP local de señalización. Si flagIP o cfgIP traen un
// valor, se respeta. Si ambos están vacíos, autodetecta la IP de la tarjeta de
// red y, de paso, registra todas las IPs disponibles para que el usuario las vea.
func resolveBindIP(flagIP, cfgIP string, log *slog.Logger) string {
	if flagIP != "" {
		return flagIP
	}
	if cfgIP != "" {
		return cfgIP
	}

	// Autodetección.
	if disponibles := netutil.ListIPv4(); len(disponibles) > 0 {
		log.Info("IPs detectadas en el equipo", "direcciones", strings.Join(disponibles, ", "))
	}
	ip, err := netutil.LocalIP()
	if err != nil {
		log.Warn("no se pudo autodetectar la IP; usando 127.0.0.1", "error", err)
		return "127.0.0.1"
	}
	log.Info("IP autodetectada para señalización", "bind_ip", ip)
	return ip
}

// runMonitor arranca el faro de OPTIONS y la interfaz web (comportamiento original).
func runMonitor(ctx context.Context, core *sipcore.Core, cfg config.Config, webAddr string, log *slog.Logger) {
	farol := monitor.New(core, cfg.Targets, cfg.Monitor, log)
	farol.Start(ctx)
	log.Info("faro iniciado", "troncales", len(cfg.Targets))

	srv := webui.New(webAddr, farol, log)
	log.Info("interfaz web disponible", "url", "http://"+webAddr)
	if err := srv.Run(ctx); err != nil {
		log.Error("la interfaz web terminó con error", "error", err)
	}
}

// runUAS arranca el servidor de llamadas: escucha y contesta según la política.
func runUAS(ctx context.Context, core *sipcore.Core, ip string, port int, transport string, answerCode int, ringDelay, hold time.Duration, log *slog.Logger) {
	core.SetUASPolicy(sipcore.UASPolicy{
		RingDelay:  ringDelay,
		AnswerCode: answerCode,
		HoldTime:   hold, // si >0, el UAS cuelga tras este tiempo; si 0, espera el BYE remoto
	})

	addr := joinHostPort(ip, port)
	log.Info("modo UAS: esperando llamadas", "addr", addr, "transport", transport, "answer_code", answerCode)

	// Serve bloquea hasta que se cancela el contexto (Ctrl+C).
	if err := core.Serve(ctx, transport, addr); err != nil && ctx.Err() == nil {
		log.Error("el servidor SIP terminó con error", "error", err)
	}
}

// runUAC lanza UNA llamada al destino indicado, la mantiene 'hold' y cuelga.
// Necesita también escuchar (Serve en segundo plano) para recibir las peticiones
// dentro del diálogo (p. ej. un BYE del otro extremo).
func runUAC(ctx context.Context, core *sipcore.Core, ip string, port int, transport, to string, hold time.Duration, log *slog.Logger) {
	if to == "" {
		log.Error("modo uac requiere --to (destino), p. ej.: --to sip:192.168.1.10:5060")
		os.Exit(2)
	}

	// Servidor en segundo plano para el tráfico dentro del diálogo.
	addr := joinHostPort(ip, port)
	go func() {
		if err := core.Serve(ctx, transport, addr); err != nil && ctx.Err() == nil {
			log.Error("servidor SIP (uac) terminó con error", "error", err)
		}
	}()
	// Pequeña espera a que el socket esté escuchando antes de llamar.
	time.Sleep(200 * time.Millisecond)

	// Para TCP añadimos el parámetro de transporte al destino si no lo trae ya.
	target := to
	if transport == "tcp" && !strings.Contains(strings.ToLower(target), "transport=") {
		target += ";transport=tcp"
	}

	log.Info("modo UAC: lanzando llamada", "to", target, "from", addr)

	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	call, err := core.DialURI(callCtx, target, nil)
	if err != nil {
		log.Error("no se pudo enviar el INVITE", "error", err)
		return
	}

	if err := call.WaitAnswer(callCtx); err != nil {
		log.Error("la llamada no fue contestada", "error", err)
		return
	}
	if err := call.Ack(callCtx); err != nil {
		log.Error("error enviando ACK", "error", err)
		return
	}
	log.Info("llamada establecida", "hold", hold)

	// Mantenemos la llamada el tiempo indicado o hasta Ctrl+C.
	select {
	case <-time.After(hold):
	case <-ctx.Done():
	}

	byeCtx, byeCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer byeCancel()
	if err := call.Hangup(byeCtx); err != nil {
		log.Error("error enviando BYE", "error", err)
		return
	}
	log.Info("llamada finalizada correctamente")
}

// runScenario carga un escenario YAML y lo ejecuta con el runner. Necesita el
// servidor en segundo plano para el tráfico dentro del diálogo (igual que uac).
func runScenario(ctx context.Context, core *sipcore.Core, ip string, port int, transport, file, to string, log *slog.Logger) {
	if file == "" {
		log.Error("modo scenario requiere --file <escenario.yaml>")
		os.Exit(2)
	}

	sc, err := scenario.Load(file)
	if err != nil {
		log.Error("no se pudo cargar el escenario", "error", err)
		os.Exit(1)
	}

	if sc.Role == scenario.RoleUAC && to == "" {
		log.Error("un escenario uac requiere --to (destino), p. ej.: --to sip:192.168.1.10:5060")
		os.Exit(2)
	}

	// Servidor en segundo plano (peticiones dentro del diálogo: BYE, etc.).
	addr := joinHostPort(ip, port)
	go func() {
		if err := core.Serve(ctx, transport, addr); err != nil && ctx.Err() == nil {
			log.Error("servidor SIP (scenario) terminó con error", "error", err)
		}
	}()
	time.Sleep(200 * time.Millisecond)

	target := to
	if transport == "tcp" && target != "" && !strings.Contains(strings.ToLower(target), "transport=") {
		target += ";transport=tcp"
	}

	r := runner.New(core, target, log)
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := r.Run(runCtx, sc); err != nil {
		log.Error("la ejecución del escenario falló", "error", err)
		return
	}
}

// joinHostPort une IP y puerto en "ip:puerto" (formato que esperan Serve y sipgo).
func joinHostPort(ip string, port int) string {
	return ip + ":" + strconv.Itoa(port)
}
