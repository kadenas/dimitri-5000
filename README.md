# dimitri-5000

Herramienta de pruebas SIP/VoIP con interfaz web. Lanza y recibe llamadas,
ejecuta escenarios reproducibles, genera carga y te enseña las trazas SIP en un
diagrama de escalera — todo desde el navegador, con un único binario.

Nace como alternativa moderna a SIPp: la misma idea (probar centralitas,
troncales y SBCs con tráfico controlado), pero con escenarios en YAML legible
en lugar de XML, y una web de control en tiempo real en lugar de una pantalla
de ncurses.

## ¿Qué puedes hacer con ella?

- **Lanzar llamadas** (UAC) y **recibirlas** (UAS), con identidades SIP
  realistas: From/To con número y dominio, P-Asserted-Identity, cabeceras
  arbitrarias… lo necesario para atravesar un SBC como lo haría una llamada real.
- **Audio de verdad**: media RTP con G.711, con métricas en vivo (paquetes,
  jitter, pérdida). Puedes enviar un tono o tu propio fichero WAV.
- **Controlar la llamada en curso**: colgar, poner en espera y reanudar
  (HOLD/RESUME con re-INVITE real) o desviarla (REFER).
- **Escenarios reproducibles** en YAML, estilo SIPp pero legibles: tanto del
  lado que llama (UAC) como del que contesta (UAS), con temporizaciones,
  respuestas opcionales y variables. Hay ejemplos en `examples/scenarios/`.
- **Pruebas de carga**: N llamadas a una tasa configurable (cps), cada una
  ejecutando un escenario completo, con estadísticas en vivo. Puedes fijar el
  número A (llamante) y el número B (llamado) desde el panel: todas las
  llamadas de la prueba salen con esa numeración (en el From y en el
  To/Request-URI), lista para enrutarla por número en tu SBC o PBX; si la
  carga usa un escenario, esos números pisan sus variables `{caller}` y
  `{callee}`.
- **Monitorizar troncales** con OPTIONS: estado, código de respuesta y RTT de
  cada trunk, con umbral de fallos configurable.
- **Ver qué pasa por el cable**: visor de trazas tipo SBC con todas las
  llamadas (estado, duración, origen/destino) y, al pulsar una, su diagrama de
  escalera mensaje a mensaje; cada mensaje se puede abrir en crudo.
- **Varios agentes a la vez**: cada agente es una instancia SIP independiente
  (su IP, su puerto, su comportamiento al contestar), así puedes simular los
  dos extremos de una llamada en la misma máquina.

Todo esto vive en la interfaz web, organizada en 7 paneles: AGENTS, PLACE
CALL, CALLS, TRUNKS/OPTIONS, SIP TRACE, SCENARIOS y LOAD TEST.

## Arranque rápido

Necesitas [Go 1.23+](https://go.dev/dl/). Desde la carpeta del proyecto:

```bash
# Linux / macOS
./run-web.sh
```

```powershell
# Windows
.\run-web.ps1
```

Abre `http://127.0.0.1:8080` y ya estás dentro. El script arranca en loopback
(SIP en 127.0.0.1:5070), que es perfecto para la primera toma de contacto:
crea un segundo agente en el panel AGENTS (por ejemplo en el puerto 5071),
lanza una llamada entre los dos desde PLACE CALL y mira la traza en SIP TRACE.

Para hablar con equipos reales (un Asterisk, un SBC…), edita las variables al
principio de `run-web.sh` / `run-web.ps1`: deja `BIND_IP` vacío para que
autodetecte la IP de tu tarjeta de red, y pon `WEB_ADDR` en `0.0.0.0:8080` si
quieres abrir la web desde otro equipo de la LAN.

Si prefieres el comando directo:

```bash
go run . --mode web --bind-ip "" --sip-port 5070 --web 127.0.0.1:8080
```

## Modos de ejecución

El modo `web` es el principal y el que querrás casi siempre. Los demás son
útiles para automatizar o para depurar sin navegador:

| Modo | Qué hace | Ejemplo |
|---|---|---|
| `web` | Estación de trabajo completa: agentes, llamadas, escenarios, carga y trazas desde el navegador | `go run . --mode web` |
| `uac` | Lanza UNA llamada, la mantiene un tiempo y cuelga | `go run . --mode uac --to sip:192.168.1.10:5060 --hold 10s` |
| `uas` | Se queda escuchando y contesta las llamadas entrantes | `go run . --mode uas --sip-port 5060 --answer-code 200` |
| `scenario` | Ejecuta un escenario YAML por CLI | `go run . --mode scenario --file examples/scenarios/uac-basico.yaml --to sip:10.0.0.5:5060` |
| `monitor` | Solo el faro de troncales (OPTIONS) + web de estado | `go run . --mode monitor --config config.json` |

`go run . --help` lista todos los flags (transporte UDP/TCP, dominio del From,
código de respuesta del UAS, tiempo de ringing…).

## Configuración

Para el modo monitor (o para fijar IP/puerto sin flags) puedes usar un JSON:

```bash
cp config.example.json config.json   # edítalo con tus IPs y troncales
go run . --config config.json
```

`config.json` está fuera del repositorio a propósito: suele contener IPs
internas. En modo `web`, agentes y trunks se crean desde la propia interfaz
(el estado vive en memoria mientras la aplicación corre).

## Escenarios

Un escenario describe un flujo SIP como una secuencia de pasos. Este, por
ejemplo, es una llamada básica del lado que llama:

```yaml
name: uac-llamada-basica
role: uac

steps:
  - send: INVITE
  - recv: "100"
    optional: true
  - recv: "180"
    optional: true
  - recv: "200"
  - send: ACK
  - pause: 3s
  - send: BYE
  - recv: "200"
```

También puedes escribir el guion del lado que contesta (`role: uas`): cuánto
tarda en dar el 180, con qué código responde, cuándo cuelga. La referencia
completa del formato está en `SCENARIO_FORMAT.md`, y en `examples/scenarios/`
hay escenarios de ambos lados listos para usar. Los escenarios se ejecutan
desde la web (panel SCENARIOS), por CLI (`--mode scenario`) o como plantilla
de cada llamada en una prueba de carga.

## Compilar un binario

```bash
go build -ldflags "-s -w" -o dist/dimitri-5000 .
```

Sale un único ejecutable autocontenido (la web va embebida): se copia a la
máquina de destino y listo, sin instalar nada más. En `DESPLIEGUE.md` está el
detalle, incluida la compilación cruzada Windows ⇄ Linux.

## ¿Para quién es?

Técnicos de VoIP, QA y operadores que necesitan probar centralitas, troncales
y SBCs: validar flujos de llamada, medir comportamiento bajo carga y
reproducir incidencias sin pelearse con XML.

## Documentación

- `FICHA_TECNICA.md` — arquitectura, stack y plan por fases.
- `SCENARIO_FORMAT.md` — referencia del lenguaje de escenarios.
- `DESPLIEGUE.md` — cómo compilar y desplegar en Windows y Ubuntu.
- `HANDOFF.md` — diario de desarrollo: qué se ha hecho y qué queda.
