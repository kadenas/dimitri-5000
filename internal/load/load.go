// Paquete load: el motor de PRUEBAS DE CARGA. Genera muchas llamadas SIP a un
// ritmo controlado (cps) y SOSTIENE un número objetivo de llamadas establecidas
// simultáneas (N concurrentes), con tráfico de media RTP, hasta que se ordena
// parar (STOP cuelga todas con BYE) o un extremo las termina.
//
// Está DELIBERADAMENTE separado del control de llamadas "manual" (paquete control):
//   - No registra una fila por llamada (con miles sería inservible): lleva
//     contadores agregados (lanzadas, activas, establecidas, fallidas, RTP).
//   - Reutiliza la maquinaria existente (sipcore para la señalización, media para
//     el RTP) sin conocer sipgo: respeta la separación por capas del proyecto.
//
// Modelo de carga (elegido con el usuario): "objetivo de N concurrentes". El motor
// repone automáticamente las llamadas que se caen para mantener N vivas; la cps
// regula la velocidad de subida (ramp-up) y de reposición.
//
// Pensado para escalar a miles de llamadas: una goroutine por llamada (baratas) y
// una sesión de media por llamada (socket RTP efímero). Si se necesita una escala
// aún mayor, el siguiente paso sería un "pump" RTP compartido en vez de un emisor
// por sesión; de momento se reutiliza media.Session, que está probado.
package load

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kadenas/dimitri-5000/internal/media"
	"github.com/kadenas/dimitri-5000/internal/runner"
	"github.com/kadenas/dimitri-5000/internal/scenario"
	"github.com/kadenas/dimitri-5000/internal/sipcore"
)

// Spec describe una prueba de carga a lanzar.
type Spec struct {
	Invite     sipcore.RichInvite // plantilla del INVITE (destino, identidades, cabeceras)
	Concurrent int                // N llamadas establecidas simultáneas a sostener
	CPS        float64            // ritmo de lanzamiento y reposición (llamadas/seg)
	MaxCalls   int64              // tope total de INVITEs (0 = sin tope: reposición indefinida)
	Audio      []int16            // PCM 8 kHz mono a enviar por RTP (nil = tono sintético)
	WithMedia  bool               // abrir RTP por llamada (false = solo señalización)
	Scenario   *scenario.Scenario // si != nil, cada llamada ejecuta este escenario UAC (señalización) en vez del INVITE básico
}

// Stats es la foto agregada de una prueba de carga (viaja a la web como JSON).
type Stats struct {
	Running     bool    `json:"running"`     // hay una prueba en curso
	Stopping    bool    `json:"stopping"`    // se ordenó STOP y se están colgando las vivas
	Target      int     `json:"target"`      // N concurrentes objetivo
	CPS         float64 `json:"cps"`         // ritmo configurado
	MaxCalls    int64   `json:"max_calls"`   // tope total (0 = sin tope)
	WithMedia   bool    `json:"with_media"`  // si se envía RTP
	Scenario    string  `json:"scenario,omitempty"` // nombre del escenario por llamada (vacío = INVITE básico)
	Launched    int64   `json:"launched"`    // INVITEs enviados (acumulado)
	Active      int64   `json:"active"`      // establecidas vivas AHORA
	Pending     int64   `json:"pending"`     // en curso (dialing/ringing) AHORA
	Established int64   `json:"established"` // total que llegaron a establecidas
	Failed      int64   `json:"failed"`      // total fallidas (rechazo/timeout/error)
	Ended       int64   `json:"ended"`       // total terminadas (BYE/caída)
	TxPackets   uint64  `json:"tx_packets"`  // RTP enviado (agregado de las activas)
	RxPackets   uint64  `json:"rx_packets"`  // RTP recibido (agregado)
	TxBytes     uint64  `json:"tx_bytes"`
	RxBytes     uint64  `json:"rx_bytes"`
	Lost        int64   `json:"lost"` // pérdida RTP agregada
	StartedAt   string  `json:"started_at,omitempty"`
}

// run es el estado de UNA ejecución de carga. Cada Start crea uno nuevo; así un
// STOP seguido de un START no mezcla contadores mientras la ejecución previa drena
// (sus workers siguen actualizando SU run, no el nuevo).
type run struct {
	spec      Spec
	cancel    context.CancelFunc
	startedAt time.Time
	wg        sync.WaitGroup // loop de lanzamiento + todos los workers
	stopping  atomic.Bool    // STOP en curso (colgando las vivas)

	launched    atomic.Int64
	pending     atomic.Int64
	active      atomic.Int64
	established atomic.Int64
	failed      atomic.Int64
	ended       atomic.Int64

	sessMu   sync.Mutex
	sessions map[uint64]*media.Session // activas con media, para agregar métricas
	nextID   atomic.Uint64
}

// Generator lanza y sostiene la carga sobre el Core de un agente. Una sola
// ejecución a la vez (la siguiente espera a que la anterior termine de drenar).
type Generator struct {
	core *sipcore.Core
	log  *slog.Logger

	mu  sync.Mutex
	cur *run // nil si no hay carga activa ni drenando
}

