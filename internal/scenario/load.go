// Carga de escenarios desde disco o desde memoria. Separado de la definición de
// tipos (scenario.go) para que la lógica de E/S y parseo quede aislada.
package scenario

import (
	"bytes"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Load lee un escenario desde un fichero YAML, lo parsea y lo valida.
func Load(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("leyendo escenario %s: %w", path, err)
	}
	sc, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("en %s: %w", path, err)
	}
	return sc, nil
}

// Parse interpreta los bytes YAML de un escenario y lo valida. Útil para tests y
// para cargar escenarios que no vienen de un fichero.
//
// Usamos KnownFields(true) para que un campo mal escrito (p. ej. "stpes:") sea un
// error explícito en lugar de ignorarse en silencio: es justo el tipo de fallo
// que en SIPp cuesta horas de depurar.
func Parse(data []byte) (*Scenario, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var sc Scenario
	if err := dec.Decode(&sc); err != nil {
		return nil, fmt.Errorf("YAML inválido: %w", err)
	}
	if err := sc.Validate(); err != nil {
		return nil, err
	}
	return &sc, nil
}
