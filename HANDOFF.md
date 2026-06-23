# HANDOFF

## Última actualización
Fecha: 2026-06-23 (sesión 4: web de control — Fase 4)

## Estado actual
- Proyecto en Go con base v0 funcional: núcleo SIP que envía OPTIONS (UAC),
  faro de monitorización (una goroutine por troncal) y web local de estado.
- Go 1.26.4 instalado en `C:\Program Files\Go\bin`. El proyecto compila
  (`go build ./...` → OK) y `sipgo v1.4.0` está descargado con sus dependencias.
- Definida la visión: convertir dimitri-5000 en una alternativa profesional a SIPp,
  con tres modos (manual / escenarios / carga) y ambos roles (UAC y UAS).
- API de sipgo verificada para la Fase 1 (ver "Decisiones tomadas").

## Completado en esta sesión
- Verificación del entorno: Go instalado, dependencias descargadas, build OK.
- Lectura y revisión de toda la base v0 (config, sipcore, monitor, webui).
- Inspección de la API real de sipgo (DialogClientCache, DialogServerCache, Server).
- Documentación base: FICHA_TECNICA.md, HANDOFF.md, README.md, DESPLIEGUE.md.
- **Fase 1 COMPLETA (señalización UAC/UAS):**
  - config: nuevo campo `sip_port` (5060 por defecto).
  - sipcore: Core ampliado con dialogClient + contact; `New` ahora recibe
    (bindIP, sipPort, userAgent, log) y fija WithClientPort = sipPort.
  - internal/sipcore/call.go: rol UAC (`Dial`/`DialURI` → `WaitAnswer` → `Ack`
    → `Hangup`) y rol UAS (`Serve` + auto-answer configurable vía `UASPolicy`,
    handlers OnInvite/OnAck/OnBye/OnCancel, enrutado de BYE a ambos roles).
  - Test de loopback (call_test.go): INVITE→180→200→ACK→BYE→200 → PASS.

## Modo manual CLI (añadido en sesión 2)
- `--mode monitor|uas|uac`. Señalización configurable: `--bind-ip`, `--sip-port`
  (5060 por defecto), `--transport udp|tcp` (udp por defecto).
- Si `--bind-ip` va vacío, autodetecta la IP de la tarjeta de red (paquete
  internal/netutil) y lista las IPs disponibles en el log.
- Probado en este PC: UAS en 127.0.0.1:5060 y UAC desde :5062 → llamada
  establecida, sostenida y colgada correctamente. Autodetección OK (Wi-Fi).

### Comandos de prueba
Loopback (dos terminales en el mismo PC, puertos distintos):
```
dimitri-5000 --mode uas --bind-ip 127.0.0.1 --sip-port 5060 --ring-delay 300ms
dimitri-5000 --mode uac --bind-ip 127.0.0.1 --sip-port 5062 --to sip:127.0.0.1:5060 --hold 5s
```
Con SBC en medio (usar IP real de red, no 127.0.0.1):
```
dimitri-5000 --mode uas --sip-port 5060            # detrás del SBC
dimitri-5000 --mode uac --to sip:<IP_DEL_SBC>:5060 # apunta al SBC, que reenvía al UAS
```

## Fase 2 en curso (escenarios YAML)
- Lenguaje definido y documentado en SCENARIO_FORMAT.md. Decisiones: placeholders
  {var}; send estructurado (headers/body) + escape raw; recv EXPLÍCITO (se declara
  cada mensaje; optional para provisionales).
- internal/scenario: tipos (Scenario/Step/Body/Inject/Duration), cargador YAML
  (gopkg.in/yaml.v3, KnownFields=true) y Validate con errores legibles por paso.
- Ejemplos en examples/scenarios/ (uac-basico.yaml, uas-contesta.yaml).
- Tests verdes (parseo válido + 7 casos de validación que deben fallar).
- yaml.v3 pasó a dependencia directa en go.mod.

