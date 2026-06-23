// Paquete sipcore: la ÚNICA capa que habla directamente con sipgo. El resto
// del programa no importa sipgo; pasa siempre por aquí. Así, si algún día
// cambiamos de librería SIP o ampliamos a INVITE/UAS, el impacto queda
// contenido en este paquete y no se desparrama por todo el proyecto.
//
// Conceptos de sipgo que usamos (explicación breve, la primera vez):
//   - UserAgent (UA): la "identidad" SIP y la maquinaria de transporte +
//     transacciones. Se crea una sola vez y se reutiliza.
//   - Client: el manejador para *enviar* peticiones salientes (somos UAC).
//   - Transacción: por debajo, sipgo gestiona las retransmisiones y los timers
//     de la RFC 3261. Nosotros solo pedimos "envía esto y dame la respuesta
//     final". Esa es justamente la parte difícil que NO queremos reimplementar.
package sipcore

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"
)

// Core envuelve el UA y el Client de sipgo, que se reutilizan en cada envío.
//
// Mantiene además las piezas necesarias para el modo llamada (Fase 1):
//   - dialogClient: caché de diálogos salientes (rol UAC).
//   - server + dialogServer: servidor y caché de diálogos entrantes (rol UAS),
//     que solo existen cuando se llama a Serve.
//   - contact: cabecera Contact común (host = bindIP, port = sipPort). Es la
//     dirección donde el otro extremo nos enviará las peticiones dentro del
//     diálogo (ACK, BYE). Por eso UAC y UAS comparten el mismo puerto local.
type Core struct {
	ua     *sipgo.UserAgent
	client *sipgo.Client
	log    *slog.Logger

	bindIP  string
	sipPort int
	contact sip.ContactHeader

	dialogClient *sipgo.DialogClientCache
	server       *sipgo.Server
	dialogServer *sipgo.DialogServerCache

	// uas define cómo responde el servidor a las llamadas entrantes. Se puede
	// ajustar antes de llamar a Serve; si no, usa valores por defecto sensatos.
	uas UASPolicy
}

// Result resume el resultado de un envío de OPTIONS.
type Result struct {
	Code   int           // código de respuesta SIP (ej: 200). 0 si no hubo respuesta.
	Reason string        // texto de la respuesta (ej: "OK")
	RTT    time.Duration // tiempo de ida y vuelta hasta la respuesta final
}

// New crea el núcleo SIP. bindIP es la IP local que aparecerá en las cabeceras
// Via/Contact y desde la que saldrá el tráfico; sipPort es el puerto SIP local
// (origen del UAC y escucha del UAS); userAgent es el nombre que se anuncia en
// la cabecera User-Agent. log puede ser nil (se usa un logger por defecto).
func New(bindIP string, sipPort int, userAgent string, log *slog.Logger) (*Core, error) {
	if log == nil {
		log = slog.Default()
	}

	ua, err := sipgo.NewUA(sipgo.WithUserAgent(userAgent))
	if err != nil {
		return nil, fmt.Errorf("creando user agent: %w", err)
	}

	// WithClientHostname fija la IP local que sipgo pondrá en Via/Contact.
	// WithClientPort fija el puerto de ORIGEN del cliente: lo igualamos al puerto
	// de escucha del servidor para que las peticiones dentro del diálogo (BYE,
	// ACK) regresen a un único socket conocido.
	client, err := sipgo.NewClient(ua,
		sipgo.WithClientHostname(bindIP),
		sipgo.WithClientPort(sipPort),
	)
	if err != nil {
		ua.Close()
		return nil, fmt.Errorf("creando client: %w", err)
	}

	// Contact común: dónde nos pueden contactar dentro del diálogo.
	contact := sip.ContactHeader{
		Address: sip.Uri{Scheme: "sip", Host: bindIP, Port: sipPort},
	}

	c := &Core{
		ua:      ua,
		client:  client,
		log:     log,
		bindIP:  bindIP,
		sipPort: sipPort,
		contact: contact,
		uas:     defaultUASPolicy(),
	}
	// Caché de diálogos salientes (UAC): lista para lanzar llamadas.
	c.dialogClient = sipgo.NewDialogClientCache(client, contact)
	return c, nil
}

// LocalIP devuelve la IP local de señalización (bind IP).
func (c *Core) LocalIP() string { return c.bindIP }

// LocalPort devuelve el puerto SIP local.
func (c *Core) LocalPort() int { return c.sipPort }

// SendOptions envía un OPTIONS al destino host:port indicado y espera la
// respuesta final. El ctx permite imponer un timeout por envío (lo fija el faro).
func (c *Core) SendOptions(ctx context.Context, host string, port int, transport string) (Result, error) {
	// URI destino: sip:host:port
	recipient := sip.Uri{
		Scheme: "sip",
		Host:   host,
		Port:   port,
	}

	req := sip.NewRequest(sip.OPTIONS, recipient)
	req.SetTransport(transport)
	// Fijamos explícitamente el destino de red por si la URI no resuelve sola.
	req.SetDestination(fmt.Sprintf("%s:%d", host, port))

	start := time.Now()
	// Do() envía la petición y devuelve la respuesta FINAL (descarta las 1xx).
	// Por debajo, sipgo crea la transacción cliente y gestiona retransmisiones.
	res, err := c.client.Do(ctx, req)
	rtt := time.Since(start)
	if err != nil {
		// Sin respuesta final: timeout, destino inalcanzable, etc.
		return Result{RTT: rtt}, err
	}

	return Result{
		Code:   int(res.StatusCode),
		Reason: res.Reason,
		RTT:    rtt,
	}, nil
}

// Close libera los recursos del núcleo SIP (orden inverso a la creación).
func (c *Core) Close() {
	if c.client != nil {
		c.client.Close()
	}
	if c.ua != nil {
		c.ua.Close()
	}
}
