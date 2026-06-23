# dimitri-5000

Herramienta profesional de pruebas SIP/VoIP: lanza y recibe llamadas, ejecuta
escenarios reproducibles y realiza pruebas de carga, con control desde una
interfaz web. Pensada como alternativa moderna a SIPp.

## ¿Qué hace?

- **Lanza llamadas** (rol UAC) y **recibe llamadas** (rol UAS).
- Tres modos de uso:
  1. **Manual:** una llamada, paso a paso (depuración).
  2. **Escenarios:** flujos definidos en YAML/JSON (estilo SIPp, pero legible).
  3. **Carga:** miles de llamadas a una tasa configurable, con estadísticas en vivo.
- Un único binario, sin dependencias externas, para **Windows y Ubuntu**.

> Estado actual (v0): funciona el monitor de troncales por OPTIONS y la web de
> estado. El motor de llamadas (INVITE) está en desarrollo (Fase 1).

## ¿Para quién es?

Técnicos de VoIP, QA y operadores que necesitan probar centralitas, troncales y
SBCs: validar flujos de llamada, medir comportamiento bajo carga y reproducir
incidencias.

## Cómo arrancarlo

Requiere [Go 1.23+](https://go.dev/dl/).

```bash
# Clonar / situarse en la carpeta del proyecto
go mod download        # descarga dependencias (la primera vez)
go run . --web 127.0.0.1:8080
```

Abre `http://127.0.0.1:8080` en el navegador.

Para usar un fichero de configuración propio:

```bash
go run . --config config.json
```

(Copia `config.example.json` a `config.json` y ajústalo. `config.json` no se sube
al repositorio porque puede contener IPs internas.)

## Documentación

- `FICHA_TECNICA.md` — arquitectura, stack y plan por fases.
- `DESPLIEGUE.md` — cómo compilar y desplegar en Windows y Ubuntu.
- `HANDOFF.md` — estado de desarrollo y próximos pasos.
