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
	"math"
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
	CallDur    time.Duration      // duración de cada llamada: cumplida, colgamos NOSOTROS con BYE y el loop la repone (churn). 0 = indefinida (hasta STOP o BYE remoto)
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
	CallSecs    float64 `json:"call_secs,omitempty"` // duración configurada por llamada, en segundos (0 = indefinida)
	WithMedia   bool    `json:"with_media"`  // si se envía RTP
	Scenario    string  `json:"scenario,omitempty"` // nombre del escenario por llamada (vacío = INVITE básico)
	Launched    int64   `json:"launched"`    // INVITEs enviados (acumulado)
	Active      int64   `json:"active"`      // establecidas vivas AHORA
	Pending     int64   `json:"pending"`     // en curso (dialing/ringing) AHORA
	Established int64   `json:"established"` // total que llegaron a establecidas
	Failed      int64   `json:"failed"`      // total fallidas (rechazo/timeout/error)
	Cancelled   int64   `json:"cancelled"`   // en vuelo abortadas por el STOP/parada (NO son fallos del sistema bajo prueba)
	Ended       int64   `json:"ended"`       // total terminadas (BYE/caída)

	// Desglose de Failed por causa: código SIP de rechazo ("486", "503"...),
	// "timeout" (nadie contestó) o "error" (transporte/escenario). Es lo que
	// permite leer una prueba contra un SBC: no es lo mismo que rechace el
	// destino a que el SBC esté saturado.
	FailedBy map[string]int64 `json:"failed_by,omitempty"`

	// PDD (Post-Dial Delay): latencia INVITE -> respuesta final 2xx, en ms,
	// sobre las llamadas establecidas. 0 si aún no hay muestras.
	PDDMinMs float64 `json:"pdd_min_ms"`
	PDDAvgMs float64 `json:"pdd_avg_ms"`
	PDDMaxMs float64 `json:"pdd_max_ms"`
	TxPackets   uint64  `json:"tx_packets"`  // RTP enviado (agregado de las activas)
	RxPackets   uint64  `json:"rx_packets"`  // RTP recibido (agregado)
	TxBytes     uint64  `json:"tx_bytes"`
	RxBytes     uint64  `json:"rx_bytes"`
	Lost        int64   `json:"lost"` // pérdida RTP agregada
	StartedAt   string  `json:"started_at,omitempty"`
	FinishedAt  string  `json:"finished_at,omitempty"` // cuándo terminó (solo en la foto final)
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
	cancelled   atomic.Int64
	ended       atomic.Int64

	failMu sync.Mutex
	failBy map[string]int64 // causa -> total (desglose de failed)

	pddMu  sync.Mutex
	pddN   int64
	pddSum time.Duration
	pddMin time.Duration
	pddMax time.Duration

	sessMu   sync.Mutex
	sessions map[uint64]*media.Session // activas con media, para agregar métricas
	nextID   atomic.Uint64

	// Acumulados RTP de las sesiones YA cerradas. Sin esto, la foto perdería el
	// tráfico de cada llamada al colgarla (los contadores "bajarían" durante la
	// prueba y la foto final saldría a cero).
	txPackets atomic.Uint64
	rxPackets atomic.Uint64
	txBytes   atomic.Uint64
	rxBytes   atomic.Uint64
	lost      atomic.Int64
}

// abortPending cierra la cuenta de una llamada que NO llegó a establecerse. Si el
// contexto de la carga ya está cancelado, el aborto lo provocó nuestro STOP (o la
// parada del agente): se cuenta como cancelada, NO como fallo — el sistema bajo
// prueba no tuvo la culpa. El resto son fallos reales, con su causa desglosada.
func (r *run) abortPending(ctx context.Context, err error) {
	r.pending.Add(-1)
	if ctx.Err() != nil {
		r.cancelled.Add(1)
		return
	}
	r.failed.Add(1)
	r.countFail(sipcore.FailureCause(err))
}

