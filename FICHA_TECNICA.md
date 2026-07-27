# FICHA TÉCNICA — dimitri-5000

## Información general

- **Nombre:** dimitri-5000
- **Descripción:** Herramienta profesional de pruebas SIP/VoIP (generador y validador de
  tráfico), pensada como alternativa moderna a SIPp.
- **Objetivo:** Lanzar y recibir llamadas SIP de forma controlada, ejecutar escenarios
  de prueba reproducibles y realizar pruebas de carga (miles de llamadas), con control
  y visualización desde una interfaz web.
- **Problema que resuelve:** SIPp es potente pero incómodo (escenarios XML difíciles de
  mantener, sin interfaz visual, despliegue engorroso en Windows). dimitri-5000 busca la
  misma potencia con escenarios legibles (YAML/JSON), un único binario multiplataforma y
  una web de control en tiempo real.

## Roles SIP (terminología estándar)

- **UAC** (User Agent Client): origina la llamada, **envía** el INVITE. Es el rol
  "generador" en pruebas de carga.
- **UAS** (User Agent Server): recibe la llamada, **responde** al INVITE. Simula una
  centralita o destino.

dimitri-5000 implementa **ambos roles**.

## Modos de operación

La interfaz principal es la **web** (`--mode web`): desde ella se crean agentes, se
lanzan y controlan llamadas, se ejecutan escenarios, se hacen pruebas de carga y se ven
las trazas. Los modos de CLI son atajos para automatizar o depurar sin navegador:

1. **Modo manual (llamada a llamada):** lanzar (`--mode uac`) o recibir (`--mode uas`)
   una única llamada y seguir su flujo paso a paso. Orientado a depuración.
2. **Modo escenarios (estilo SIPp):** flujos definidos en fichero propio (YAML) con una
   máquina de estados (enviar/esperar/pausar/validar). Por CLI, `--mode scenario`.
3. **Modo carga:** generar tráfico a una tasa configurable (cps) y/o sostener N llamadas
   activas simultáneas, con estadísticas en tiempo real. Se opera desde la web.
4. **Modo monitor:** faro de OPTIONS que vigila el estado de las troncales
   (`--mode monitor`).

## Arquitectura

- **Frontend:** interfaz web local (HTML/CSS/JS sin framework). Control principal de la
  herramienta y visualización de estado/estadísticas en vivo.
- **Backend / motor:** Go. Toda la lógica SIP, escenarios, carga y media.
- **Base de datos:** ninguna en v1 (estado en memoria). Export a CSV para resultados.
- **APIs externas:** ninguna.
- **Autenticación:** la web sirve solo en localhost (127.0.0.1) por defecto. SIP soporta
  digest auth (vía sipgo) para REGISTER/INVITE.
- **Hosting/despliegue:** binario único autocontenido (la web va embebida con go:embed).
  Multiplataforma: Windows y Ubuntu.

### Separación por capas (principio de diseño)

`sipcore` es la **única** capa que importa `sipgo`. El resto del programa (escenarios,
carga, web) no conoce la librería SIP. Así, un cambio de librería o una ampliación queda
contenido y no se desparrama por el proyecto.

```
main.go                 Arranque: lee config/flags, cablea módulos, parada limpia.
internal/
  config/   Configuración (JSON) y persistencia: ajustes del modo monitor y
            catálogo de destinos remotos del modo web.
  sipcore/  ÚNICA capa que habla con sipgo: UAC, UAS, transacciones, diálogos.
  agent/    Gestor de agentes: instancias SIP independientes (IP, puerto, conducta).
  control/  Orquestación de una llamada web (lanzar, colgar, HOLD/RESUME, REFER).
  scenario/ Parser YAML + validación de un flujo (máquina de estados).
  runner/   Ejecuta un escenario contra el motor SIP (lados UAC y UAS).
  load/     Generador de carga: token-bucket de cps, concurrencia y estadísticas.
  media/    RTP + codec G.711 + audio (tono o WAV propio) + SDP.
  trace/    Registro de trazas SIP por llamada (para el visor tipo SBC).
  monitor/  Faro de OPTIONS que vigila troncales.
  netutil/  Utilidades de red (autodetección de IP, etc.).
  webui/    Interfaz web + API (estado, control y stream de estadísticas).
```

## Stack tecnológico

- **Lenguaje:** Go 1.23+ (compila nativo a Windows y Ubuntu sin dependencias externas).
- **Librería SIP:** `github.com/emiago/sipgo` v1.4.0 — gestiona transacciones,
  retransmisiones, diálogos y digest auth de la RFC 3261 (lo más difícil de reimplementar).
