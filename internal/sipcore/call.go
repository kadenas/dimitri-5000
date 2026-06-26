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
	"strings"
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

// SplitURI parsea un destino "sip:user@host:port" (o "sip:host:port") y devuelve
// sus partes. Lo usa la web para el modo simple de PLACE CALL (solo URI), sin que
// las capas superiores tengan que conocer sipgo.
func SplitURI(uri string) (host string, port int, user string, err error) {
	var u sip.Uri
	if err := sip.ParseUri(uri, &u); err != nil {
		return "", 0, "", fmt.Errorf("URI inválida %q: %w", uri, err)
	}
	return u.Host, u.Port, u.User, nil
}

// RichInvite describe una llamada con valores SIP concretos, pensada para
// pruebas realistas contra un SBC/PBX: identidades (From/To con número y dominio),
// destino real del paquete (el SBC, separado del To), P-Asserted-Identity y
// cabeceras arbitrarias.
//
// Clave para atravesar un SBC: el Request-URI se envía a DestHost:DestPort (el
// SBC), pero el To puede llevar otro dominio. El SBC enruta por el número y
// entrega la llamada a su destino real (p. ej. otro agente nuestro).
type RichInvite struct {
	DestHost  string // a dónde se envía de verdad el INVITE (SBC o peer). Host del Request-URI
	DestPort  int    // puerto del destino real
	Transport string // "udp"|"tcp" (vacío = udp)

	FromUser    string // número/usuario origen
	FromDomain  string // dominio del From (vacío = IP de bind)
	FromDisplay string // nombre visible del llamante (opcional)

	ToUser   string // número/usuario destino (vacío = sin user en el Request-URI)
	ToDomain string // dominio del To (vacío = DestHost)

	PAIUser string // P-Asserted-Identity: número (vacío = no se añade la cabecera)

	Headers map[string]string // cabeceras arbitrarias (Diversion, X-..., etc.)
	Body    []byte            // cuerpo (SDP). nil = sin cuerpo
}

