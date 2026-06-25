// Media (RTP) del lado UAS: cuando este núcleo CONTESTA un INVITE con oferta SDP,
// negocia un códec G.711, abre una sesión RTP, responde 200 con la respuesta SDP y
// empieza a enviar/recibir audio. Las sesiones se indexan por Call-ID y se cierran
// con el BYE correspondiente (o al parar el núcleo).
//
// Nota de capas: este fichero usa internal/media (RTP puro, sin SIP). No rompe el
// principio "sipcore es la única capa que habla SIP": media NO importa sipgo; aquí
// solo unimos la señalización SIP entrante con su plano de media.
package sipcore

import (
	"context"

	"github.com/emiago/sipgo"
	"github.com/emiago/sipgo/sip"

	"github.com/kadenas/dimitri-5000/internal/media"
)

// EnableMedia activa el plano de media (RTP) para las llamadas entrantes. Debe
// llamarse antes de Serve (lo hace el agente al arrancar).
func (c *Core) EnableMedia() { c.mediaEnabled = true }

// SetMediaAudio fija el audio (PCM 8 kHz mono) que las llamadas ENTRANTES enviarán
// por RTP en lugar del tono. Un slice nil/vacío vuelve al tono por defecto.
func (c *Core) SetMediaAudio(pcm []int16) {
	c.mediaMu.Lock()
	c.mediaAudio = pcm
	c.mediaMu.Unlock()
}

// answerWithMedia intenta contestar un INVITE entrante con audio: parsea la oferta
// SDP, elige códec, abre la sesión RTP, responde 200 con la respuesta SDP y arranca
// el envío/recepción. Devuelve true si respondió con media; false si no había oferta
// válida o la media está desactivada (en cuyo caso el llamante responderá sin SDP).
func (c *Core) answerWithMedia(req *sip.Request, dlg *sipgo.DialogServerSession) bool {
	if !c.mediaEnabled {
		return false
	}
	offer := req.Body()
	if len(offer) == 0 {
		return false // INVITE sin SDP: contestamos sin media (señalización pura)
	}
	desc, err := media.Parse(offer)
	if err != nil {
		c.log.Warn("oferta SDP entrante no parseable; contesto sin media", "error", err)
		return false
	}
	pt, ok := media.ChooseCodec(desc)
	if !ok || desc.Port == 0 {
		c.log.Warn("oferta SDP sin códec G.711 común o puerto 0; contesto sin media")
		return false
	}

	sess, err := media.Open(c.bindIP, c.log)
	if err != nil {
		c.log.Warn("no se pudo abrir el socket RTP (UAS); contesto sin media", "error", err)
		return false
	}
	answer := media.BuildAnswer(c.bindIP, sess.LocalPort(), pt)

	// Si hay un audio cargado, lo enviamos en bucle en lugar del tono por defecto.
	c.mediaMu.Lock()
	audio := c.mediaAudio
	c.mediaMu.Unlock()
	if len(audio) > 0 {
		sess.SetSource(media.NewPCMSource(audio))
	}

	// Arrancamos la media ANTES de responder: ya conocemos el destino (de la oferta)
	// y el otro extremo ya escucha en ese puerto. El contexto es de fondo: la sesión
	// se cierra explícitamente con el BYE (closeMediaSession) o al parar el núcleo.
	if err := sess.Start(context.Background(), desc.ConnIP, desc.Port, pt, desc.PTime); err != nil {
		c.log.Warn("no se pudo iniciar la media (UAS); contesto sin media", "error", err)
		sess.Close()
		return false
	}

	id := callIDOf(req)
	c.storeMediaSession(id, sess)
	if err := dlg.RespondSDP(answer); err != nil {
		c.log.Error("error respondiendo 200 con SDP", "error", err)
		c.closeMediaSession(id)
		return false
	}
	return true
}

// callIDOf extrae el Call-ID de un mensaje SIP (clave de la sesión de media).
func callIDOf(msg sip.Message) string {
	if h := msg.CallID(); h != nil {
		return h.Value()
	}
	return ""
}

// storeMediaSession registra la sesión por Call-ID. Si ya había una (p. ej. un
// re-INVITE), cierra la anterior. Con id vacío, cierra la sesión (no podríamos
// localizarla luego para liberarla).
func (c *Core) storeMediaSession(id string, s *media.Session) {
	if id == "" {
		s.Close()
		return
	}
	c.mediaMu.Lock()
	if c.mediaSess == nil {
		c.mediaSess = make(map[string]*media.Session)
	}
	old := c.mediaSess[id]
	c.mediaSess[id] = s
	c.mediaMu.Unlock()
	if old != nil {
		old.Close()
	}
}

// closeMediaSession cierra y olvida la sesión de media de un Call-ID (no-op si no
// existe, p. ej. en un BYE de una llamada saliente, cuya media gestiona el control).
func (c *Core) closeMediaSession(id string) {
	if id == "" {
		return
	}
	c.mediaMu.Lock()
	s := c.mediaSess[id]
	delete(c.mediaSess, id)
	c.mediaMu.Unlock()
	if s != nil {
		s.Close()
	}
}

// closeAllMedia cierra todas las sesiones de media vivas (parada del núcleo).
func (c *Core) closeAllMedia() {
	c.mediaMu.Lock()
	sessions := c.mediaSess
	c.mediaSess = make(map[string]*media.Session)
	c.mediaMu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
}
