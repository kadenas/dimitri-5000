// Llamadas SIP (Fase 1): rol UAC (lanzar) y rol UAS (recibir), sobre el mismo
// Core. Toda la interacción con sipgo vive aquí; el resto del programa usa estos
// tipos sin conocer la librería SIP.
//
// Flujo de una llamada (RFC 3261, feliz):
//
//	UAC  --INVITE-->  UAS
//	UAC  <--180----   UAS   (Ringing, opcional)
//	UAC  <--200----   UAS   (OK: llamada contestada)
//	UAC  --ACK---->   UAS   (confirma; diálogo establecido)
//	  ... conversación ...
//	UAC  --BYE---->   UAS   (cuelga cualquiera de los dos)
//	UAC  <--200----   UAS
package sipcore

import (
	"context"
	"fmt"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

// ----------------------------- Rol UAC (lanzar) -----------------------------

// UACCall representa una llamada saliente en curso. Envuelve la sesión de diálogo
// de sipgo para no exponer la librería al resto del programa.
type UACCall struct {
	session *sipgo.DialogClientSession
}

// Dial construye y envía un INVITE al destino indicado. Devuelve la llamada en
// estado "temprano" (aún sin respuesta final): hay que llamar a WaitAnswer.
// sdp es el cuerpo SDP de la oferta de media; en la Fase 1 puede ir a nil
// (señalización sin media).
func (c *Core) Dial(ctx context.Context, recipient sip.Uri, sdp []byte) (*UACCall, error) {
	session, err := c.dialogClient.Invite(ctx, recipient, sdp)
	if err != nil {
		return nil, fmt.Errorf("enviando INVITE: %w", err)
	}
	return &UACCall{session: session}, nil
}

// DialURI es una comodidad: parsea "sip:host:port" (o similar) y llama a Dial.
func (c *Core) DialURI(ctx context.Context, uri string, sdp []byte) (*UACCall, error) {
	var recipient sip.Uri
	if err := sip.ParseUri(uri, &recipient); err != nil {
		return nil, fmt.Errorf("URI de destino inválida %q: %w", uri, err)
	}
	return c.Dial(ctx, recipient, sdp)
}

// CallOptions permite a capas superiores (el runner de escenarios) personalizar el
// INVITE sin conocer sipgo: las cabeceras se pasan como texto y aquí se traducen.
type CallOptions struct {
	Headers map[string]string // cabeceras a fijar (From, To, ...). Vacío = autogeneradas
	Body    []byte            // cuerpo del mensaje (p. ej. SDP). nil = sin cuerpo
}

// DialURIWithOptions lanza un INVITE con cabeceras/cuerpo personalizados. Es la
// vía que usa el runner de escenarios.
func (c *Core) DialURIWithOptions(ctx context.Context, uri string, opts CallOptions) (*UACCall, error) {
	var recipient sip.Uri
	if err := sip.ParseUri(uri, &recipient); err != nil {
		return nil, fmt.Errorf("URI de destino inválida %q: %w", uri, err)
	}

	// Traducimos las cabeceras de texto a cabeceras de sipgo (aislamiento de la librería).
	headers := make([]sip.Header, 0, len(opts.Headers))
	for nombre, valor := range opts.Headers {
		headers = append(headers, sip.NewHeader(nombre, valor))
	}

	session, err := c.dialogClient.Invite(ctx, recipient, opts.Body, headers...)
	if err != nil {
		return nil, fmt.Errorf("enviando INVITE: %w", err)
	}
	return &UACCall{session: session}, nil
}

// WaitAnswer bloquea hasta recibir la respuesta final del INVITE. Devuelve nil
// si la llamada fue contestada (2xx); error en caso de rechazo (4xx/5xx/6xx),
// timeout o cancelación del contexto. Si el contexto se cancela mientras se
// espera, sipgo envía CANCEL automáticamente.
func (call *UACCall) WaitAnswer(ctx context.Context) error {
	return call.session.WaitAnswer(ctx, sipgo.AnswerOptions{})
}

// WaitAnswerObserved es como WaitAnswer pero invoca onResponse por CADA respuesta
// recibida (100, 180, 200...). Lo usa el runner para validar los pasos 'recv'
// explícitos contra lo que realmente llega.
func (call *UACCall) WaitAnswerObserved(ctx context.Context, onResponse func(code int, reason string)) error {
	return call.session.WaitAnswer(ctx, sipgo.AnswerOptions{
		OnResponse: func(res *sip.Response) error {
			if onResponse != nil {
				onResponse(int(res.StatusCode), res.Reason)
			}
			return nil
		},
	})
}

// Ack confirma una llamada contestada (envía el ACK del 200 OK). Tras esto el
// diálogo queda establecido.
func (call *UACCall) Ack(ctx context.Context) error {
	return call.session.Ack(ctx)
}

// Hangup cuelga la llamada enviando BYE y espera el 200 de confirmación.
func (call *UACCall) Hangup(ctx context.Context) error {
	return call.session.Bye(ctx)
}

// ----------------------------- Rol UAS (recibir) -----------------------------

// UASPolicy define cómo responde el servidor a una llamada entrante. Para la
// Fase 1 es un auto-answer simple y configurable.
type UASPolicy struct {
	RingDelay  time.Duration // espera entre el 180 Ringing y la respuesta final
	AnswerCode int           // código de respuesta final (200 = contestar; 486 = ocupado, etc.)
	HoldTime   time.Duration // tras contestar, cuánto sostener la llamada antes de colgar (0 = esperar el BYE remoto)
}

// defaultUASPolicy: contesta tras 200 ms de "ringing" y deja que cuelgue el otro
// extremo (no manda BYE por su cuenta).
func defaultUASPolicy() UASPolicy {
	return UASPolicy{
		RingDelay:  200 * time.Millisecond,
		AnswerCode: 200,
		HoldTime:   0,
	}
}

// SetUASPolicy ajusta el comportamiento del servidor. Debe llamarse ANTES de Serve.
func (c *Core) SetUASPolicy(p UASPolicy) {
	c.uas = p
}

// Serve levanta el servidor SIP (rol UAS) y bloquea hasta que el contexto se
// cancela. Registra los handlers de las peticiones que nos importan en la Fase 1
// (INVITE, ACK, BYE, CANCEL). network suele ser "udp"; addr es "ip:puerto".
func (c *Core) Serve(ctx context.Context, network, addr string) error {
	srv, err := sipgo.NewServer(c.ua)
	if err != nil {
		return fmt.Errorf("creando servidor SIP: %w", err)
	}
	c.server = srv
	// Caché de diálogos entrantes (UAS).
	c.dialogServer = sipgo.NewDialogServerCache(c.client, c.contact)

	srv.OnInvite(c.onInvite)
	srv.OnAck(c.onAck)
	srv.OnBye(c.onBye)
	srv.OnCancel(c.onCancel)
	srv.OnOptions(c.onOptions) // responde a los OPTIONS de keepalive (rol "trunk")

	c.log.Info("servidor SIP escuchando", "network", network, "addr", addr)
	return srv.ListenAndServe(ctx, network, addr)
}

// onInvite maneja una llamada entrante con el auto-answer de la política activa.
func (c *Core) onInvite(req *sip.Request, tx sip.ServerTransaction) {
	dlg, err := c.dialogServer.ReadInvite(req, tx)
	if err != nil {
		c.log.Error("INVITE entrante inválido", "error", err)
		return
	}
	c.log.Info("llamada entrante", "from", req.From().Address.String())

	// 180 Ringing: avisamos de que "suena" antes de contestar.
	if c.uas.RingDelay > 0 {
		if err := dlg.Respond(180, "Ringing", nil); err != nil {
			c.log.Error("respondiendo 180", "error", err)
			return
		}
		// Pausa que simula el tiempo que tarda en descolgarse.
		select {
		case <-time.After(c.uas.RingDelay):
		case <-tx.Done(): // el otro extremo canceló mientras "sonaba"
			return
		}
	}

	// Respuesta final según la política (200 = contestar). WriteResponse para un
	// 2xx bloquea hasta recibir el ACK, dejando el diálogo confirmado.
	if err := dlg.Respond(c.uas.AnswerCode, reasonFor(c.uas.AnswerCode), nil); err != nil {
		c.log.Error("respondiendo final", "code", c.uas.AnswerCode, "error", err)
		return
	}

	// Si contestamos y hay HoldTime, colgamos nosotros tras ese tiempo.
	if c.uas.AnswerCode >= 200 && c.uas.AnswerCode < 300 && c.uas.HoldTime > 0 {
		select {
		case <-time.After(c.uas.HoldTime):
			byeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := dlg.Bye(byeCtx); err != nil {
				c.log.Error("enviando BYE (UAS)", "error", err)
			}
		case <-tx.Done():
		}
	}
}

// onOptions responde a un OPTIONS con 200 OK. Es el comportamiento de un trunk
// o centralita ante el keepalive: confirma que está vivo y, según la RFC 3261
// (sección 11), anuncia en 'Allow' los métodos que soporta y en 'Accept' los
// cuerpos que entiende. Así el otro extremo (o nuestro propio faro) lo ve activo.
func (c *Core) onOptions(req *sip.Request, tx sip.ServerTransaction) {
	c.log.Debug("OPTIONS entrante", "from", req.Source())
	res := sip.NewResponseFromRequest(req, 200, "OK", nil)
	res.AppendHeader(sip.NewHeader("Allow", "INVITE, ACK, BYE, CANCEL, OPTIONS"))
	res.AppendHeader(sip.NewHeader("Accept", "application/sdp"))
	res.AppendHeader(&c.contact) // dónde contactarnos
	if err := tx.Respond(res); err != nil {
		c.log.Error("respondiendo OPTIONS", "error", err)
	}
}

// onAck confirma el diálogo de la llamada entrante.
func (c *Core) onAck(req *sip.Request, tx sip.ServerTransaction) {
	if err := c.dialogServer.ReadAck(req, tx); err != nil {
		c.log.Debug("ACK no asociado a diálogo entrante", "error", err)
	}
}

// onBye enruta el BYE al diálogo correcto: primero los entrantes (UAS) y, si no
// casa, los salientes (UAC). Así un mismo servidor sirve a ambos roles.
func (c *Core) onBye(req *sip.Request, tx sip.ServerTransaction) {
	if c.dialogServer != nil {
		if err := c.dialogServer.ReadBye(req, tx); err == nil {
			return
		}
	}
	if c.dialogClient != nil {
		if err := c.dialogClient.ReadBye(req, tx); err == nil {
			return
		}
	}
	// No corresponde a ningún diálogo conocido.
	_ = tx.Respond(sip.NewResponseFromRequest(req, 481, "Call/Transaction Does Not Exist", nil))
}

// onCancel responde a un CANCEL de una llamada entrante aún no contestada.
// sipgo, al leer el INVITE, ya tiene registrado el manejo de cancelación del
// diálogo; aquí solo confirmamos el CANCEL con 200 OK.
func (c *Core) onCancel(req *sip.Request, tx sip.ServerTransaction) {
	_ = tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil))
}

// reasonFor devuelve el texto estándar para los códigos que usamos en la Fase 1.
func reasonFor(code int) string {
	switch code {
	case 200:
		return "OK"
	case 486:
		return "Busy Here"
	case 603:
		return "Decline"
	default:
		return "OK"
	}
}
