// Sesión de media RTP: el socket UDP por el que enviamos audio G.711 (un tono
// sintético) y recibimos el del otro extremo, midiendo tráfico, pérdida y jitter.
// No conoce SIP: la negociación (SDP) la hace la capa superior, que abre la sesión
// para conocer el puerto local (la oferta) y luego la arranca con el destino real.
package media

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

const sampleRate = 8000 // Hz, fijo para G.711

// toneFreq y toneAmp definen el tono sintético que enviamos: 440 Hz a amplitud
// moderada. Ejercita el códec de verdad y, cuando en 5.3 grabemos/escuchemos la
// llamada, se oirá un tono claro en lugar de silencio.
const (
	toneFreq = 440.0
	toneAmp  = 6000.0
)

// Metrics es la foto de las métricas de una sesión RTP (viaja a la web como JSON).
type Metrics struct {
	LocalPort  int     `json:"local_port"`
	RemoteAddr string  `json:"remote_addr"`
	Codec      string  `json:"codec"`
	TxPackets  uint64  `json:"tx_packets"`
	TxBytes    uint64  `json:"tx_bytes"`
	RxPackets  uint64  `json:"rx_packets"`
	RxBytes    uint64  `json:"rx_bytes"`
	Lost       int64   `json:"lost"`
	JitterMs   float64 `json:"jitter_ms"`
}

// Session es una sesión de media RTP sobre un socket UDP.
type Session struct {
	log   *slog.Logger
	conn  *net.UDPConn
	local int       // puerto UDP local (se anuncia en el SDP)
	start time.Time // referencia temporal para el cálculo de jitter
	ssrc  uint32

	pt    uint8  // payload negociado (fijado en Start, leído por los bucles)
	ptime int    // ms por trama (fijado en Start)
	src   Source // audio que enviamos (tono por defecto; WAV subido si se fija)

	mu       sync.Mutex
	remote   *net.UDPAddr
	codec    string
	sendMute bool // true en HOLD: el bucle de envío salta la emisión de RTP

	// Métricas y contabilidad de recepción (protegidas por mu).
	txPackets, txBytes uint64
	rxPackets, rxBytes uint64
	rxInit             bool
	baseSeq            uint16
	maxSeq             uint16
	cycles             uint32
	received           uint64
	jitterInit         bool
	transit            float64
	jitter             float64

	cancel context.CancelFunc
	wg     sync.WaitGroup
	closed bool
}

// Open abre el socket RTP local en localIP con un puerto efímero asignado por el
// SO; lo leemos para anunciarlo en el SDP. (RTP suele usar puertos pares, pero
// para una herramienta de prueba basta con anunciar el que nos den.)
func Open(localIP string, log *slog.Logger) (*Session, error) {
	if log == nil {
		log = slog.Default()
	}
	ip := net.ParseIP(localIP)
	if ip == nil {
		return nil, fmt.Errorf("IP local de media inválida: %q", localIP)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("abriendo socket RTP: %w", err)
	}
	return &Session{
		log:   log,
		conn:  conn,
		local: conn.LocalAddr().(*net.UDPAddr).Port,
		start: time.Now(),
		ssrc:  rand.Uint32(),
		src:   NewToneSource(toneFreq, toneAmp), // por defecto, tono
	}, nil
}

// LocalPort devuelve el puerto UDP local donde escucha el RTP (para el SDP).
func (s *Session) LocalPort() int { return s.local }

// SetSource fija el audio a enviar (p. ej. un WAV subido). Debe llamarse ANTES de
// Start; si src es nil, se mantiene la fuente por defecto (el tono).
func (s *Session) SetSource(src Source) {
	if src != nil {
		s.src = src
	}
}

// SetSending activa o pausa la emisión de RTP en caliente. En HOLD (sending=false)
// el bucle de envío sigue vivo pero no emite paquetes (coherente con anunciar
// a=inactive); RESUME (sending=true) reanuda la emisión. La recepción y las
// métricas no se ven afectadas.
func (s *Session) SetSending(sending bool) {
	s.mu.Lock()
	s.sendMute = !sending
	s.mu.Unlock()
}

// Start fija el destino y el códec negociados y lanza los bucles de envío y
// recepción. ptime<=0 usa 20 ms. Debe llamarse una sola vez, tras negociar el SDP.
func (s *Session) Start(ctx context.Context, remoteIP string, remotePort int, pt uint8, ptime int) error {
	rip := net.ParseIP(remoteIP)
	if rip == nil {
		return fmt.Errorf("IP remota de media inválida: %q", remoteIP)
	}
	if ptime <= 0 {
		ptime = 20
	}
	s.mu.Lock()
	s.remote = &net.UDPAddr{IP: rip, Port: remotePort}
	s.codec = CodecName(pt)
	s.mu.Unlock()
	s.pt = pt
	s.ptime = ptime

	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.wg.Add(2)
	go s.sendLoop(ctx)
	go s.recvLoop(ctx)
	s.log.Info("media RTP iniciada", "local_port", s.local, "remote", s.remote.String(), "codec", s.codec)
	return nil
}

