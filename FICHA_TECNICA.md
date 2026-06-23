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

## Modos de operación (visión)

1. **Modo manual (llamada a llamada):** lanzar o recibir una única llamada y seguir su
   flujo paso a paso. Orientado a depuración.
2. **Modo escenarios (estilo SIPp):** flujos definidos en fichero propio (YAML/JSON) con
   una máquina de estados (enviar/esperar/pausar/validar).
3. **Modo carga:** generar tráfico a una tasa configurable (cps) y/o sostener N llamadas
   activas simultáneas, con estadísticas en tiempo real.

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
main.go                 Arranque: lee config, cablea módulos, parada limpia.
internal/
  config/   Configuración (JSON; YAML para escenarios más adelante).
  sipcore/  ÚNICA capa que habla con sipgo: UAC, UAS, transacciones, diálogos.
  scenario/ (futuro) Parser YAML/JSON + máquina de estados de un flujo.
  engine/   (futuro) Generador de carga: control de cps y concurrencia.
  media/    (futuro) RTP + codecs (G.711) + gestión de audio.
  stats/    (futuro) Métricas en tiempo real.
  monitor/  Faro de OPTIONS (v0; pasará a ser un escenario más).
  webui/    Interfaz web + API (estado y, en el futuro, control y stream de stats).
```

## Stack tecnológico

- **Lenguaje:** Go 1.23+ (compila nativo a Windows y Ubuntu sin dependencias externas).
- **Librería SIP:** `github.com/emiago/sipgo` v1.4.0 — gestiona transacciones,
  retransmisiones, diálogos y digest auth de la RFC 3261 (lo más difícil de reimplementar).
- **Escenarios:** `gopkg.in/yaml.v3` (ya disponible vía dependencias de sipgo).
- **Web:** servidor HTTP de la librería estándar + ficheros embebidos (go:embed).

### Decisión de audio (MP3 → RTP)

El RTP de VoIP no transporta MP3, sino audio en bruto codificado (típicamente G.711
µ-law/A-law a 8 kHz mono). Decisión adoptada: **convertir el audio una sola vez al subirlo**
(p. ej. con ffmpeg) a G.711, en lugar de transcodificar en cada llamada. Se concreta en la
Fase 5 (media).

## Estructura de carpetas

Ver el árbol de la sección Arquitectura. Cada paquete bajo `internal/` tiene una única
responsabilidad y comentarios en español explicando el porqué.

## Ejecución local

Requiere Go 1.23+ instalado.

```
go run . --config config.json --web 127.0.0.1:8080
```

(En la v0 actual arranca el faro de OPTIONS y la web de estado.)

## Despliegue

Ver `DESPLIEGUE.md`.

## Estado actual

- **v0 (base):** núcleo SIP que envía OPTIONS (UAC), faro de monitorización con una
  goroutine por troncal, web local que muestra estado por polling. Compila y arranca.
- **En curso:** Fase 1 — núcleo de señalización UAC/UAS para llamadas INVITE completas.

## Plan por fases

1. **Fase 1 — Señalización:** UAC y UAS de INVITE completo (INVITE→180→200→ACK→BYE), sin media.
2. **Fase 2 — Escenarios:** motor de escenarios en YAML/JSON con máquina de estados.
3. **Fase 3 — Carga:** generador de cps, llamadas concurrentes, estadísticas en vivo.
4. **Fase 4 — Web de control:** lanzar/configurar/parar pruebas y métricas en tiempo real.
5. **Fase 5 — Media RTP:** subida y conversión de audio, envío/recepción RTP, DTMF.
6. **Fase 6 — Pulido:** TLS/TCP, REGISTER, export CSV, alto rendimiento.
