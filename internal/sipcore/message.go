// Mensajería SIP (RFC 3428): envío y recepción de peticiones MESSAGE. Es
// mensajería instantánea fuera de diálogo: un MESSAGE lleva un cuerpo de texto y
// se responde con 200 OK. Útil para pruebas de paso de mensajes a través de un
// SBC/PBX, igual que hacemos con las llamadas.
package sipcore

import (
	"context"
	"fmt"
	"time"

	"github.com/emiago/sipgo/sip"
)

// MessageSpec describe un MESSAGE saliente con valores concretos (identidades,
// destino, cuerpo y cabeceras), en la misma línea que RichInvite para llamadas.
type MessageSpec struct {
	DestHost  string // a dónde se envía (SBC o peer): host del Request-URI
	DestPort  int    // puerto del destino real
	Transport string // "udp"|"tcp" (vacío = udp)

	FromUser    string
	FromDomain  string // vacío = IP de bind
	FromDisplay string

	ToUser   string
	ToDomain string // vacío = DestHost

	Body        string // texto del mensaje
	ContentType string // vacío = text/plain

	Headers map[string]string // cabeceras arbitrarias
}

// MessageEvent es un MESSAGE entrante ya recibido, listo para que las capas
// superiores (control/web) lo registren y lo muestren.
type MessageEvent struct {
	From string // identidad del remitente (From)
	To   string // a quién iba dirigido (To)
	Body string // cuerpo del mensaje
}

// SetMessageHandler registra un callback que se invoca por cada MESSAGE entrante.
// Debe llamarse antes de Serve. Si es nil, los MESSAGE se contestan 200 OK igual,
// pero no se notifican.
func (c *Core) SetMessageHandler(h func(MessageEvent)) {
	c.msgHandler = h
}

// SendMessage construye y envía un MESSAGE, devolviendo el resultado (código y
// razón de la respuesta final). No abre diálogo: es una transacción suelta.
func (c *Core) SendMessage(ctx context.Context, ms MessageSpec) (Result, error) {
	transport := ms.Transport
	if transport == "" {
		transport = "udp"
	}

	// Request-URI: a dónde va dirigido y a dónde se envía el paquete.
	recipient := sip.Uri{Scheme: "sip", User: ms.ToUser, Host: ms.DestHost, Port: ms.DestPort}
	req := sip.NewRequest(sip.MESSAGE, recipient)
	req.SetTransport(transport)
	req.SetDestination(fmt.Sprintf("%s:%d", ms.DestHost, ms.DestPort))

	// From con su tag (toda petición SIP debe llevarlo).
	fromDomain := ms.FromDomain
	if fromDomain == "" {
		fromDomain = c.bindIP
	}
	from := &sip.FromHeader{
		DisplayName: ms.FromDisplay,
		Address:     sip.Uri{Scheme: "sip", User: ms.FromUser, Host: fromDomain},
		Params:      sip.NewParams(),
	}
	from.Params.Add("tag", sip.GenerateTagN(16))
	req.AppendHeader(from)

	// To (el dominio puede diferir del destino real).
	toDomain := ms.ToDomain
	if toDomain == "" {
		toDomain = ms.DestHost
	}
	req.AppendHeader(&sip.ToHeader{
		Address: sip.Uri{Scheme: "sip", User: ms.ToUser, Host: toDomain},
	})

	// Cuerpo y tipo de contenido.
	contentType := ms.ContentType
	if contentType == "" {
		contentType = "text/plain"
	}
	req.AppendHeader(sip.NewHeader("Content-Type", contentType))
	req.SetBody([]byte(ms.Body))

	// Cabeceras arbitrarias.
	for nombre, valor := range ms.Headers {
		req.AppendHeader(sip.NewHeader(nombre, valor))
	}

	start := time.Now()
	res, err := c.client.Do(ctx, req)
	rtt := time.Since(start)
	if err != nil {
		return Result{RTT: rtt}, err
	}
	return Result{Code: int(res.StatusCode), Reason: res.Reason, RTT: rtt}, nil
}

// onMessage maneja un MESSAGE entrante: responde 200 OK (RFC 3428) y, si hay
// handler registrado, notifica el mensaje para que se guarde y se muestre.
func (c *Core) onMessage(req *sip.Request, tx sip.ServerTransaction) {
	body := string(req.Body())
	c.log.Info("MESSAGE entrante",
		"from", req.From().Address.String(),
		"to", req.To().Address.String(),
		"body", body,
	)
	if c.msgHandler != nil {
		c.msgHandler(MessageEvent{
			From: req.From().Address.String(),
			To:   req.To().Address.String(),
			Body: body,
		})
	}
	if err := tx.Respond(sip.NewResponseFromRequest(req, 200, "OK", nil)); err != nil {
		c.log.Error("respondiendo MESSAGE", "error", err)
	}
}