// sendLoop emite una trama RTP cada ptime ms con un tono G.711 sintético.
func (s *Session) sendLoop(ctx context.Context) {
	defer s.wg.Done()

	samplesPerFrame := sampleRate * s.ptime / 1000 // 160 muestras para 20 ms
	encode := encoderFor(s.pt)
	frame := make([]int16, samplesPerFrame)  // muestras PCM de cada trama
	payload := make([]byte, samplesPerFrame) // esas muestras codificadas a G.711

	ticker := time.NewTicker(time.Duration(s.ptime) * time.Millisecond)
	defer ticker.Stop()

	seq := uint16(rand.Uint32())
	ts := rand.Uint32()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// En HOLD no emitimos: avanzamos el timestamp para reflejar el tiempo
			// transcurrido (el reloj de muestreo no se detiene), pero NO el número de
			// secuencia (es contiguo a los paquetes realmente enviados). Así, al
			// reanudar, el RTP continúa con seq+1 y un timestamp coherente.
			s.mu.Lock()
			muted := s.sendMute
			s.mu.Unlock()
			if muted {
				ts += uint32(samplesPerFrame)
				continue
			}

			s.src.NextFrame(frame)
			for i := 0; i < samplesPerFrame; i++ {
				payload[i] = encode(frame[i])
			}
			h := Header{PayloadType: s.pt, SequenceNumber: seq, Timestamp: ts, SSRC: s.ssrc}
			pkt := append(h.Marshal(), payload...)

			s.mu.Lock()
			raddr := s.remote
			s.mu.Unlock()

			if _, err := s.conn.WriteToUDP(pkt, raddr); err != nil {
				if ctx.Err() != nil {
					return // socket cerrado por una parada ordenada
				}
				s.log.Debug("error enviando RTP", "error", err)
				continue
			}
			s.mu.Lock()
			s.txPackets++
			s.txBytes += uint64(len(pkt))
			s.mu.Unlock()

			seq++
			ts += uint32(samplesPerFrame)
		}
	}
}

// recvLoop lee paquetes RTP entrantes y actualiza rx, pérdida y jitter. Usa un
// deadline corto para poder atender la cancelación del contexto.
func (s *Session) recvLoop(ctx context.Context) {
	defer s.wg.Done()
	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return
		}
		_ = s.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		nb, _, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue // sin tráfico en esta ventana: reintentamos
			}
			if ctx.Err() != nil {
				return
			}
			s.log.Debug("error recibiendo RTP", "error", err)
			continue
		}
		h, _, err := ParsePacket(buf[:nb])
		if err != nil {
			continue // no es RTP válido: lo ignoramos
		}
		s.mu.Lock()
		s.rxPackets++
		s.rxBytes += uint64(nb)
		s.updateLossLocked(h.SequenceNumber)
		s.updateJitterLocked(h.Timestamp)
		s.mu.Unlock()
	}
}

// updateLossLocked mantiene el número de secuencia máximo (con cuenta de ciclos de
// 16 bits) y los paquetes recibidos, siguiendo la lógica de la RFC 3550 (A.1).
// Debe llamarse con s.mu tomado.
func (s *Session) updateLossLocked(seq uint16) {
	if !s.rxInit {
		s.rxInit = true
		s.baseSeq = seq
		s.maxSeq = seq
		s.received = 1
		return
	}
	s.received++
	udelta := seq - s.maxSeq // aritmética de 16 bits (con wrap)
	if udelta < 0x8000 {
		// Avance "hacia delante" (en orden, quizá con hueco): si numéricamente
		// bajó es que el contador de 16 bits dio la vuelta.
		if seq < s.maxSeq {
			s.cycles += 0x10000
		}
		s.maxSeq = seq
	}
	// udelta >= 0x8000: paquete viejo (reordenado/duplicado); no movemos el máximo.
}

// lostLocked estima los paquetes perdidos: esperados (rango de secuencia) menos
// recibidos. Puede ser negativo si hubo duplicados. Debe llamarse con s.mu tomado.
func (s *Session) lostLocked() int64 {
	if !s.rxInit {
		return 0
	}
	extMax := uint64(s.cycles) + uint64(s.maxSeq)
	expected := int64(extMax) - int64(s.baseSeq) + 1
	return expected - int64(s.received)
}

// updateJitterLocked actualiza el jitter de interarribo (RFC 3550 §6.4.1). El
// tiempo de llegada se expresa en unidades de timestamp RTP (8000 Hz). El offset
// constante entre ambos relojes se cancela al usar solo la diferencia D.
func (s *Session) updateJitterLocked(rtpTS uint32) {
	arrival := time.Since(s.start).Seconds() * sampleRate
	transit := arrival - float64(rtpTS)
	if s.jitterInit {
		d := transit - s.transit
		if d < 0 {
			d = -d
		}
		s.jitter += (d - s.jitter) / 16
	} else {
		s.jitterInit = true
	}
	s.transit = transit
}

// Metrics devuelve una foto consistente de las métricas de la sesión.
func (s *Session) Metrics() Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	remote := ""
	if s.remote != nil {
		remote = s.remote.String()
	}
	return Metrics{
		LocalPort:  s.local,
		RemoteAddr: remote,
		Codec:      s.codec,
		TxPackets:  s.txPackets,
		TxBytes:    s.txBytes,
		RxPackets:  s.rxPackets,
		RxBytes:    s.rxBytes,
		Lost:       s.lostLocked(),
		JitterMs:   s.jitter / 8, // unidades RTP (8 kHz) → milisegundos
	}
}

// Close detiene los bucles y cierra el socket. Es idempotente y seguro aunque la
// sesión nunca llegara a arrancar (Open sin Start).
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()

	if s.cancel != nil {
		s.cancel()
	}
	_ = s.conn.Close() // desbloquea recvLoop
	s.wg.Wait()
}