// countFail registra un fallo de establecimiento con su causa (código SIP,
// "timeout", "error"). El total ya lo lleva el contador atómico failed; aquí
// solo se mantiene el desglose.
func (r *run) countFail(causa string) {
	r.failMu.Lock()
	if r.failBy == nil {
		r.failBy = make(map[string]int64)
	}
	r.failBy[causa]++
	r.failMu.Unlock()
}

// observePDD registra la latencia de establecimiento (INVITE -> 2xx) de una
// llamada contestada, para el min/avg/max agregado.
func (r *run) observePDD(d time.Duration) {
	r.pddMu.Lock()
	r.pddN++
	r.pddSum += d
	if r.pddMin == 0 || d < r.pddMin {
		r.pddMin = d
	}
	if d > r.pddMax {
		r.pddMax = d
	}
	r.pddMu.Unlock()
}

// addSessionMetrics vuelca a los acumulados las métricas de una sesión que se
// cierra. Llamar DESPUÉS de Close(): los contadores ya no crecen y no se pierde
// ningún paquete entre la lectura y el cierre.
func (r *run) addSessionMetrics(s *media.Session) {
	m := s.Metrics()
	r.txPackets.Add(m.TxPackets)
	r.rxPackets.Add(m.RxPackets)
	r.txBytes.Add(m.TxBytes)
	r.rxBytes.Add(m.RxBytes)
	r.lost.Add(m.Lost)
}

// stats construye la foto agregada de esta ejecución: contadores de llamadas y
// RTP (acumulado de las cerradas + lo que llevan las vivas).
func (r *run) stats() Stats {
	tx, rx := r.txPackets.Load(), r.rxPackets.Load()
	txB, rxB := r.txBytes.Load(), r.rxBytes.Load()
	lost := r.lost.Load()
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

	// Copia del desglose de fallos (el mapa vivo sigue mutando en los workers).
	var failBy map[string]int64
	r.failMu.Lock()
	if len(r.failBy) > 0 {
		failBy = make(map[string]int64, len(r.failBy))
		for k, v := range r.failBy {
			failBy[k] = v
		}
	}
	r.failMu.Unlock()

	// PDD agregado, en milisegundos.
	var pddMin, pddAvg, pddMax float64
	r.pddMu.Lock()
	if r.pddN > 0 {
		pddMin = redondeaMs(r.pddMin)
		pddAvg = redondeaMs(r.pddSum / time.Duration(r.pddN))
		pddMax = redondeaMs(r.pddMax)
	}
	r.pddMu.Unlock()

	return Stats{
		Running:     true,
		Stopping:    r.stopping.Load(),
		Target:      r.spec.Concurrent,
		CPS:         r.spec.CPS,
		MaxCalls:    r.spec.MaxCalls,
		CallSecs:    r.spec.CallDur.Seconds(),
		WithMedia:   r.spec.WithMedia,
		Scenario:    scName,
		Launched:    r.launched.Load(),
		Active:      r.active.Load(),
		Pending:     r.pending.Load(),
		Established: r.established.Load(),
		Failed:      r.failed.Load(),
		Cancelled:   r.cancelled.Load(),
		Ended:       r.ended.Load(),
		FailedBy:    failBy,
		PDDMinMs:    pddMin,
		PDDAvgMs:    pddAvg,
		PDDMaxMs:    pddMax,
		TxPackets:   tx,
		RxPackets:   rx,
		TxBytes:     txB,
		RxBytes:     rxB,
		Lost:        lost,
		StartedAt:   r.startedAt.Format(time.RFC3339),
	}
}

// redondeaMs pasa una duración a milisegundos con 2 decimales (legible en la web
// sin perder las décimas, que en LAN son toda la medida).
func redondeaMs(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*100) / 100
}

// Generator lanza y sostiene la carga sobre el Core de un agente. Una sola
// ejecución a la vez (la siguiente espera a que la anterior termine de drenar).
type Generator struct {
	core *sipcore.Core
	log  *slog.Logger

	mu   sync.Mutex
	cur  *run   // nil si no hay carga activa ni drenando
	last *Stats // foto FINAL de la última ejecución terminada (nil si nunca hubo)
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
	if spec.CallDur < 0 {
		spec.CallDur = 0 // negativa no tiene sentido: indefinida
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
		g.retire(r)
		g.log.Info("carga detenida (todas las llamadas colgadas)")
	}()
}

