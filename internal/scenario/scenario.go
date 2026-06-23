// Paquete scenario: define el "lenguaje" de escenarios de prueba de dimitri-5000
// y su validación. Un escenario es una secuencia de pasos (una máquina de estados)
// que el runner (Fase 2, siguiente paso) ejecutará contra el núcleo SIP.
//
// Diseño del lenguaje (acordado):
//   - Formato YAML legible (alternativa al XML de SIPp).
//   - Placeholders de variables con llaves: {caller}, {call_id}, {header:To;tag}...
//   - Paso 'send' estructurado (headers/body) con escape 'raw' para control total.
//   - Paso 'recv' EXPLÍCITO: se declara cada mensaje esperado (100, 180, 200...),
//     igual que en SIPp; 'optional: true' para los que pueden no llegar.
//
// Este fichero contiene los TIPOS y la VALIDACIÓN. La carga desde disco vive en
// load.go. El paquete no importa sipgo: solo describe y valida.
package scenario

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration es un time.Duration que se lee desde YAML como texto ("3s", "500ms").
// time.Duration no se parsea solo desde YAML, así que añadimos el desempaquetado.
type Duration time.Duration

// UnmarshalYAML convierte cadenas tipo "3s" en una Duration. Da un error claro si
// el texto no es una duración válida de Go.
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return fmt.Errorf("se esperaba una duración como texto (ej: \"3s\"): %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("duración inválida %q (usa formato Go: 500ms, 3s, 1m): %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// Std devuelve la duración como time.Duration estándar.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Roles válidos de un escenario.
const (
	RoleUAC = "uac" // origina llamadas (envía INVITE)
	RoleUAS = "uas" // recibe llamadas (responde)
)

// Scenario es la definición completa de un escenario de prueba.
type Scenario struct {
	Name        string            `yaml:"name"`
	Role        string            `yaml:"role"`
	Description string            `yaml:"description"`
	Defaults    Defaults          `yaml:"defaults"`
	Variables   map[string]string `yaml:"variables"`
	Steps       []Step            `yaml:"steps"`
	Bodies      map[string]Body   `yaml:"bodies"`
	Inject      *Inject           `yaml:"inject"`
}

// Defaults agrupa valores comunes que aplican a todo el escenario salvo que un
// paso los sobrescriba.
type Defaults struct {
	Transport   string   `yaml:"transport"`    // "udp" | "tcp"
	RecvTimeout Duration `yaml:"recv_timeout"` // espera máxima por defecto en cada recv
}

// Step es un paso del escenario. Cada paso tiene UNA acción principal:
// send, recv, pause, o ser un paso de solo-acciones (save/log) ≈ "nop".
type Step struct {
	// Acción principal (exactamente una por paso).
	Send  string    `yaml:"send"`  // método (INVITE, ACK...) o, en UAS, código de respuesta (180, 200...)
	Recv  string    `yaml:"recv"`  // código de respuesta o método de petición que se espera
	Pause *Duration `yaml:"pause"` // espera fija

	// Modificadores comunes.
	Optional bool      `yaml:"optional"` // (recv) si no llega, no falla el escenario
	Timeout  *Duration `yaml:"timeout"`  // (recv) sobrescribe Defaults.RecvTimeout

	// Construcción del mensaje (send).
	Headers map[string]string `yaml:"headers"` // cabeceras que controla el usuario
	Body    string            `yaml:"body"`    // nombre de un body de Bodies
	Raw     string            `yaml:"raw"`     // escape: mensaje SIP crudo (control total)

	// Acciones (recv / nop).
	Save  map[string]string `yaml:"save"`  // capturas: variable -> "{header:...}" o "{regex:...}"
	Match map[string]string `yaml:"match"` // validaciones sobre el mensaje recibido
	Log   string            `yaml:"log"`   // mensaje a registrar al ejecutar el paso
}

// Body es un cuerpo de mensaje reutilizable (de momento, SDP).
type Body struct {
	Type    string `yaml:"type"`    // "sdp" (único soportado en v1)
	Media   string `yaml:"media"`   // alias de media: "g711"... (audio real en Fase 5)
	Content string `yaml:"content"` // contenido literal alternativo a Media
}

// Inject describe la inyección de datos por llamada desde un CSV (como SIPp).
type Inject struct {
	File  string `yaml:"file"`  // ruta al CSV; cada fila = una llamada
	Order string `yaml:"order"` // "sequential" | "random"
}

// StepKind clasifica el tipo de paso tras validar.
type StepKind string

const (
	KindSend  StepKind = "send"
	KindRecv  StepKind = "recv"
	KindPause StepKind = "pause"
	KindNop   StepKind = "nop" // solo acciones (save/log)
)

// Kind deduce el tipo de paso a partir de sus campos. Asume un paso ya validado
// (Validate garantiza que solo hay una acción principal).
func (s Step) Kind() StepKind {
	switch {
	case s.Send != "":
		return KindSend
	case s.Recv != "":
		return KindRecv
	case s.Pause != nil:
		return KindPause
	default:
		return KindNop
	}
}

// Validate comprueba que el escenario es coherente y devuelve un error legible
// (con el número de paso) en cuanto encuentra el primer problema. Mantener los
// mensajes claros es deliberado: un escenario mal escrito debe decir QUÉ falla.
func (sc *Scenario) Validate() error {
	if strings.TrimSpace(sc.Name) == "" {
		return fmt.Errorf("el escenario no tiene 'name'")
	}
	if sc.Role != RoleUAC && sc.Role != RoleUAS {
		return fmt.Errorf("'role' debe ser %q o %q (encontrado: %q)", RoleUAC, RoleUAS, sc.Role)
	}
	if t := strings.ToLower(sc.Defaults.Transport); t != "" && t != "udp" && t != "tcp" {
		return fmt.Errorf("defaults.transport debe ser 'udp' o 'tcp' (encontrado: %q)", sc.Defaults.Transport)
	}
	if len(sc.Steps) == 0 {
		return fmt.Errorf("el escenario no tiene pasos ('steps')")
	}
	if sc.Inject != nil {
		if strings.TrimSpace(sc.Inject.File) == "" {
			return fmt.Errorf("inject.file no puede estar vacío")
		}
		if o := sc.Inject.Order; o != "" && o != "sequential" && o != "random" {
			return fmt.Errorf("inject.order debe ser 'sequential' o 'random' (encontrado: %q)", o)
		}
	}

	for i, st := range sc.Steps {
		paso := i + 1 // numeración humana (empieza en 1)
		if err := st.validate(sc); err != nil {
			return fmt.Errorf("paso %d: %w", paso, err)
		}
	}
	return nil
}

// validate comprueba un único paso en el contexto de su escenario.
func (s Step) validate(sc *Scenario) error {
	// Contar las acciones principales declaradas: debe haber como mucho una.
	acciones := 0
	if s.Send != "" {
		acciones++
	}
	if s.Recv != "" {
		acciones++
	}
	if s.Pause != nil {
		acciones++
	}
	if acciones > 1 {
		return fmt.Errorf("un paso solo puede tener una acción (send, recv o pause)")
	}

	switch s.Kind() {
	case KindSend:
		// 'send' puede ser un método (INVITE...) o, en UAS, un código de respuesta.
		if isStatusCode(s.Send) {
			if sc.Role != RoleUAS {
				return fmt.Errorf("send de un código de respuesta (%s) solo tiene sentido en role uas", s.Send)
			}
		} else if !isMethod(s.Send) {
			return fmt.Errorf("send %q no es ni un método SIP ni un código de respuesta", s.Send)
		}
		if s.Body != "" {
			if _, ok := sc.Bodies[s.Body]; !ok {
				return fmt.Errorf("body %q no está definido en 'bodies'", s.Body)
			}
		}

	case KindRecv:
		// 'recv' espera un código de respuesta o un método de petición.
		if !isStatusCode(s.Recv) && !isMethod(s.Recv) {
			return fmt.Errorf("recv %q no es ni un código de respuesta ni un método SIP", s.Recv)
		}

	case KindNop:
		// Paso de solo-acciones: debe tener al menos save o log; si no, está vacío.
		if len(s.Save) == 0 && strings.TrimSpace(s.Log) == "" {
			return fmt.Errorf("paso vacío: indica send, recv, pause o al menos save/log")
		}
	}

	return nil
}

// isStatusCode indica si s es un código de respuesta SIP (3 dígitos, 100-699).
func isStatusCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s[0] >= '1' && s[0] <= '6'
}

// metodosSIP son los métodos que reconocemos (los habituales de RFC 3261 y extensiones).
var metodosSIP = map[string]bool{
	"INVITE": true, "ACK": true, "BYE": true, "CANCEL": true, "OPTIONS": true,
	"REGISTER": true, "INFO": true, "PRACK": true, "UPDATE": true, "MESSAGE": true,
	"SUBSCRIBE": true, "NOTIFY": true, "REFER": true, "PUBLISH": true,
}

// isMethod indica si s es un método SIP reconocido (en mayúsculas, como en el protocolo).
func isMethod(s string) bool {
	return metodosSIP[s]
}