// DialInvite construye y envía un INVITE con valores concretos (identidades,
// destino, PAI y cabeceras), devolviendo la llamada para esperar la respuesta.
// Es la vía "humana" para recrear comportamientos reales de telco.
func (c *Core) DialInvite(ctx context.Context, ri RichInvite) (*UACCall, error) {
	// --- Request-URI: a dónde va dirigida (y a dónde se envía el paquete) ---
	recipient := sip.Uri{Scheme: "sip", User: ri.ToUser, Host: ri.DestHost, Port: ri.DestPort}
	transport := ri.Transport
	if transport == "" {
		transport = "udp"
	}
	if transport == "tcp" {
		recipient.UriParams = sip.NewParams()
		recipient.UriParams.Add("transport", "tcp")
	}

	// --- From: identidad del llamante, con su tag (obligatorio para el diálogo) ---
	fromDomain := ri.FromDomain
	if fromDomain == "" {
		fromDomain = c.bindIP
	}
	from := &sip.FromHeader{
		DisplayName: ri.FromDisplay,
		Address:     sip.Uri{Scheme: "sip", User: ri.FromUser, Host: fromDomain},
		Params:      sip.NewParams(),
	}
	from.Params.Add("tag", sip.GenerateTagN(16))

	// --- To: a quién va dirigida (dominio puede diferir del destino real) ---
	toDomain := ri.ToDomain
	if toDomain == "" {
		toDomain = ri.DestHost
	}
	to := &sip.ToHeader{
		Address: sip.Uri{Scheme: "sip", User: ri.ToUser, Host: toDomain},
	}

	// Cabeceras tipadas primero; sipgo las respeta y no las regenera.
	headers := []sip.Header{from, to}

	// P-Asserted-Identity (identidad asegurada), habitual en SBC/PBX.
	if ri.PAIUser != "" {
		pai := fmt.Sprintf("<sip:%s@%s>", ri.PAIUser, fromDomain)
		headers = append(headers, sip.NewHeader("P-Asserted-Identity", pai))
	}

	// Cabeceras arbitrarias del usuario.
	for nombre, valor := range ri.Headers {
		headers = append(headers, sip.NewHeader(nombre, valor))
	}

	session, err := c.dialogClient.Invite(ctx, recipient, ri.Body, headers...)
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

// AnswerSDP devuelve el cuerpo SDP de la respuesta final (200 OK) al INVITE, o nil
// si no la hubo o no traía cuerpo. Lo usa la capa de control para negociar la media
// del lado UAC (saber a qué IP:puerto y con qué códec enviar el RTP).
func (call *UACCall) AnswerSDP() []byte {
	if call.session == nil || call.session.InviteResponse == nil {
		return nil
	}
	return call.session.InviteResponse.Body()
}

// Hangup cuelga la llamada enviando BYE y espera el 200 de confirmación.
func (call *UACCall) Hangup(ctx context.Context) error {
	return call.session.Bye(ctx)
}

// Done devuelve un canal que se cierra cuando el diálogo TERMINA: lo normal es un
// BYE remoto (el otro extremo cuelga) o cualquier otra terminación. sipgo cancela
// el contexto del diálogo al pasar a estado Ended, así que esto avisa "en caliente"
// sin sondear. Lo usa el motor de carga para reponer las llamadas que se caen.
func (call *UACCall) Done() <-chan struct{} {
	return call.session.Context().Done()
}

// Refer envía un REFER dentro del diálogo para DESVIAR la llamada (transferencia
// ciega): pide al otro extremo que contacte con 'referTo'. sipgo construye las
// cabeceras de diálogo (From/To/Call-ID/CSeq/Route/Contact); nosotros fijamos el
// Request-URI (el contacto remoto) y la cabecera Refer-To. Devuelve la respuesta
// (lo normal es 202 Accepted).
func (call *UACCall) Refer(ctx context.Context, referTo string) (Result, error) {
	// Request-URI = contacto remoto del 200 OK (o, en su defecto, el del INVITE).
	var recipient sip.Uri
	if call.session.InviteResponse != nil {
		if contact := call.session.InviteResponse.Contact(); contact != nil {
			recipient = contact.Address
		}
	}
	if recipient.Host == "" {
		recipient = call.session.InviteRequest.Recipient
	}

	req := sip.NewRequest(sip.REFER, recipient)
	req.AppendHeader(sip.NewHeader("Refer-To", normalizeReferTo(referTo)))

	start := time.Now()
	res, err := call.session.Do(ctx, req)
	rtt := time.Since(start)
	if err != nil {
		return Result{RTT: rtt}, err
	}
	return Result{Code: int(res.StatusCode), Reason: res.Reason, RTT: rtt}, nil
}

// normalizeReferTo asegura que el valor de Refer-To sea una URI SIP entre <>.
func normalizeReferTo(v string) string {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "sip:") && !strings.HasPrefix(v, "<") {
		v = "sip:" + v
	}
	if !strings.HasPrefix(v, "<") {
		v = "<" + v + ">"
	}
	return v
}

// ----------------------------- Rol UAS (recibir) -----------------------------

// UASPolicy define cómo responde el servidor a una llamada entrante. El modo
// simple (RingDelay/AnswerCode/HoldTime) es un auto-answer configurable; si Script
// no está vacío, dirige la respuesta paso a paso (escenarios role uas).
type UASPolicy struct {
	RingDelay  time.Duration // espera entre el 180 Ringing y la respuesta final
	AnswerCode int           // código de respuesta final (200 = contestar; 486 = ocupado, etc.)
	HoldTime   time.Duration // tras contestar, cuánto sostener la llamada antes de colgar (0 = esperar el BYE remoto)

	// Script, si no está vacío, dirige la respuesta a las llamadas entrantes paso a
	// paso (pausas + respuestas con su temporización), sustituyendo a RingDelay/
	// AnswerCode/HoldTime. Lo construye el runner de escenarios (rol uas) a partir
	// del YAML; sipcore solo lo EJECUTA, sin conocer el lenguaje de escenarios.
	Script []UASStep
}

