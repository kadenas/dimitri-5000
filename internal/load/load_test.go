package load

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// TestCargaSostieneYDetiene verifica el ciclo completo del motor de carga en
// loopback: un UAS contesta, el generador sube hasta N llamadas concurrentes y las
// SOSTIENE (no se caen solas), y al parar las cuelga todas (Active -> 0).
func TestCargaSostieneYDetiene(t *testing.T) {
	const (
		ip      = "127.0.0.1"
		uasPort = 35082
		uacPort = 35083
		target  = 4 // N concurrentes objetivo
	)

	// --- UAS: contesta y espera el BYE remoto (HoldTime 0) ---
	uas, err := sipcore.New(ip, uasPort, "uas", "", nil)
	if err != nil {
		t.Fatalf("creando UAS: %v", err)
	}
	defer uas.Close()
	uas.SetUASPolicy(sipcore.UASPolicy{RingDelay: 10 * time.Millisecond, AnswerCode: 200})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = uas.Serve(ctx, "udp", ip+":"+strconv.Itoa(uasPort)) }()

	// --- UAC: el Core sobre el que corre la carga (también escucha para el BYE) ---
	uac, err := sipcore.New(ip, uacPort, "uac", "", nil)
	if err != nil {
		t.Fatalf("creando UAC: %v", err)
	}
	defer uac.Close()
	go func() { _ = uac.Serve(ctx, "udp", ip+":"+strconv.Itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond) // que ambos sockets escuchen

	gen := New(uac, nil)
	spec := Spec{
		Invite:     sipcore.RichInvite{DestHost: ip, DestPort: uasPort},
		Concurrent: target,
		CPS:        50,    // sube rápido
		WithMedia:  false, // probamos la mecánica de carga, sin RTP
	}
	if err := gen.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Doble Start debe fallar (una sola ejecución a la vez).
	if err := gen.Start(ctx, spec); err == nil {
		t.Fatal("se esperaba error al arrancar una segunda carga")
	}

	// Esperamos a alcanzar y SOSTENER N concurrentes.
	if !waitFor(2*time.Second, func() bool { return gen.Snapshot().Active == target }) {
		t.Fatalf("no se alcanzó el objetivo de %d concurrentes; stats=%+v", target, gen.Snapshot())
	}

	// Se sostiene: tras una pausa, sigue habiendo N (no se caen solas).
	time.Sleep(300 * time.Millisecond)
	st := gen.Snapshot()
	if st.Active != target {
		t.Fatalf("las llamadas no se sostienen: Active=%d (esperado %d)", st.Active, target)
	}
	if st.Launched < target {
		t.Fatalf("Launched=%d, esperado >= %d", st.Launched, target)
	}

	// STOP: cuelga todas y, al drenar, deja de estar Running.
	gen.Stop()
	if !waitFor(3*time.Second, func() bool { return !gen.Snapshot().Running }) {
		t.Fatalf("la carga no drenó tras STOP; stats=%+v", gen.Snapshot())
	}

	// La foto FINAL se conserva tras el STOP (no se pierde al drenar).
	fin := gen.Snapshot()
	if fin.Active != 0 {
		t.Fatalf("tras STOP Active=%d, esperado 0", fin.Active)
	}
	if fin.Launched < target || fin.Established < target {
		t.Fatalf("la foto final perdió contadores: %+v", fin)
	}
	if fin.FinishedAt == "" || fin.StartedAt == "" {
		t.Fatalf("la foto final debe llevar StartedAt y FinishedAt: %+v", fin)
	}
	if fin.Stopping {
		t.Fatalf("la foto final no debe quedar en Stopping: %+v", fin)
	}

	// PDD: el UAS tarda ~10 ms (RingDelay) en contestar; las muestras deben
	// reflejarlo y mantener la coherencia min <= avg <= max.
	if fin.PDDMinMs <= 0 || fin.PDDAvgMs < fin.PDDMinMs || fin.PDDMaxMs < fin.PDDAvgMs {
		t.Fatalf("PDD incoherente: min=%v avg=%v max=%v", fin.PDDMinMs, fin.PDDAvgMs, fin.PDDMaxMs)
	}
}