## Runner de escenarios (añadido)
- sipcore: DialURIWithOptions (cabeceras como map[string]string + body) y
  WaitAnswerObserved (observa cada respuesta vía AnswerOptions.OnResponse).
  Getters LocalIP()/LocalPort().
- internal/runner: ejecuta escenarios UAC dirigidos por los pasos. Sustitución de
  variables {var} (incluye internas remote_host/local_ip...), genera SDP G.711
  básico, añade tag al From, valida los recv de respuesta contra lo recibido.
- CLI: nuevo --mode scenario --file <yaml> [--to <destino>].
- Verificado: test de loopback (runner) + ejecución real por CLI de
  examples/scenarios/uac-basico.yaml contra un UAS. Todos los tests en verde.
- Alcance actual del runner: UAC con flujo estándar de llamada. Pendiente: UAS,
  peticiones arbitrarias, save/match reales e inyección CSV.

## Fase 4 (web de control) — primera iteración
- internal/control: Controller que posee el Core, lanza llamadas UAC en segundo
  plano y mantiene su estado (dialing/ringing/established/ended/failed) con snapshot
  y Hangup. Llamada manual desde la web.
- webui: endpoints /api/calls (GET), /api/call (POST), /api/call/hangup (POST);
  monitor y control opcionales (nil-safe).
- main: nuevo --mode web (arranca Serve + faro + controlador + web).
- UI rediseñada estilo The Designer Republic (negro, tipografía bold, rejilla,
  acento ácido): bloques 01 PLACE CALL, 02 CALLS (en vivo), 03 TRUNKS.
- Verificado de extremo a extremo: lanzar llamada por la web → established → ended.
- Arranque: dimitri-5000 --mode web --bind-ip <ip> --sip-port 5070 --web 127.0.0.1:8080

## Próximos pasos
1. Lanzar ESCENARIOS desde la web (no solo llamadas sueltas).
2. Fase 2: escenarios UAS; save/match ({header:...}/{regex:...}); inyección CSV.
3. Fase 3: carga (cps y llamadas concurrentes) + estadísticas en vivo (SSE/WS).
4. Media RTP (Fase 5): subir audio (MP3→G.711) y oír las llamadas.
5. Revisar el WARN de sipgo al cerrar el socket UDP (cosmético, baja prioridad).

## Decisiones tomadas
- **Conformidad RFC (principio rector):** todo debe cumplir las RFC de SIP (3261 y
  relacionadas) y el comportamiento correcto de UDP/TCP. Apoyarse en sipgo y no
  introducir atajos que violen el protocolo. Validar contra SIPp/centralitas reales.
- **Librería SIP:** sipgo (única en su capa, aislada en internal/sipcore).
- **Escenarios:** formato propio YAML/JSON (no XML de SIPp), por legibilidad.
- **Interfaz:** web como control principal; CLI para arranque.
- **Audio (futuro):** convertir a G.711 al subir el fichero, no por llamada.
- **API sipgo Fase 1 (verificada en módulo cache v1.4.0):**
  - UAC: `NewDialogClientCache(client, contactHDR)` → `Invite(ctx, uri, body)` →
    `WaitAnswer(ctx)` → `Ack(ctx)` → `Bye(ctx)`.
  - UAS: `NewServer(ua)` con `OnInvite/OnAck/OnBye/OnCancel`;
    `NewDialogServerCache(client, contactHDR)` → `ReadInvite` → `DialogServerSession`
    con `Respond(code,...)` / `RespondSDP(...)`; `srv.ListenAndServe(ctx, "udp", addr)`.

## Problemas conocidos
- Aún no hay media RTP (solo señalización planificada para Fase 1).
- `config.go` no valida valores (p. ej. fail_threshold ≤ 0); pendiente de endurecer.
- `Transport` no se normaliza (mayúsc./minúsc.); revisar en Fase 1.

## Archivos modificados
- Nuevos: FICHA_TECNICA.md, HANDOFF.md, README.md, DESPLIEGUE.md.
- (Sin cambios de código todavía en esta sesión.)