// finish termina una ejecución que acabó POR SÍ SOLA (MaxCalls alcanzado y ya sin
// llamadas vivas): espera al resto de goroutines y la retira dejando la foto final.
func (g *Generator) finish(r *run) {
	r.cancel() // libera el contexto (los workers ya terminaron)
	r.wg.Wait()
	g.retire(r)
	g.log.Info("carga completada (tope de llamadas alcanzado)")
}

// retire captura la foto FINAL de la ejecución (visible en Snapshot hasta la
// siguiente carga) y libera el hueco para poder arrancar otra. Pueden llamarla
// el drenaje del STOP y el autofin de MaxCalls: solo la primera tiene efecto.
func (g *Generator) retire(r *run) {
	st := r.stats()
	st.Running = false
	st.Stopping = false
	st.FinishedAt = time.Now().Format(time.RFC3339)
	g.mu.Lock()
	if g.cur == r {
		g.last = &st
		g.cur = nil
	}
	g.mu.Unlock()
}

// Running indica si hay una prueba en curso (incluida la fase de drenaje del STOP).
func (g *Generator) Running() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.cur != nil
}

// Snapshot devuelve la foto agregada actual. Sin carga en curso, devuelve la foto
// FINAL de la última ejecución (Running=false, FinishedAt relleno): así el operador
// no pierde los resultados justo cuando la prueba termina.
func (g *Generator) Snapshot() Stats {
	g.mu.Lock()
	r, last := g.cur, g.last
	g.mu.Unlock()
	if r == nil {
		if last != nil {
			return *last
		}
		return Stats{Running: false}
	}
	return r.stats()
}

// launchLoop es el cerebro de la carga: a cada vuelta acredita las "fichas" de
// lanzamiento devengadas por el tiempo real transcurrido (token bucket, ver
// bucket.go) y lanza una llamada por ficha mientras no se alcance el objetivo
// de N concurrentes (contando las en vuelo, para no disparar ráfagas en la
// subida). El tick es FIJO (20 ms): la cps la marca el bucket, no el ticker,
// así que ni hay techo de ~1000 cps ni se pierden llamadas si el loop se
// retrasa (el tiempo transcurrido las devenga igual).
func (g *Generator) launchLoop(ctx context.Context, r *run) {
	defer r.wg.Done()

	b := newBucket(r.spec.CPS)
	last := time.Now()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			b.suma(now.Sub(last))
			last = now
			// Tope total alcanzado: no se lanzan nuevas y, cuando termine la última
			// viva, la prueba acaba SOLA (foto final en Snapshot, hueco libre).
			if r.spec.MaxCalls > 0 && r.launched.Load() >= r.spec.MaxCalls {
				if r.active.Load()+r.pending.Load() == 0 {
					go g.finish(r) // en otra goroutine: finish espera también a este loop
					return
				}
				continue
			}
			// Reponemos hasta alcanzar N establecidas (contando las en vuelo), una
			// llamada por ficha. toma() va la ÚLTIMA en la condición: si no toca
			// lanzar, la ficha no se gasta (queda acumulada, con su techo de ráfaga).
			// launched/pending se cuentan AQUÍ y no en el worker: si se contaran allí,
			// varias vueltas podrían colarse antes de que el worker arranque y se
			// sobrepasaría MaxCalls (y el autofin de arriba vería un 0 falso).
			for (r.spec.MaxCalls == 0 || r.launched.Load() < r.spec.MaxCalls) &&
				r.active.Load()+r.pending.Load() < int64(r.spec.Concurrent) &&
				b.toma() {
				r.launched.Add(1)
				r.pending.Add(1)
				r.wg.Add(1)
				go g.worker(ctx, r)
			}
		}
	}
}

