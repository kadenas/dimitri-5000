package sipcore

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

// TestFailureCause verifica la clasificación neutra de los errores de INVITE:
// rechazo con código (ErrDialogResponse por valor Y por puntero, sipgo usa ambas),
// timeout de transacción, cancelación propia y el cajón "error".
func TestFailureCause(t *testing.T) {
	res486 := &sip.Response{StatusCode: 486, Reason: "Busy Here"}
	res503 := &sip.Response{StatusCode: 503, Reason: "Service Unavailable"}

	casos := []struct {
		nombre string
		err    error
		want   string
	}{
		{"rechazo por valor", sipgo.ErrDialogResponse{Res: res486}, "486"},
		{"rechazo por puntero", &sipgo.ErrDialogResponse{Res: res503}, "503"},
		{"rechazo envuelto", fmt.Errorf("la llamada no fue contestada: %w", &sipgo.ErrDialogResponse{Res: res486}), "486"},
		{"timeout de transacción", fmt.Errorf("Timer_B timed out. %w", sip.ErrTransactionTimeout), "timeout"},
		{"deadline del contexto", context.DeadlineExceeded, "timeout"},
		{"cancelación propia", context.Canceled, "cancelada"},
		{"cancelación envuelta", fmt.Errorf("enviando INVITE: %w", context.Canceled), "cancelada"},
		{"error de transporte", errors.New("write udp: connection refused"), "error"},
	}
	for _, c := range casos {
		if got := FailureCause(c.err); got != c.want {
			t.Errorf("%s: FailureCause=%q, esperado %q", c.nombre, got, c.want)
		}
	}
}
