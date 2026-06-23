// Paquete config: define la estructura de configuración de dimitri-5000 y su
// carga desde un fichero JSON.
//
// Para la v1 usamos JSON (de la librería estándar) en lugar de YAML para no
// añadir dependencias externas sin necesidad. Si más adelante quieres YAML
// (más cómodo de escribir y comentar a mano), se valora y se aprueba entonces.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Target representa una troncal / destino SIP a vigilar.
type Target struct {
	ID        string `json:"id"`        // identificador corto y único (ej: "trunk-operador")
	Name      string `json:"name"`      // nombre legible para la interfaz
	Host      string `json:"host"`      // IP o FQDN del destino
	Port      int    `json:"port"`      // puerto SIP (típicamente 5060)
	Transport string `json:"transport"` // "UDP" o "TCP" (en la v1, UDP)
}

// MonitorConfig agrupa los parámetros de comportamiento del faro.
type MonitorConfig struct {
	IntervalSeconds int `json:"interval_seconds"` // cada cuánto se envía OPTIONS
	TimeoutSeconds  int `json:"timeout_seconds"`  // cuánto se espera la respuesta
	FailThreshold   int `json:"fail_threshold"`   // fallos seguidos para marcar DOWN
}

// Interval devuelve el intervalo ya como time.Duration listo para usar.
func (m MonitorConfig) Interval() time.Duration {
	return time.Duration(m.IntervalSeconds) * time.Second
}

// Timeout devuelve el timeout por envío como time.Duration.
func (m MonitorConfig) Timeout() time.Duration {
	return time.Duration(m.TimeoutSeconds) * time.Second
}

// Config es la configuración completa de la aplicación.
type Config struct {
	BindIP  string        `json:"bind_ip"`  // IP local de origen para el SIP (Via/Contact)
	SIPPort int           `json:"sip_port"` // puerto SIP local (escucha UAS y origen UAC). 5060 por defecto
	Targets []Target      `json:"targets"`
	Monitor MonitorConfig `json:"monitor"`
}

// defaults devuelve una configuración mínima y razonable para arrancar SIN
// fichero (útil para una primera prueba en local).
func defaults() Config {
	return Config{
		BindIP:  "127.0.0.1",
		SIPPort: 5060, // puerto SIP estándar
		Monitor: MonitorConfig{
			IntervalSeconds: 5,
			TimeoutSeconds:  2,
			FailThreshold:   3,
		},
		Targets: []Target{
			{ID: "local", Name: "Centralita local", Host: "127.0.0.1", Port: 5060, Transport: "UDP"},
		},
	}
}

// Load carga la configuración. Si path está vacío, devuelve los valores por
// defecto. Si se indica un fichero, parte de los valores por defecto y los
// sobrescribe con lo que traiga el JSON (lo ausente conserva el valor por
// defecto).
func Load(path string) (Config, error) {
	cfg := defaults()
	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("leyendo %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parseando JSON de %s: %w", path, err)
	}
	return cfg, nil
}