- **Escenarios:** `gopkg.in/yaml.v3` (ya disponible vía dependencias de sipgo).
- **Web:** servidor HTTP de la librería estándar + ficheros embebidos (go:embed).

### Decisión de audio (WAV → G.711 → RTP)

El RTP de VoIP no transporta ficheros comprimidos, sino audio en bruto codificado
(típicamente G.711 µ-law/A-law a 8 kHz mono). Decisión adoptada: partir de **WAV (PCM)**
—lo que produce sin esfuerzo Audacity o ffmpeg— y **convertirlo una sola vez al cargarlo**
a G.711, en lugar de transcodificar en cada llamada. La decodificación de WAV (mezcla a
mono y resampleo a 8 kHz) es propia, sin dependencias. Se puede enviar ese WAV o un tono
generado. El soporte de MP3 quedaría para más adelante (con una librería en Go puro).

## Estructura de carpetas

Ver el árbol de la sección Arquitectura. Cada paquete bajo `internal/` tiene una única
responsabilidad y comentarios en español explicando el porqué.

## Ejecución local

Requiere Go 1.23+ instalado. La vía habitual es el script `run-web.sh` (Linux/macOS) o
`run-web.ps1` (Windows), que arranca el modo web. Con `--mode` a mano:

```
# Web (interfaz principal: agentes, llamadas, escenarios, carga y trazas)
go run . --mode web --bind-ip "" --sip-port 5070 --web 127.0.0.1:8080

# Monitor (faro de OPTIONS + web de estado)
go run . --mode monitor --web 127.0.0.1:8080

# Recibir llamadas (UAS)
go run . --mode uas --bind-ip 127.0.0.1 --sip-port 5060

# Lanzar una llamada (UAC)
go run . --mode uac --bind-ip 127.0.0.1 --sip-port 5062 --to sip:127.0.0.1:5060 --hold 5s

# Ejecutar un escenario YAML
go run . --mode scenario --file examples/scenarios/uac-basico.yaml --to sip:127.0.0.1:5060
```

La IP de señalización (`--bind-ip`) se autodetecta de la tarjeta de red si se deja
vacía; el puerto por defecto es 5060 y el transporte UDP (`--transport tcp` para TCP).

## Despliegue

Ver `DESPLIEGUE.md`.

## Estado actual

Todas las fases del plan están implementadas y en uso. La herramienta funciona de
extremo a extremo desde la web:

- **Señalización UAC/UAS** de INVITE completo (INVITE→180→200→ACK→BYE), transporte
  UDP/TCP y autodetección de la IP de la tarjeta de red.
- **Media RTP con G.711** (µ-law/A-law), enviando un tono o un WAV propio, con métricas
  en vivo (paquetes, jitter, pérdida).
- **Control de la llamada en curso:** colgar, HOLD/RESUME (re-INVITE real) y REFER.
- **Escenarios en YAML** de ambos lados (UAC y UAS), ejecutables desde la web, por CLI o
  como plantilla de cada llamada en una prueba de carga.
- **Pruebas de carga:** token-bucket de cps sin techo, N llamadas concurrentes, números
  A/B fijos, duración de llamada opcional y desglose de fallos por causa (PDD, canceladas).
- **Catálogo de destinos:** los elementos remotos (SBC, centralitas, operadores) se dan
  de alta una vez y se referencian por id desde llamadas, escenarios, carga y monitor.
- **Monitor de troncales** por OPTIONS y **visor de trazas tipo SBC** con diagrama de
  escalera por llamada.
- **Multi-agente:** varias instancias SIP independientes en la misma máquina.

Nota: del modo web solo persiste a disco el **catálogo de destinos** (en el `config.json`,
vía `config.Store`). Los agentes y la asignación de qué agente monitoriza qué destino
viven en memoria mientras la aplicación corre.

## Plan por fases (todas completadas)

1. **Fase 1 — Señalización:** UAC y UAS de INVITE completo (INVITE→180→200→ACK→BYE), sin media. ✔
2. **Fase 2 — Escenarios:** motor de escenarios en YAML con máquina de estados. ✔
3. **Fase 3 — Carga:** generador de cps, llamadas concurrentes, estadísticas en vivo. ✔
4. **Fase 4 — Web de control:** lanzar/configurar/parar pruebas y métricas en tiempo real. ✔
5. **Fase 5 — Media RTP:** subida y conversión de audio (WAV→G.711), envío/recepción RTP. ✔
6. **Fase 6 — Pulido:** TCP, export CSV, alto rendimiento (2000 cps reales verificados). ✔