// TestCargaRechazoDesglose verifica el desglose de fallos por causa: un UAS que
// responde 486 a todo debe dejar Failed=MaxCalls con FailedBy["486"], sin
// establecidas, sin canceladas y con autofin (los rechazos también agotan el tope).
func TestCargaRechazoDesglose(t *testing.T) {
	const (
		ip       = "127.0.0.1"
		uasPort  = 35088
		uacPort  = 35089
		maxCalls = 3
	)

	uas, err := sipcore.New(ip, uasPort, "uas", "", nil)
	if err != nil {
		t.Fatalf("creando UAS: %v", err)
	}
	defer uas.Close()
	uas.SetUASPolicy(sipcore.UASPolicy{RingDelay: 10 * time.Millisecond, AnswerCode: 486})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = uas.Serve(ctx, "udp", ip+":"+strconv.Itoa(uasPort)) }()

	uac, err := sipcore.New(ip, uacPort, "uac", "", nil)
	if err != nil {
		t.Fatalf("creando UAC: %v", err)
	}
	defer uac.Close()
	go func() { _ = uac.Serve(ctx, "udp", ip+":"+strconv.Itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond)

	gen := New(uac, nil)
	spec := Spec{
		Invite:     sipcore.RichInvite{DestHost: ip, DestPort: uasPort},
		Concurrent: 2,
		CPS:        50,
		MaxCalls:   maxCalls,
		WithMedia:  false,
	}
	if err := gen.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(5*time.Second, func() bool { return !gen.Snapshot().Running }) {
		t.Fatalf("la carga no autofinalizó con rechazos; stats=%+v", gen.Snapshot())
	}

	fin := gen.Snapshot()
	if fin.Failed != maxCalls || fin.Established != 0 || fin.Cancelled != 0 {
		t.Fatalf("contadores inesperados con rechazo: %+v", fin)
	}
	if fin.FailedBy["486"] != maxCalls {
		t.Fatalf("FailedBy[486]=%d, esperado %d (desglose=%v)", fin.FailedBy["486"], maxCalls, fin.FailedBy)
	}
	if fin.PDDMaxMs != 0 {
		t.Fatalf("sin llamadas contestadas no debe haber PDD: %+v", fin)
	}
}

// TestCargaStopCancelaEnVuelo verifica que las llamadas aún sin contestar cuando
// llega el STOP se cuentan como Cancelled y NO como Failed: el sistema bajo
// prueba no falló, fuimos nosotros quienes abortamos.
func TestCargaStopCancelaEnVuelo(t *testing.T) {
	const (
		ip      = "127.0.0.1"
		uasPort = 35092
		uacPort = 35093
	)

	uas, err := sipcore.New(ip, uasPort, "uas", "", nil)
	if err != nil {
		t.Fatalf("creando UAS: %v", err)
	}
	defer uas.Close()
	// Ring larguísimo: la llamada seguirá en vuelo cuando ordenemos el STOP.
	uas.SetUASPolicy(sipcore.UASPolicy{RingDelay: 30 * time.Second, AnswerCode: 200})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = uas.Serve(ctx, "udp", ip+":"+strconv.Itoa(uasPort)) }()

	uac, err := sipcore.New(ip, uacPort, "uac", "", nil)
	if err != nil {
		t.Fatalf("creando UAC: %v", err)
	}
	defer uac.Close()
	go func() { _ = uac.Serve(ctx, "udp", ip+":"+strconv.Itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond)

	gen := New(uac, nil)
	spec := Spec{
		Invite:     sipcore.RichInvite{DestHost: ip, DestPort: uasPort},
		Concurrent: 2,
		CPS:        50,
		WithMedia:  false,
	}
	if err := gen.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !waitFor(2*time.Second, func() bool { return gen.Snapshot().Pending == 2 }) {
		t.Fatalf("no hay llamadas en vuelo; stats=%+v", gen.Snapshot())
	}

	gen.Stop()
	if !waitFor(3*time.Second, func() bool { return !gen.Snapshot().Running }) {
		t.Fatalf("la carga no drenó tras STOP; stats=%+v", gen.Snapshot())
	}

	fin := gen.Snapshot()
	if fin.Cancelled != 2 {
		t.Fatalf("Cancelled=%d, esperado 2 (las dos en vuelo): %+v", fin.Cancelled, fin)
	}
	if fin.Failed != 0 || len(fin.FailedBy) != 0 {
		t.Fatalf("el STOP no debe contar como fallo: %+v", fin)
	}
}

