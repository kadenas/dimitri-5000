package runner

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// TestUASScriptTiming verifica el caso pedido por el usuario: cuando llega la
// llamada a B, B espera un tiempo y SÓLO ENTONCES responde 180, y más tarde 200.
// Comprueba el ORDEN y la TEMPORIZACIÓN observados por el UAC, y que el guion
// espera el BYE (recv BYE) y la llamada termina limpia.
func TestUASScriptTiming(t *testing.T) {
	const (
		ip      = "127.0.0.1"
		uasPort = 35180 // rango propio: el test deja los servidores vivos (no cierra para
		uacPort = 35181 // evitar el race de shutdown de sipgo), así que no debe colisionar
	)
	const ring = 300 * time.Millisecond
	const toAnswer = 250 * time.Millisecond

	// Escenario uas: recibir INVITE -> esperar -> 180 -> esperar -> 200 -> ACK -> BYE.
	p1 := scenario.Duration(ring)
	p2 := scenario.Duration(toAnswer)
	sc := &scenario.Scenario{
		Name: "uas-lento",
		Role: scenario.RoleUAS,
		Steps: []scenario.Step{
			{Recv: "INVITE"},
			{Pause: &p1},
			{Send: "180"},
			{Pause: &p2},
			{Send: "200"},
			{Recv: "ACK"},
			{Recv: "BYE"},
		},
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("escenario inválido: %v", err)
	}
	pol, err := BuildUASPolicy(sc)
	if err != nil {
		t.Fatalf("BuildUASPolicy: %v", err)
	}
	if len(pol.Script) != 5 { // pause,180,pause,200,waitBye (los recv INVITE/ACK no generan paso)
		t.Fatalf("guion con %d pasos, esperado 5: %+v", len(pol.Script), pol.Script)
	}

	uas, err := sipcore.New(ip, uasPort, "uas", "", nil)
	if err != nil {
		t.Fatalf("creando UAS: %v", err)
	}
	uas.SetUASPolicy(pol)

	// Servimos con un contexto que NO se cancela durante el test: sipgo tiene un race
	// conocido al cerrar el listener cuando se cancela el ctx de ListenAndServe (lo
	// dispara también el test de loopback de sipcore). Aquí solo nos interesa la
	// lógica del guion UAS; los sockets se liberan al terminar el proceso de test.
	serveCtx := context.Background()
	go func() { _ = uas.Serve(serveCtx, "udp", ip+":"+strconv.Itoa(uasPort)) }()

	uac, err := sipcore.New(ip, uacPort, "uac", "", nil)
	if err != nil {
		t.Fatalf("creando UAC: %v", err)
	}
	go func() { _ = uac.Serve(serveCtx, "udp", ip+":"+strconv.Itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond)

	// Contexto acotado para las operaciones de la llamada (no para los servidores).
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	call, err := uac.DialURIWithOptions(ctx, "sip:"+ip+":"+strconv.Itoa(uasPort), sipcore.CallOptions{
		Headers: map[string]string{
			"From": "<sip:a@dimitri>;tag=abc123",
			"To":   "<sip:b@dimitri>",
		},
	})
	if err != nil {
		t.Fatalf("DialURIWithOptions: %v", err)
	}

	start := time.Now()
	var mu sync.Mutex
	seen := map[int]time.Duration{}
	err = call.WaitAnswerObserved(ctx, func(code int, reason string) {
		mu.Lock()
		if _, ok := seen[code]; !ok {
			seen[code] = time.Since(start)
		}
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("la llamada no fue contestada: %v", err)
	}

	mu.Lock()
	t180, ok180 := seen[180]
	t200, ok200 := seen[200]
	mu.Unlock()

	if !ok180 {
		t.Fatalf("no se observó el 180 Ringing")
	}
	if !ok200 {
		t.Fatalf("no se observó el 200 OK")
	}
	// El 180 NO debe llegar antes de la pausa inicial (con holgura por jitter de red local).
	if t180 < ring-100*time.Millisecond {
		t.Fatalf("el 180 llegó demasiado pronto (%v), debía esperar ~%v", t180, ring)
	}
	// El 200 llega tras el 180 + su pausa.
	if t200 < t180 {
		t.Fatalf("el 200 (%v) llegó antes que el 180 (%v)", t200, t180)
	}
	if t200 < ring+toAnswer-150*time.Millisecond {
		t.Fatalf("el 200 llegó demasiado pronto (%v), debía esperar ~%v", t200, ring+toAnswer)
	}

	// ACK y luego colgamos: el guion (recv BYE) debe desbloquearse y la llamada acabar.
	if err := call.Ack(ctx); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if err := call.Hangup(ctx); err != nil {
		t.Fatalf("Hangup (BYE): %v", err)
	}
}
