package load

import (
	"context"
	"strconv"
	"testing"
	"time"

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