// TestCargaMaxCallsAutofin verifica que, con un tope MaxCalls y un UAS que cuelga
// él mismo, la prueba TERMINA SOLA (sin STOP): exactamente MaxCalls lanzadas (el
// contador vive en el loop, sin carrera que sobrepase el tope), todas terminadas,
// y la foto final disponible en Snapshot.
func TestCargaMaxCallsAutofin(t *testing.T) {
	const (
		ip       = "127.0.0.1"
		uasPort  = 35086
		uacPort  = 35087
		maxCalls = 3
	)

	uas, err := sipcore.New(ip, uasPort, "uas", "", nil)
	if err != nil {
		t.Fatalf("creando UAS: %v", err)
	}
	defer uas.Close()
	// El UAS cuelga cada llamada a los 150 ms: así hay reposición y final natural.
	uas.SetUASPolicy(sipcore.UASPolicy{RingDelay: 10 * time.Millisecond, AnswerCode: 200, HoldTime: 150 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = uas.Serve(ctx, "udp", ip+":"+strconv.Itoa(uasPort)) }()

	uac, err := sipcore.New(ip, uacPort, "uac", "", nil)
	if err != nil {
		t.Fatalf("creando UAC: %v", err)
	}
	defer uac.Close()
	go func() { _ = uac.Serve(ctx, "udp", ip+":"+strconv.Itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond)

	gen := New(uac, nil)
	spec := Spec{
		Invite:     sipcore.RichInvite{DestHost: ip, DestPort: uasPort},
		Concurrent: 2,
		CPS:        50,
		MaxCalls:   maxCalls,
		WithMedia:  false,
	}
	if err := gen.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Sin llamar a Stop: debe acabar sola cuando el UAS cuelgue la última.
	if !waitFor(5*time.Second, func() bool { return !gen.Snapshot().Running }) {
		t.Fatalf("la carga no autofinalizó con MaxCalls; stats=%+v", gen.Snapshot())
	}

	fin := gen.Snapshot()
	if fin.Launched != maxCalls {
		t.Fatalf("Launched=%d, esperado exactamente %d (tope respetado)", fin.Launched, maxCalls)
	}
	if fin.Ended != maxCalls || fin.Active != 0 || fin.Pending != 0 {
		t.Fatalf("foto final incoherente: %+v", fin)
	}
	if fin.FinishedAt == "" {
		t.Fatalf("la foto final debe llevar FinishedAt: %+v", fin)
	}

	// El hueco queda libre: se puede arrancar otra prueba inmediatamente.
	if err := gen.Start(ctx, spec); err != nil {
		t.Fatalf("Start tras autofin: %v", err)
	}
	gen.Stop()
}

// TestCargaConEscenario verifica que el motor de carga puede establecer cada
// llamada ejecutando un ESCENARIO UAC (en vez del INVITE básico): sube hasta N
// concurrentes y las sostiene, ignorando las pausas y el BYE del escenario (la
// duración la manda la carga). Al parar, las cuelga todas.
func TestCargaConEscenario(t *testing.T) {
	const (
		ip      = "127.0.0.1"
		uasPort = 35084
		uacPort = 35085
		target  = 3
	)

	uas, err := sipcore.New(ip, uasPort, "uas", "", nil)
	if err != nil {
		t.Fatalf("creando UAS: %v", err)
	}
	defer uas.Close()
	uas.SetUASPolicy(sipcore.UASPolicy{RingDelay: 10 * time.Millisecond, AnswerCode: 200})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = uas.Serve(ctx, "udp", ip+":"+strconv.Itoa(uasPort)) }()

	uac, err := sipcore.New(ip, uacPort, "uac", "", nil)
	if err != nil {
		t.Fatalf("creando UAC: %v", err)
	}
	defer uac.Close()
	go func() { _ = uac.Serve(ctx, "udp", ip+":"+strconv.Itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond)

	pause := scenario.Duration(time.Hour) // si NO se ignorara, la llamada nunca colgaría sola
	sc := &scenario.Scenario{
		Name: "carga-escenario",
		Role: scenario.RoleUAC,
		Steps: []scenario.Step{
			{Send: "INVITE", Headers: map[string]string{
				"From": "<sip:load@dimitri>",
				"To":   "<sip:uas@dimitri>",
			}},
			{Recv: "200"},
			{Send: "ACK"},
			{Pause: &pause}, // debe ignorarse en carga
			{Send: "BYE"},   // debe ignorarse en carga (lo cuelga el motor)
		},
	}
	if err := sc.Validate(); err != nil {
		t.Fatalf("escenario inválido: %v", err)
	}

	gen := New(uac, nil)
	spec := Spec{
		Invite:     sipcore.RichInvite{DestHost: ip, DestPort: uasPort},
		Concurrent: target,
		CPS:        50,
		WithMedia:  false,
		Scenario:   sc,
	}
	if err := gen.Start(ctx, spec); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if !waitFor(2*time.Second, func() bool { return gen.Snapshot().Active == target }) {
		t.Fatalf("no se alcanzó el objetivo de %d con escenario; stats=%+v", target, gen.Snapshot())
	}
	if got := gen.Snapshot().Scenario; got != "carga-escenario" {
		t.Fatalf("Stats.Scenario=%q, esperado 'carga-escenario'", got)
	}
	// Se sostienen pese al BYE del escenario (que se ignora en carga).
	time.Sleep(300 * time.Millisecond)
	if st := gen.Snapshot(); st.Active != target {
		t.Fatalf("las llamadas no se sostienen con escenario: Active=%d (esperado %d)", st.Active, target)
	}

	gen.Stop()
	if !waitFor(3*time.Second, func() bool { return !gen.Snapshot().Running }) {
		t.Fatalf("la carga con escenario no drenó tras STOP; stats=%+v", gen.Snapshot())
	}
	if got := gen.Snapshot().Active; got != 0 {
		t.Fatalf("tras STOP Active=%d, esperado 0", got)
	}
}

// waitFor sondea cond cada 20 ms hasta que sea true o venza el plazo.
func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return cond()
}
