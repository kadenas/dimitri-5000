// Listado de escenarios disponibles en una carpeta del disco. Separado de la
// carga individual (load.go) porque es una responsabilidad distinta: aquí solo
// recorremos un directorio y resumimos cada escenario para que la web los pinte
// en un desplegable. La EJECUCIÓN no vive aquí (la hace el runner).
package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Info es el resumen de un escenario disponible en disco, pensado para la web.
// Las etiquetas json definen cómo viaja al desplegable de la interfaz.
type Info struct {
	File        string `json:"file"`            // nombre del fichero (base, sin ruta)
	Name        string `json:"name"`            // 'name' del escenario (vacío si no carga)
	Role        string `json:"role"`            // "uac" | "uas"
	Description string `json:"description"`     // descripción libre del escenario
	Error       string `json:"error,omitempty"` // motivo si el YAML no se pudo cargar
}

// List lee la carpeta dir y devuelve el resumen de cada escenario .yaml/.yml,
// ordenado por nombre de fichero. Un escenario que no parsea NO rompe el listado:
// se incluye con su campo Error, para que el usuario vea en la web qué fichero
// está mal y por qué (en lugar de que desaparezca en silencio).
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("leyendo la carpeta de escenarios %q: %w", dir, err)
	}

	out := make([]Info, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !esEscenarioYAML(name) {
			continue
		}

		info := Info{File: name}
		// Cargamos para extraer metadatos (name/role/description) y, de paso,
		// validar: si falla, lo reflejamos como Error sin abortar el listado.
		if sc, err := Load(filepath.Join(dir, name)); err != nil {
			info.Error = err.Error()
		} else {
			info.Name = sc.Name
			info.Role = sc.Role
			info.Description = sc.Description
		}
		out = append(out, info)
	}

	// Orden estable por nombre de fichero para que la lista no "baile".
	sort.Slice(out, func(i, j int) bool { return out[i].File < out[j].File })
	return out, nil
}

// esEscenarioYAML indica si el nombre de fichero tiene extensión de escenario.
func esEscenarioYAML(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml":
		return true
	default:
		return false
	}
}
