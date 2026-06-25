package media

import (
	"context"
	"testing"
	"time"
)

// TestSessionLoopback abre dos sesiones en loopback apuntándose mutuamente y
// comprueba que se envían y reciben RTP, sin pérdida apreciable. Es la prueba de
// extremo a extremo del plano de media (socket + cabecera RTP + códec + métricas).
func TestSessionLoopback(t *testing.T) {
	a, err := Open("127.0.0.1", nil)
	if err != nil {
		t.Fatalf("Open(a): %v", err)
	}
	defer a.Close()
	b, err := Open("127.0.0.1", nil)
	if err != nil {
		t.Fatalf("Open(b): %v", err)
	}
	defer b.Close()

	if a.LocalPort() == 0 || b.LocalPort() == 0 {
		t.Fatal("el socket RTP no obtuvo puerto local")
	}

	ctx := context.Background()
	if err := a.Start(ctx, "127.0.0.1", b.LocalPort(), PayloadPCMU, 20); err != nil {
		t.Fatalf("Start(a): %v", err)
	}
	if err := b.Start(ctx, "127.0.0.1", a.LocalPort(), PayloadPCMU, 20); err != nil {
		t.Fatalf("Start(b): %v", err)
	}

	// ~12 tramas a 20 ms; margen de sobra para que crucen varias.
	time.Sleep(260 * time.Millisecond)

	ma := a.Metrics()
	mb := b.Metrics()

	if ma.TxPackets < 5 || mb.TxPackets < 5 {
		t.Fatalf("pocos paquetes enviados (a=%d b=%d)", ma.TxPackets, mb.TxPackets)
	}
	if ma.RxPackets < 3 || mb.RxPackets < 3 {
		t.Fatalf("pocos paquetes recibidos (a=%d b=%d)", ma.RxPackets, mb.RxPackets)
	}
	if ma.RxBytes == 0 || mb.RxBytes == 0 {
		t.Fatal("no se contabilizaron bytes recibidos")
	}
	if ma.Codec != "PCMU" {
		t.Fatalf("códec mal reportado: %q", ma.Codec)
	}
	// En loopback no debería perderse nada (toleramos 2 por bordes del muestreo).
	if ma.Lost > 2 || mb.Lost > 2 {
		t.Fatalf("pérdida inesperada en loopback (a=%d b=%d)", ma.Lost, mb.Lost)
	}
}

// Una sesión recién abierta pero nunca arrancada debe cerrarse sin colgarse.
func TestSessionCloseWithoutStart(t *testing.T) {
	s, err := Open("127.0.0.1", nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()
	s.Close() // idempotente
}
