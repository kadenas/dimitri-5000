// Paquete netutil: utilidades de red para descubrir las direcciones IP locales.
//
// dimitri-5000 necesita saber con qué IP local debe enviar la señalización SIP
// (aparece en las cabeceras Via/Contact). Aquí ofrecemos dos cosas:
//   - LocalIP: la IP "principal" (la de la tarjeta por la que el sistema saldría).
//   - ListIPv4: todas las IPv4 disponibles, para mostrárselas al usuario.
package netutil

import (
	"fmt"
	"net"
)

// LocalIP detecta la IP IPv4 que el sistema usaría para salir hacia el exterior,
// que normalmente es la de la tarjeta de red principal.
//
// Truco habitual: abrimos un socket UDP "hacia" una dirección externa. UDP no
// establece conexión ni envía paquetes al hacer Dial, pero el sistema operativo
// elige la interfaz de salida y nos deja leer su IP local. Funciona también sin
// internet, porque solo se consulta la tabla de rutas.
func LocalIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "", fmt.Errorf("no se pudo determinar la IP local: %w", err)
	}
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", fmt.Errorf("dirección local inesperada: %T", conn.LocalAddr())
	}
	return addr.IP.String(), nil
}

// ListIPv4 enumera las IPv4 no-loopback de las interfaces activas, en formato
// "192.168.1.50 (Ethernet)". Sirve para que el usuario vea qué IPs hay y pueda
// elegir una con --bind-ip si la autodetección no acierta.
func ListIPv4() []string {
	var out []string

	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}

	for _, ifc := range ifaces {
		// Saltamos interfaces caídas o de loopback (127.0.0.1).
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			// Solo IPv4 (descartamos IPv6 en la v1).
			if ip == nil || ip.To4() == nil {
				continue
			}
			out = append(out, fmt.Sprintf("%s (%s)", ip.String(), ifc.Name))
		}
	}
	return out
}