// New crea el generador ligado al Core indicado (el de un agente).
func New(core *sipcore.Core, log *slog.Logger) *Generator {
	if log == nil {
		log = slog.Default()
	}
	return &Generator{core: core, log: log}
}

// Start arranca una prueba de carga. Devuelve error si ya hay una en curso o si la
// Spec es inválida. parent es el contexto de vida del agente: si el agente para,
// la carga para (y cuelga sus llamadas).
func (g *Generator) Start(parent context.Context, spec Spec) error {
	if spec.Concurrent <= 0 {
		return errors.New("el número de llamadas concurrentes debe ser > 0")
	}
	if spec.CPS <= 0 {
		spec.CPS = 10 // ritmo por defecto sensato
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cur != nil {
		if g.cur.stopping.Load() {
			return errors.New("la prueba anterior aún se está deteniendo; espera unos segundos")
		}
		return errors.New("ya hay una prueba de carga en curso")
	}

	ctx, cancel := context.WithCancel(parent)
	r := &run{
		spec:      spec,
		cancel:    cancel,
		startedAt: time.Now(),
		sessions:  make(map[uint64]*media.Session),
	}
	g.cur = r

	r.wg.Add(1)
	go g.launchLoop(ctx, r)
	g.log.Info("carga iniciada", "concurrent", spec.Concurrent, "cps", spec.CPS,
		"media", spec.WithMedia, "max_calls", spec.MaxCalls)
	return nil
}

// Stop ordena detener la carga: deja de lanzar y cuelga (BYE) todas las llamadas
// vivas. No bloquea: el drenaje ocurre en segundo plano y se refleja en Stats
// (Stopping=true, Active bajando). Cuando termina, libera la ejecución.
func (g *Generator) Stop() {
	g.mu.Lock()
	r := g.cur
	g.mu.Unlock()
	if r == nil {
		return
	}
	r.stopping.Store(true)
	if r.cancel != nil {
		r.cancel() // detiene el loop y dispara el BYE de cada worker
	}
	go func() {
		r.wg.Wait() // espera a que salgan todos los BYE y se cierre la media
		g.mu.Lock()
		if g.cur == r {
			g.cur = nil
		}
		g.mu.Unlock()
		g.log.Info("carga detenida (todas las llamadas colgadas)")
	}()
}

// Running indica si hay una prueba en curso (incluida la fase de drenaje del STOP).
func (g *Generator) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cur != nil
}

// Snapshot devuelve la foto agregada actual (Running=false si no hay carga).
func (g *Generator) Snapshot() Stats {
	g.mu.Lock()
	r := g.cur
	g.mu.Unlock()
	if r == nil {
		return Stats{Running: false}
	}

	var tx, rx, txB, rxB uint64
	var lost int64
	r.sessMu.Lock()
	for _, s := range r.sessions {
		m := s.Metrics()
		tx += m.TxPackets
		rx += m.RxPackets
		txB += m.TxBytes
		rxB += m.RxBytes
		lost += m.Lost
	}
	r.sessMu.Unlock()

	scName := ""
	if r.spec.Scenario != nil {
		scName = r.spec.Scenario.Name
	}

	return Stats{
		Running:     true,
		Stopping:    r.stopping.Load(),
		Target:      r.spec.Concurrent,
		CPS:         r.spec.CPS,
		MaxCalls:    r.spec.MaxCalls,
		WithMedia:   r.spec.WithMedia,
		Scenario:    scName,
		Launched:    r.launched.Load(),
		Active:      r.active.Load(),
		Pending:     r.pending.Load(),
		Established: r.established.Load(),
		Failed:      r.failed.Load(),
		Ended:       r.ended.Load(),
		TxPackets:   tx,
		RxPackets:   rx,
		TxBytes:     txB,
		RxBytes:     rxB,
		Lost:        lost,
		StartedAt:   r.startedAt.Format(time.RFC3339),
	}
}

// launchLoop es el cerebro de la carga: cada "tick" (según cps) lanza una llamada
// nueva si aún no se alcanzó el objetivo de N concurrentes. Cuenta también las
// llamadas en vuelo (pending) para no disparar una ráfaga durante la subida.
func (g *Generator) launchLoop(ctx context.Context, r *run) {
	defer r.wg.Done()

	interval := time.Duration(float64(time.Second) / r.spec.CPS)
	if interval < time.Millisecond {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Tope total de llamadas, si se fijó: dejamos de lanzar nuevas.
			if r.spec.MaxCalls > 0 && r.launched.Load() >= r.spec.MaxCalls {
				continue
			}
			// Reponemos hasta alcanzar N establecidas (contando las en vuelo).
			if r.active.Load()+r.pending.Load() < int64(r.spec.Concurrent) {
				r.wg.Add(1)
				go g.worker(ctx, r)
			}
		}
	}
}

