// Paquete media: implementa el plano de MEDIA (RTP) de las llamadas, separado por
// completo de la señalización SIP. No importa sipgo ni ninguna librería externa:
// la cabecera RTP (RFC 3550), los códecs G.711 (µ-law/A-law) y el SDP se hacen a
// mano. Así dimitri-5000 puede ofertar, enviar, recibir y MEDIR audio real sin
// añadir dependencias, manteniendo el principio de capas del proyecto: sipcore es
// la única capa que habla SIP; media es la única que habla RTP.
//
// Este fichero: la cabecera RTP de 12 bytes (RFC 3550 §5.1). Al GENERAR usamos la
// forma simple (sin CSRC ni extensiones, CC=0); al PARSEAR sí toleramos CSRC y la
// cabecera de extensión, porque otros emisores reales pueden incluirlos.
package media

import (
	"encoding/binary"
	"errors"
)

// Version es la versión RTP que emitimos y exigimos. La RFC 3550 fija la 2.
const Version = 2

// HeaderSize es el tamaño en bytes de la cabecera RTP básica (sin CSRC ni extensión).
const HeaderSize = 12

// Header modela los campos de la cabecera RTP que usamos. Al generar, CC=0,
// padding=0 y extensión=0 (no los necesitamos para enviar G.711).
type Header struct {
	Marker         bool   // bit M: marca un evento (p. ej. primer paquete de un talkspurt)
	PayloadType    uint8  // PT: 0 = PCMU (G.711 µ-law), 8 = PCMA (G.711 A-law)
	SequenceNumber uint16 // +1 por paquete; permite detectar pérdidas y reordenación
	Timestamp      uint32 // marca de muestreo (8000 Hz en G.711): +160 por trama de 20 ms
	SSRC           uint32 // identificador de la fuente de sincronización
}

// Marshal serializa la cabecera en 12 bytes en orden de red (big-endian).
func (h Header) Marshal() []byte {
	b := make([]byte, HeaderSize)
	// Byte 0: V(2) P(1) X(1) CC(4) → V=2, sin padding, sin extensión, CC=0.
	b[0] = Version << 6
	// Byte 1: M(1) PT(7).
	b[1] = h.PayloadType & 0x7F
	if h.Marker {
		b[1] |= 0x80
	}
	binary.BigEndian.PutUint16(b[2:4], h.SequenceNumber)
	binary.BigEndian.PutUint32(b[4:8], h.Timestamp)
	binary.BigEndian.PutUint32(b[8:12], h.SSRC)
	return b
}

// ParsePacket separa la cabecera RTP del payload de un datagrama recibido. Valida
// la longitud mínima y la versión, y descuenta los CSRC y la cabecera de extensión
// si vienen (otros emisores pueden incluirlos). Devuelve la cabecera y el payload
// (una sub-rebanada del buffer original, sin copiar).
func ParsePacket(pkt []byte) (Header, []byte, error) {
	if len(pkt) < HeaderSize {
		return Header{}, nil, errors.New("paquete RTP demasiado corto")
	}
	if pkt[0]>>6 != Version {
		return Header{}, nil, errors.New("versión RTP no soportada")
	}
	csrc := int(pkt[0] & 0x0F) // nº de CSRC: cada uno ocupa 4 bytes tras la cabecera
	hasExt := pkt[0]&0x10 != 0 // bit X: hay cabecera de extensión
	offset := HeaderSize + csrc*4
	if len(pkt) < offset {
		return Header{}, nil, errors.New("paquete RTP con lista CSRC incompleta")
	}
	h := Header{
		Marker:         pkt[1]&0x80 != 0,
		PayloadType:    pkt[1] & 0x7F,
		SequenceNumber: binary.BigEndian.Uint16(pkt[2:4]),
		Timestamp:      binary.BigEndian.Uint32(pkt[4:8]),
		SSRC:           binary.BigEndian.Uint32(pkt[8:12]),
	}
	// Cabecera de extensión (RFC 3550 §5.3.1): 4 bytes (id+longitud) + longitud*4.
	if hasExt {
		if len(pkt) < offset+4 {
			return Header{}, nil, errors.New("paquete RTP con extensión incompleta")
		}
		extWords := int(binary.BigEndian.Uint16(pkt[offset+2 : offset+4]))
		offset += 4 + extWords*4
		if len(pkt) < offset {
			return Header{}, nil, errors.New("paquete RTP con extensión incompleta")
		}
	}
	return h, pkt[offset:], nil
}
