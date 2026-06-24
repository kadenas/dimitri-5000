// Traza global de mensajes SIP. sipgo expone SIPDebugTracer: un hook único de
// proceso que recibe CADA mensaje leído o escrito por la capa de transporte (con
// dirección, transporte, direcciones y bytes crudos). Lo usamos para alimentar el
// diagrama de escalera (ladder) de la web.
//
// Mantiene la regla de capas: sipgo solo se toca aquí. Hacia fuera exponemos un
// callback neutro (TraceFunc) sin tipos de sipgo, para que el paquete trace y la
// web no dependan de la librería.
package sipcore

import "github.com/emiago/sipgo/sip"

// TraceFunc recibe cada mensaje SIP: dir es "in" (recibido) u "out" (enviado);
// transport p. ej. "UDP"; laddr/raddr son "ip:puerto" local y remoto; raw es el
// mensaje completo en bytes.
type TraceFunc func(dir, transport, laddr, raddr string, raw []byte)

// traceAdapter implementa la interfaz sip.SIPTracer de sipgo y reenvía cada
// mensaje al callback neutro.
type traceAdapter struct {
	fn TraceFunc
}

func (t traceAdapter) SIPTraceRead(transport, laddr, raddr string, msg []byte) {
	t.fn("in", transport, laddr, raddr, msg)
}

func (t traceAdapter) SIPTraceWrite(transport, laddr, raddr string, msg []byte) {
	t.fn("out", transport, laddr, raddr, msg)
}

// EnableTracing activa la captura global de mensajes SIP y enruta cada uno al
// callback dado. Debe llamarse una vez al arrancar (afecta a TODO el proceso, es
// decir, a todos los agentes).
func EnableTracing(fn TraceFunc) {
	sip.SIPDebug = true // activa el reporte de mensajes en la capa de transporte
	sip.SIPDebugTracer(traceAdapter{fn: fn})
}
