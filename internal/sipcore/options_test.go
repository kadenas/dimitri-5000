package sipcore

import (
	"context"
	"strconv"
	"testing"
	"time"
)

// TestOptionsTrunk verifica que una instancia con Serve responde 200 a un OPTIONS,
// es decir, que se comporta como un trunk vivo ante el keepalive.
func TestOptionsTrunk(t *testing.T) {
	const ip = "127.0.0.1"
	const trunkPort = 35090
	const monPort = 35091

	trunk, err := New(ip, trunkPort, "trunk", nil)
	if err != nil {
		t.Fatalf("crear trunk: %v", err)
	}
	defer trunk.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = trunk.Serve(ctx, "udp", ip+":"+strconv.Itoa(trunkPort)) }()
	time.Sleep(300 * time.Millisecond)

	mon, err := New(ip, monPort, "mon", nil)
	if err != nil {
		t.Fatalf("crear monitor: %v", err)
	}
	defer mon.Close()
	// El monitor también debe escuchar en su puerto para recibir la respuesta
	// (su Via anuncia ese puerto; sin listener la respuesta se perdería).
	go func() { _ = mon.Serve(ctx, "udp", ip+":"+strconv.Itoa(monPort)) }()
	time.Sleep(300 * time.Millisecond)

	probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer probeCancel()
	res, err := mon.SendOptions(probeCtx, ip, trunkPort, "udp")
	if err != nil {
		t.Fatalf("SendOptions devolvió error: %v", err)
	}
	if res.Code != 200 {
		t.Fatalf("esperaba 200 OK al OPTIONS, obtenido %d %s", res.Code, res.Reason)
	}
}