// worker ejecuta el ciclo de vida de UNA llamada de carga: INVITE -> 200 -> ACK,
// arranca la media y la mantiene viva hasta el STOP (ctx) o hasta que el otro
// extremo cuelgue (call.Done()). Actualiza los contadores agregados.
func (g *Generator) worker(ctx context.Context, r *run) {
	defer r.wg.Done()
	id := r.nextID.Add(1)
	r.launched.Add(1)
	r.pending.Add(1)

	// Plano de media (RTP): abrimos el socket y preparamos la oferta SDP (con el
	// puerto RTP real) que irá en el INVITE, tanto para el INVITE básico como para
	// el escenario (donde sustituye al body que el YAML pudiera definir).
	var sess *media.Session
	var mediaHdr map[string]string
	var mediaBody []byte
	if r.spec.WithMedia {
		if s, err := media.Open(g.core.LocalIP(), g.log); err != nil {
			g.log.Debug("carga: no se pudo abrir RTP; llamada sin audio", "error", err)
		} else {
			sess = s
			mediaBody = media.BuildOffer(g.core.LocalIP(), s.LocalPort())
			mediaHdr = map[string]string{"Content-Type": "application/sdp"}
		}
	}

	// Establecimiento: por escenario (señalización dirigida por el YAML, sostenida
	// hasta el STOP) o por el INVITE básico de la plantilla.
	var call *sipcore.UACCall
	var err error
	if r.spec.Scenario != nil {
		rn := runner.New(g.core, scenarioTarget(r.spec.Invite), g.log)
		// Establish hace INVITE -> respuestas -> ACK: la llamada queda establecida.
		call, err = rn.Establish(ctx, r.spec.Scenario, mediaHdr, mediaBody)
	} else {
		// Copia de la plantilla para no mutar la Spec compartida.
		inv := r.spec.Invite
		if mediaBody != nil {
			hdr := make(map[string]string, len(inv.Headers)+1)
			for k, v := range inv.Headers {
				hdr[k] = v
			}
			hdr["Content-Type"] = "application/sdp"
			inv.Headers = hdr
			inv.Body = mediaBody
		}
		call, err = g.core.DialInvite(ctx, inv)
	}
	if err != nil {
		r.pending.Add(-1)
		r.failed.Add(1)
		closeSession(sess)
		return
	}

	// El INVITE básico aún debe esperar la respuesta y enviar el ACK; con escenario
	// eso ya está hecho dentro de Establish.
	if r.spec.Scenario == nil {
		if err := call.WaitAnswer(ctx); err != nil {
			r.pending.Add(-1)
			r.failed.Add(1)
			closeSession(sess)
			return
		}
		if err := call.Ack(ctx); err != nil {
			r.pending.Add(-1)
			r.failed.Add(1)
			closeSession(sess)
			return
		}
	}

	// Establecida.
	r.pending.Add(-1)
	r.established.Add(1)
	r.active.Add(1)

	// Negociamos y arrancamos la media a partir del SDP de respuesta.
	if sess != nil && !g.startMedia(ctx, call, r, sess) {
		closeSession(sess)
		sess = nil
	}
	if sess != nil {
		r.sessMu.Lock()
		r.sessions[id] = sess
		r.sessMu.Unlock()
	}

	// Sostener la llamada hasta el STOP o hasta que el otro extremo la termine.
	select {
	case <-ctx.Done():
		// STOP / parada del agente: colgamos nosotros con BYE. Contexto propio para
		// que el BYE salga aunque el de la carga ya esté cancelado.
		byeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = call.Hangup(byeCtx)
		cancel()
	case <-call.Done():
		// El otro extremo colgó (o el diálogo terminó): no hay que enviar BYE.
	}

	if sess != nil {
		r.sessMu.Lock()
		delete(r.sessions, id)
		r.sessMu.Unlock()
		sess.Close()
	}
	r.active.Add(-1)
	r.ended.Add(1)
}

// startMedia negocia el códec/destino a partir del SDP de respuesta y arranca el
// RTP. Devuelve false si no se pudo negociar (la llamada sigue viva, sin audio).
func (g *Generator) startMedia(ctx context.Context, call *sipcore.UACCall, r *run, sess *media.Session) bool {
	answer := call.AnswerSDP()
	if len(answer) == 0 {
		return false
	}
	desc, err := media.Parse(answer)
	if err != nil {
		return false
	}
	pt, ok := media.ChooseCodec(desc)
	if !ok || desc.Port == 0 {
		return false
	}
	if len(r.spec.Audio) > 0 {
		sess.SetSource(media.NewPCMSource(r.spec.Audio))
	}
	return sess.Start(ctx, desc.ConnIP, desc.Port, pt, desc.PTime) == nil
}

// scenarioTarget construye la URI de destino (Request-URI) para el runner a partir
// del destino real de la Spec (el SBC/peer). El escenario aporta las identidades
// (From/To, cabeceras); aquí solo decidimos a dónde se envía de verdad el paquete.
func scenarioTarget(inv sipcore.RichInvite) string {
	uri := fmt.Sprintf("sip:%s:%d", inv.DestHost, inv.DestPort)
	if inv.Transport == "tcp" {
		uri += ";transport=tcp"
	}
	return uri
}

// closeSession cierra una sesión de media si no es nil (azúcar para los caminos de error).
func closeSession(s *media.Session) {
	if s != nil {
		s.Close()
	}
}
