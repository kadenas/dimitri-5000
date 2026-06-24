package sipcore

import (
	"context"
	"testing"
	"time"
)

// TestLlamadaLoopback verifica el flujo completo de una llamada en local:
// un Core en modo UAS escucha en un puerto y otro Core en modo UAC le llama.
// Comprueba INVITE -> (180) -> 200 -> ACK -> BYE -> 200.
func TestLlamadaLoopback(t *testing.T) {
	const (
		ip      = "127.0.0.1"
		uasPort = 35070 // puerto del que recibe (UAS)
		uacPort = 35071 // puerto del que llama (UAC)
	)

	// --- UAS: el que recibe la llamada ---
	uas, err := New(ip, uasPort, "dimitri-uas", "", nil)
	if err != nil {
		t.Fatalf("creando UAS: %v", err)
	}
	defer uas.Close()
	// Contesta rápido y deja que cuelgue el llamante.
	uas.SetUASPolicy(UASPolicy{RingDelay: 50 * time.Millisecond, AnswerCode: 200})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	serveErr := make(chan error, 1)
	go func() { serveErr <- uas.Serve(ctx, "udp", ip+":"+itoa(uasPort)) }()

	// Damos un instante a que el servidor abra el socket.
	time.Sleep(200 * time.Millisecond)

	// --- UAC: el que llama ---
	uac, err := New(ip, uacPort, "dimitri-uac", "", nil)
	if err != nil {
		t.Fatalf("creando UAC: %v", err)
	}
	defer uac.Close()
	// El UAC también necesita escuchar para recibir el BYE/respuestas dentro del diálogo.
	go func() { _ = uac.Serve(ctx, "udp", ip+":"+itoa(uacPort)) }()
	time.Sleep(200 * time.Millisecond)

	callCtx, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer callCancel()

	call, err := uac.DialURI(callCtx, "sip:"+ip+":"+itoa(uasPort), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	if err := call.WaitAnswer(callCtx); err != nil {
		t.Fatalf("WaitAnswer (esperaba contestar): %v", err)
	}
	if err := call.Ack(callCtx); err != nil {
		t.Fatalf("Ack: %v", err)
	}

	// Llamada establecida; la mantenemos un momento y colgamos.
	time.Sleep(100 * time.Millisecond)

	if err := call.Hangup(callCtx); err != nil {
		t.Fatalf("Hangup (BYE): %v", err)
	}

	cancel() // paramos los servidores
	select {
	case <-serveErr:
	case <-time.After(time.Second):
	}
}

// itoa convierte un entero pequeño y positivo a string sin tirar de strconv en
// el cuerpo del test (claridad de lectura).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [10]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