// worker ejecuta el ciclo de vida de UNA llamada de carga: INVITE -> 200 -> ACK,
// arranca la media y la mantiene viva hasta el STOP (ctx) o hasta que el otro
// extremo cuelgue (call.Done()). Actualiza los contadores agregados; launched y
// pending ya los contó el loop de lanzamiento.
func (g *Generator) worker(ctx context.Context, r *run) {
	defer r.wg.Done()
	id := r.nextID.Add(1)

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
	t0 := time.Now() // arranque del PDD (INVITE -> 2xx)
	var call *sipcore.UACCall
	var err error
	if r.spec.Scenario != nil {
		rn := runner.New(g.core, scenarioTarget(r.spec.Invite), g.log)
		// Los números A/B del panel pisan {caller}/{callee} del YAML: TODAS las
		// llamadas de la prueba salen con la misma numeración (enrutable en el SBC).
		rn.Vars = identityVars(r.spec.Invite)
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
		r.abortPending(ctx, err)
		closeSession(sess)
		return
	}

	// El INVITE básico aún debe esperar la respuesta y enviar el ACK; con escenario
	// eso ya está hecho dentro de Establish.
	if r.spec.Scenario == nil {
		if err := call.WaitAnswer(ctx); err != nil {
			r.abortPending(ctx, err)
			closeSession(sess)
			return
		}
		r.observePDD(time.Since(t0)) // contestada: el PDD llega hasta el 2xx
		if err := call.Ack(ctx); err != nil {
			r.abortPending(ctx, err)
			closeSession(sess)
			return
		}
	} else {
		// Establish ya validó respuestas y envió el ACK (coste ~0 sobre el 2xx).
		r.observePDD(time.Since(t0))
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

	// Sostener la llamada hasta el STOP, hasta que venza su duración (churn) o
	// hasta que el otro extremo la termine.
	var caduca <-chan time.Time
	if r.spec.CallDur > 0 {
		tmr := time.NewTimer(r.spec.CallDur)
		defer tmr.Stop()
		caduca = tmr.C // canal nil si no hay duración: ese case nunca dispara
	}
	select {
	case <-ctx.Done():
		// STOP / parada del agente: colgamos nosotros con BYE. Contexto propio para
		// que el BYE salga aunque el de la carga ya esté cancelado.
		byeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = call.Hangup(byeCtx)
		cancel()
	case <-caduca:
		// Duración cumplida: BYE nuestro y hueco libre — el loop repondrá con una
		// llamada NUEVA (churn continuo: cada una con su INVITE, su PDD y su media).
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
		// Con la sesión ya cerrada (contadores quietos), su RTP pasa al acumulado:
		// la foto de la prueba no pierde el tráfico de las llamadas colgadas.
		r.addSessionMetrics(sess)
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
// (From/To, cabeceras); aquí decidimos a dónde se envía de verdad el paquete y,
// si hay número B, lo ponemos como user del Request-URI: es el campo por el que
// un SBC/Kamailio/PBX enruta la llamada.
func scenarioTarget(inv sipcore.RichInvite) string {
	uri := "sip:"
	if inv.ToUser != "" {
		uri += inv.ToUser + "@"
	}
	uri += fmt.Sprintf("%s:%d", inv.DestHost, inv.DestPort)
	if inv.Transport == "tcp" {
		uri += ";transport=tcp"
	}
	return uri
}

// identityVars traduce los números A/B del panel a variables de escenario
// ({caller}/{callee}, la convención documentada en SCENARIO_FORMAT.md). Solo se
// imponen los que el operador rellenó; devolver nil deja el YAML intacto.
func identityVars(inv sipcore.RichInvite) map[string]string {
	vars := make(map[string]string, 2)
	if inv.FromUser != "" {
		vars["caller"] = inv.FromUser
	}
	if inv.ToUser != "" {
		vars["callee"] = inv.ToUser
	}
	if len(vars) == 0 {
		return nil
	}
	return vars
}

// closeSession cierra una sesión de media si no es nil (azúcar para los caminos de error).
func closeSession(s *media.Session) {
	if s != nil {
		s.Close()
	}
}
