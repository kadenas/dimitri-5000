package runner

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

const escenarioUAC = `
name: uac-test
role: uac
defaults:
  recv_timeout: 5s
variables:
  caller: "1000"
  callee: "2000"
  domain: "{remote_host}"
steps:
  - send: INVITE
    headers:
      From: "sip:{caller}@{domain}"
      To: "sip:{callee}@{domain}"
    body: oferta
  - recv: "100"
    optional: true
  - recv: "180"
    optional: true
  - recv: "200"
  - send: ACK
  - pause: 100ms
  - send: BYE
  - recv: "200"
bodies:
  oferta:
    type: sdp
    media: g711
`

// TestRunnerUACLoopback ejecuta un escenario UAC real contra un UAS de auto-answer
// en el mismo proceso, verificando el flujo completo dirigido por el escenario.
func TestRunnerUACLoopback(t *testing.T) {
	const ip = "127.0.0.1"
	const uasPort = 35080
	const uacPort = 35081

	// UAS de auto-answer.
	uas, err := sipcore.New(ip, uasPort, "uas", "", nil)
	if err != nil {
		t.Fatalf("crear UAS: %v", err)
	}
	defer uas.Close()
	uas.SetUASPolicy(sipcore.UASPolicy{RingDelay: 30 * time.Millisecond, AnswerCode: 200})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = uas.Serve(ctx, "udp", ip+":"+strconv.Itoa(uasPort)) }()
	time.Sleep(200 * time.Millisecond)

	// UAC + runner.
	uac, err := sipcore.New(ip, uacPort, "uac", "", nil)
	if err != nil {
		t.Fatalf("crear UAC: %v", err)
	}
	defer uac.Close()
	go func() { _ = uac.Serve(ctx, "udp", ip+":"+strconv.Itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond)

	sc, err := scenario.Parse([]byte(escenarioUAC))
	if err != nil {
		t.Fatalf("parsear escenario: %v", err)
	}

	r := New(uac, "sip:"+ip+":"+strconv.Itoa(uasPort), nil)

	runCtx, runCancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer runCancel()
	if err := r.Run(runCtx, sc); err != nil {
		t.Fatalf("ejecución del escenario falló: %v", err)
	}
}