// UASStepKind distingue las acciones de un paso del guion de respuesta UAS.
type UASStepKind int

const (
	UASPause            UASStepKind = iota // esperar Dur
	UASSendProvisional                     // enviar respuesta 1xx (Code/Reason)
	UASSendFinal                           // enviar respuesta final (2xx con media si procede; 3xx-6xx = rechazo)
	UASWaitBye                             // esperar el BYE remoto (el otro extremo cuelga)
	UASSendBye                             // colgar nosotros (enviar BYE)
)

// UASStep es un paso del guion de respuesta del UAS. Es un tipo NEUTRO (sin saber
// de YAML): el runner traduce un escenario role uas a una lista de estos pasos.
type UASStep struct {
	Kind   UASStepKind
	Dur    time.Duration // para UASPause
	Code   int           // para UASSendProvisional / UASSendFinal
	Reason string        // texto de la respuesta (vacío = estándar del código)
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

// SetUASPolicy ajusta el comportamiento del servidor. Es seguro llamarlo en
// caliente (con el servidor ya sirviendo): onInvite toma una copia bajo lock, así
// que las llamadas EN CURSO no se ven afectadas y las nuevas usan la política nueva.
func (c *Core) SetUASPolicy(p UASPolicy) {
	c.uasMu.Lock()
	c.uas = p
	c.uasMu.Unlock()
}

// uasPolicy devuelve una copia de la política UAS vigente (lectura segura).
func (c *Core) uasPolicy() UASPolicy {
	c.uasMu.RLock()
	defer c.uasMu.RUnlock()
	return c.uas
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
	c.serveCtx = ctx // vida del servidor: el guion UAS deriva su cancelación de aquí
	// Caché de diálogos entrantes (UAS).
	c.dialogServer = sipgo.NewDialogServerCache(c.client, c.contact)

	srv.OnInvite(c.onInvite)
	srv.OnAck(c.onAck)
	srv.OnBye(c.onBye)
	srv.OnCancel(c.onCancel)
	srv.OnOptions(c.onOptions) // responde a los OPTIONS de keepalive (rol "trunk")
	srv.OnMessage(c.onMessage) // mensajería SIP (RFC 3428): responde 200 y notifica
	srv.OnRefer(c.onRefer)     // desvío entrante: acepta el REFER con 202

	// Backstop: al cancelar el contexto del servidor (parada del agente o de la app)
	// cerramos cualquier sesión de media entrante que siguiera viva.
	go func() {
		<-ctx.Done()
		c.closeAllMedia()
	}()

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
	// Registramos los datos clave de la llamada entrante: útil para verificar que
	// las identidades y cabeceras (p. ej. tras atravesar un SBC) llegan correctas.
	entrante := []any{
		"from", req.From().Address.String(),
		"to", req.To().Address.String(),
		"r-uri", req.Recipient.String(),
	}
	if pai := req.GetHeader("P-Asserted-Identity"); pai != nil {
		entrante = append(entrante, "pai", pai.Value())
	}
	c.log.Info("llamada entrante", entrante...)

	// Copia de la política vigente (la web puede cambiarla en caliente). Las llamadas
	// en curso usan la foto que tomaron aquí; las nuevas, la política nueva.
	pol := c.uasPolicy()

	// Escenario UAS: si hay un guion cargado, dirige él la respuesta (pausas y
	// respuestas con su temporización), sustituyendo al auto-answer fijo.
	if len(pol.Script) > 0 {
		c.runUASScript(pol, req, dlg, tx)
		return
	}

	// 180 Ringing: avisamos de que "suena" antes de contestar.
	if pol.RingDelay > 0 {
		if err := dlg.Respond(180, "Ringing", nil); err != nil {
			c.log.Error("respondiendo 180", "error", err)
			return
		}
		// Pausa que simula el tiempo que tarda en descolgarse.
		select {
		case <-time.After(pol.RingDelay):
		case <-tx.Done(): // el otro extremo canceló mientras "sonaba"
			return
		}
	}

	// Respuesta final según la política. Si vamos a contestar (2xx) y la media está
	// activada, intentamos negociar audio: respondemos 200 con SDP y arrancamos la
	// sesión RTP. Si no hay oferta válida (o la media está desactivada), contestamos
	// con el código de la política sin cuerpo. WriteResponse para un 2xx bloquea
	// hasta recibir el ACK, dejando el diálogo confirmado.
	answeredWithMedia := false
	if pol.AnswerCode >= 200 && pol.AnswerCode < 300 {
		answeredWithMedia = c.answerWithMedia(req, dlg)
	}
	if !answeredWithMedia {
		if err := dlg.Respond(pol.AnswerCode, reasonFor(pol.AnswerCode), nil); err != nil {
			c.log.Error("respondiendo final", "code", pol.AnswerCode, "error", err)
			return
		}
	}

	// Si contestamos y hay HoldTime, colgamos nosotros tras ese tiempo.
	if pol.AnswerCode >= 200 && pol.AnswerCode < 300 && pol.HoldTime > 0 {
		select {
		case <-time.After(pol.HoldTime):
			byeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := dlg.Bye(byeCtx); err != nil {
				c.log.Error("enviando BYE (UAS)", "error", err)
			}
			c.closeMediaSession(callIDOf(req)) // colgamos nosotros: liberamos la media
		case <-tx.Done():
			c.closeMediaSession(callIDOf(req)) // cancelado/colgado: liberamos la media
		}
	}
}

// runUASScript ejecuta el guion de respuesta del UAS (construido por el runner a
// partir de un escenario role uas): pausas y respuestas con su temporización. El
// 2xx se contesta con media si está activada y hay oferta; dlg.Respond para un 2xx
// bloquea hasta el ACK, así que los pasos siguientes (esperar/enviar BYE) ocurren
// con el diálogo ya confirmado. Una respuesta no-2xx termina el diálogo.
func (c *Core) runUASScript(pol UASPolicy, req *sip.Request, dlg *sipgo.DialogServerSession, tx sip.ServerTransaction) {
	ctx := c.serveCtx
	if ctx == nil {
		ctx = context.Background()
	}
	id := callIDOf(req)
	answered := false

	for _, st := range pol.Script {
		switch st.Kind {
		case UASPause:
			// Antes de contestar, un CANCEL del otro extremo (tx.Done) aborta la espera;
			// después de contestar el tx del INVITE ya terminó, así que no lo escuchamos.
			if answered {
				select {
				case <-time.After(st.Dur):
				case <-ctx.Done():
					return
				}
			} else {
				select {
				case <-time.After(st.Dur):
				case <-ctx.Done():
					return
				case <-tx.Done():
					return
				}
			}

		case UASSendProvisional:
			if err := dlg.Respond(st.Code, reasonOr(st.Code, st.Reason), nil); err != nil {
				c.log.Error("guion UAS: respondiendo provisional", "code", st.Code, "error", err)
				return
			}

		case UASSendFinal:
			if st.Code >= 200 && st.Code < 300 {
				if !c.answerWithMedia(req, dlg) {
					if err := dlg.Respond(st.Code, reasonOr(st.Code, st.Reason), nil); err != nil {
						c.log.Error("guion UAS: respondiendo 2xx", "code", st.Code, "error", err)
						return
					}
				}
				answered = true
			} else {
				if err := dlg.Respond(st.Code, reasonOr(st.Code, st.Reason), nil); err != nil {
					c.log.Error("guion UAS: respondiendo final", "code", st.Code, "error", err)
				}
				return // respuesta no-2xx: la llamada no se establece
			}

		case UASWaitBye:
			// Esperamos a que el otro extremo cuelgue. onBye responde el 200 y nos
			// despierta; aquí no reenviamos nada (evita doble respuesta al BYE).
			c.waitBye(ctx, id)
			return

		case UASSendBye:
			byeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := dlg.Bye(byeCtx); err != nil {
				c.log.Error("guion UAS: enviando BYE", "error", err)
			}
			cancel()
			c.closeMediaSession(id)
			return
		}
	}
}

// waitBye bloquea hasta que llegue el BYE de la llamada (Call-ID) o se cancele el
// contexto. El disparo lo hace onBye al recibir el BYE de ese diálogo.
func (c *Core) waitBye(ctx context.Context, callID string) {
	ch := make(chan struct{})
	c.byeMu.Lock()
	if c.byeWaiters == nil {
		c.byeWaiters = make(map[string]chan struct{})
	}
	c.byeWaiters[callID] = ch
	c.byeMu.Unlock()

	defer func() {
		c.byeMu.Lock()
		if c.byeWaiters[callID] == ch {
			delete(c.byeWaiters, callID)
		}
		c.byeMu.Unlock()
	}()

	select {
	case <-ch:
	case <-ctx.Done():
	}
}

// fireByeWaiter despierta al guion UAS que esperaba el BYE de esta llamada (si lo
// había). Cierra el canal una sola vez (el que tiene la entrada la borra y cierra).
func (c *Core) fireByeWaiter(callID string) {
	c.byeMu.Lock()
	if ch, ok := c.byeWaiters[callID]; ok {
		delete(c.byeWaiters, callID)
		close(ch)
	}
	c.byeMu.Unlock()
}

// reasonOr devuelve reason si no está vacío; si lo está, el texto estándar del código.
func reasonOr(code int, reason string) string {
	if reason != "" {
		return reason
	}
	return reasonFor(code)
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

// onRefer maneja un REFER entrante (desvío). En la v1 lo ACEPTAMOS con 202 para
// que el flujo de transferencia sea coherente; la transferencia "completa" (que el
// transferido lance el nuevo INVITE al Refer-To y mande NOTIFYs) la realiza un
// SBC/PBX real, no nuestra parte de prueba.
func (c *Core) onRefer(req *sip.Request, tx sip.ServerTransaction) {
	referTo := ""
	if h := req.GetHeader("Refer-To"); h != nil {
		referTo = h.Value()
	}
	c.log.Info("REFER entrante (desvío)", "refer-to", referTo, "from", req.From().Address.String())
	if err := tx.Respond(sip.NewResponseFromRequest(req, 202, "Accepted", nil)); err != nil {
		c.log.Error("respondiendo REFER", "error", err)
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
	// Si la llamada tenía media entrante (rol UAS), la liberamos al colgar. Para una
	// llamada saliente (rol UAC) su Call-ID no está en el mapa y esto es un no-op.
	c.closeMediaSession(callIDOf(req))
	if c.dialogServer != nil {
		if err := c.dialogServer.ReadBye(req, tx); err == nil {
			c.fireByeWaiter(callIDOf(req)) // despierta al guion UAS que esperaba el BYE
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

// reasonFor devuelve el texto estándar (RFC 3261) para los códigos habituales en
// escenarios. Si no se reconoce, devuelve un texto genérico por familia.
func reasonFor(code int) string {
	switch code {
	case 100:
		return "Trying"
	case 180:
		return "Ringing"
	case 183:
		return "Session Progress"
	case 200:
		return "OK"
	case 400:
		return "Bad Request"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 408:
		return "Request Timeout"
	case 480:
		return "Temporarily Unavailable"
	case 486:
		return "Busy Here"
	case 487:
		return "Request Terminated"
	case 500:
		return "Server Internal Error"
	case 503:
		return "Service Unavailable"
	case 603:
		return "Decline"
	}
	switch {
	case code >= 100 && code < 200:
		return "Provisional"
	case code >= 200 && code < 300:
		return "OK"
	default:
		return "Error"
	}
}
