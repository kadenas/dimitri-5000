# HANDOFF

## Última actualización
Fecha: 2026-06-24 (sesión 7: + señalización simétrica + trunks por agente v0.10)

## Sesión 7 (cont.) — fix puertos simétricos + trunks por agente (v0.10)
- FIX RAÍZ DE PUERTOS: usábamos sipgo WithClientPort, que solo fija el puerto del
  Via y DESACTIVA la reutilización de conexión → origen efímero. Cambiado a
  WithClientConnectionAddr(bindIP:sipPort): sipgo reutiliza el socket de escucha
  (req.Laddr -> GetConnection(laddr)). Verificado: OPTIONS e INVITE salen del
  puerto del agente (5070), no random. Señalización SIMÉTRICA (clave para SBC).
- MODELO local/remoto + trunks POR AGENTE:
  - Cada Agent tiene su lista de trunks (config.Target) y su propio monitor.Monitor
    sobre su core: AddTrunk/RemoveTrunk/TrunksSnapshot. Faro global eliminado en
    modo web (se pasa nil a webui); el trunk loopback de ejemplo ya no se usa.
  - Manager guarda MonitorConfig y lo pasa a los agentes (NewManager(monCfg, log)).
  - webui: GET/POST /api/trunks (lista global agregada + alta) y /api/trunks/remove.
  - UI v0.10: panel 04 TRUNKS con columna AGENT + REMOVE + alta (FROM AGENT, id,
    name, host remoto, port, transport). La web lee /api/trunks (antes /api/status).
- Verificado por API: trunk default->uac-2 UP; baja/alta OK.

## Próximo: RTP/media (Fase 5) o control de llamada (HOLD/REFER). Pendiente decidir.

## Sesión 7 (cont.) — ladder SIP (v0.9) y validación de bind
- TRAZA SIP / ladder: sipcore.EnableTracing(fn) activa sip.SIPDebugTracer (hook
  global) y reenvía cada mensaje (dir/transport/laddr/raddr/bytes) a un callback
  neutro. internal/trace.Store parsea (primera línea, Call-ID, CSeq), buffer
  acotado (2000), agrupa por Call-ID. webui: /api/trace, /api/trace/calls,
  /api/trace/clear. UI bloque 06 LADDER (selector de llamada, filtro OPTIONS,
  CLEAR; diagrama de 2 carriles con flechas, dedup del mismo msg visto en ambos
  extremos cuando los dos agentes son locales). Tests del parseo en verde.
- VALIDACIÓN DE BIND (agent.Start): checkBindAvailable abre el socket un instante
  antes de Serve → detecta "puerto en uso" e "IP no local" y devuelve error claro
  (la web lo muestra; el agente ya NO queda 'running' en falso). Verificado.
- Diagnóstico puertos: el OPTIONS desde 0.0.0.0:efímero pasa SOLO con destino
  loopback (trunk 127.0.0.1) bindeando IP de LAN (interfaces distintas). Contra un
  remoto real alcanzable, sale desde el puerto configurado. Pendiente: modelo
  local/remoto (trunks por agente) para originar OPTIONS desde el socket del agente.

## Decidido — PRÓXIMA feature: agente local + trunks remotos
- Separar "agente local" (escucha local, IP local validada + puerto libre) de
  "trunk remoto" (endpoint que NO escuchamos; OPTIONS/llamadas originados desde un
  agente local). Trunks POR agente, OPTIONS desde el core del agente (sin puerto
  random). Reemplaza el faro global (que usa un único core y trae trunk loopback).
- Después: RTP/media (Fase 5, G.711 nativo; G.729 patentes ~expiradas 2017, decode
  necesitaría bcg729/cgo GPL, opcional; para test basta medir el stream),
  control de llamada (HOLD re-INVITE / REFER vía DialogClientSession.Do).

## Sesión 7 (cont.) — selector TO AGENT (v0.7) y mensajería MESSAGE (v0.8)
- PLACE CALL: selector FROM AGENT (origen) + TO AGENT (destino): al elegir un
  agente destino, su IP:puerto rellena dest_host/dest_port (sigue el modo manual/
  SBC con DEST HOST o TARGET URI). Resolución client-side (agentsCache).
- Mensajería SIP (RFC 3428):
  - sipcore/message.go: MessageSpec + Core.SendMessage (MESSAGE vía client.Do) y
    onMessage (responde 200 + callback). Core.SetMessageHandler + campo msgHandler.
    Serve registra srv.OnMessage.
  - control: MessageRec + SendMessage (async, registra code/reason) +
    RecordIncomingMessage + MessagesSnapshot. agent.Start cablea el handler.
  - webui: GET /api/messages (agrega por agente) + POST /api/message (simple o
    enriquecido). UI bloque 05 MESSAGES (compose + historial IN/OUT).
- Verificado por API: MESSAGE default->uac-2: out 200 OK + in registrado en uac-2.

## Próxima feature elegida: TRAZA DE ESCALERA (ladder)
- sipgo expone sip.SIPDebugTracer(SIPTracer) — hook GLOBAL que entrega cada
  mensaje (read/write, transport, laddr, raddr, bytes). Plan: tracer propio que
  parsea primera línea + Call-ID + CSeq, guarda eventos {time,dir,laddr,raddr,...}
  agrupados por Call-ID, API /api/trace y diagrama de escalera en la web.
- Control de llamada (hold/resume/desvío) FACTIBLE vía DialogClientSession.Do
  (re-INVITE, REFER) — siguiente tras el ladder.

## Sesión 7 (cont.) — Paso A: llamada "humana" con valores SIP (travesía SBC)
- sipcore.RichInvite + Core.DialInvite: construye el INVITE con valores concretos.
  - Request-URI se envía a DestHost:DestPort (el SBC o el peer); el To puede llevar
    OTRO dominio → patrón de travesía por SBC (el SBC enruta por número).
  - From tipado con su tag (sipgo lo respeta y no lo regenera). PAI y cabeceras
    arbitrarias. SplitURI parsea el modo simple "sip:host:port".
- control.PlaceCall ahora recibe CallSpec (RichInvite + hold + display).
- webui /api/call admite modo simple ('to') o enriquecido (dest_host, from_user/
  domain/display, to_user/domain, pai_user, headers{}). buildCallSpec une ambos.
- onInvite loguea from/to/r-uri/pai (verificable que las identidades llegan).
- UI (v0.6): PLACE CALL con bloque plegable "VALORES SIP / SBC" (DEST HOST/PORT,
  FROM user/domain/display, TO user/domain, PAI, HEADERS textarea Nombre: Valor).
- Verificado por API: llamada default->:5072 con from=1000@pbx.local,
  to=2000@destino.local, r-uri=2000@127.0.0.1:5072, pai=<sip:1000@pbx.local>.
  Falta REPASO VISUAL en navegador del bloque avanzado antes de commit.
- Pendiente Paso B: escenarios SIPp en la web (motor runner ya existe).

## Sesión 7 (cont.) — G2: API y panel de AGENTES en la web
- webui ahora habla con el Manager (no con un control único):
  - GET/POST /api/agents (lista | alta+arranque), POST /api/agents/start|stop|remove.
  - /api/call lleva agent_id (qué agente origina); /api/calls AGREGA las llamadas
    de todos los agentes etiquetadas con agent_id; hangup busca en todos.
  - nil-safe: en modo monitor (manager=nil) /api/agents devuelve [].
- UI rediseñada: bloque 01 AGENTS (tabla + alta con id/name/ip/puerto/transporte/
  answer_code + acciones START/STOP/REMOVE), 02 PLACE CALL con selector de AGENT,
  03 CALLS con columna AGENT, 04 TRUNKS. CSS para estados running/stopped y botones
  mini. Versión visible v0.5.
- Verificado por API en un SOLO proceso: alta de 'uac-2' en :5072 junto al 'default'
  en :5070, llamada default->uac-2 (established->ended), stop/remove y rechazo de id
  duplicado (400). Falta repaso VISUAL en navegador (la API está sólida).
- Conocido: si el puerto de un agente nuevo está ocupado, Serve falla en su goroutine
  pero el agente queda 'running' (el bind es asíncrono). Mejorable en G2.1.

## Sesión 7 — fix From (RFC) y modelo "un proceso, varios agentes" (G1)
- **#0 Fix From (commit e706102):** sipgo ponía el From con hostname "localhost"
  por defecto (rechazable por PBX/SBC). sipcore.New recibe ahora `fromDomain`
  (vacío = IP de bind) y lo fija con WithUserAgentHostname. Nuevo campo config
  `from_domain` y flag `--from-domain` (flag > config > IP). Verificado: el From
  sale `sip:dimitri-5000@<IP_real>`. El Contact ya estaba bien.
- **Decisión de producto (memoria actualizada):** la herramienta evoluciona a
  "un proceso, varios agentes" (una web gestiona varios endpoints SIP UAC/UAS) y
  empaquetado final con **Wails** (app de escritorio reutilizando la misma web).
- **G1 (backend multi-agente) COMPLETO:**
  - internal/agent: `Agent` (Core + política UAS + control, con Start/Stop; puede
    CREAR su Core o ADOPTAR uno existente) y `Manager` (CRUD + Start/Stop/Remove +
    Snapshot, orden estable, validación de Spec). Tests verdes (manager_test.go).
  - main runWeb refactorizado: crea un Manager con un agente "default" que ADOPTA
    el Core actual → comportamiento idéntico (verificado e2e: llamada OK). El faro
    comparte el Core; la web usa el control del agente por defecto.
- Pendiente G2: API /api/agents + panel AGENTS en la web (alta/activar/parar
  agentes), y reencajar SETTINGS/TRUNKS/escenarios en el modelo por agente.
  Luego G3: empaquetado Wails. Ver [[vision-sipp-replacement]].

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

## Sesión 5 — trunk OPTIONS, multi-instancia y config persistente
- Trunk real: Serve responde OPTIONS 200 (Allow/Accept/Contact). Fix: el modo
  monitor ahora hace Serve (si no, se perdían las respuestas a los OPTIONS).
  Verificado con dos instancias en 127.0.0.1 y test TestOptionsTrunk.
- Multi-instancia: ejecutar el binario 2 veces con --sip-port y --web distintos
  (p. ej. :5070/:8080 y :5072/:8081). Cada una es independiente.
- IP de red: el SIP ya usa la IP real autodetectada. La web ahora loguea la URL
  con la IP de red y sugiere --web 0.0.0.0:PUERTO para acceso desde otros equipos.
- Configuración PERSISTENTE (base de "app configurable"):
  - config.Save atómico (temporal + rename, no corrompe config.json).
  - config.Store: fuente de verdad concurrente con AddTarget/RemoveTarget/
    SetSignaling + validación (id único, transporte normalizado) y persistencia.
  - Decidido: cambios de nuestro puerto/IP se aplican AL REINICIAR.
  - Test store_test.go en verde.

## Sesión 6 — faro dinámico (paso 1 del bloque "app configurable") COMPLETO
- internal/monitor refactorizado: cada troncal se vigila en su PROPIA goroutine
  con su context.CancelFunc, todas colgando de un contexto raíz fijado en Start.
- Métodos nuevos en caliente:
  - AddTarget(t): valida, rechaza id duplicado/inválido y arranca su goroutine.
  - RemoveTarget(id): cancela su goroutine y olvida su estado (false si no existe).
  - Sync(targets): reconcilia la lista viva con una deseada (alta/baja/reinicio si
    cambió host/puerto/transporte). Será la vía que use la API web / config.Store.
- Estado interno pasa a mapas (states/cancels/targets) + slice `order` para que
  Snapshot mantenga orden estable. Guarda en probe: si la troncal se retiró con un
  sondeo en vuelo, sale sin tocar nada (evita panic).
- Firma de New intacta → main.go sin cambios; comportamiento de arranque conservado.
- monitor_test.go nuevo (TestAddRemoveTarget, TestSyncReconcilia). Toda la batería
  en verde; go vet limpio. (-race no disponible: requiere cgo/compilador C.)

## Próximos pasos (orden actual tras G1/G2/Paso A)
1. **Paso B — Escenarios SIPp en la web:** panel para listar y lanzar los YAML de
   examples/scenarios/ desde un agente (motor runner ya existe). Luego ampliar el
   runner: escenarios UAS, save/match, inyección CSV.
2. **Enrutado por número (idea del usuario, para MÁS ADELANTE):** cómo enrutar una
   llamada entrante al agente destino por el número (To/Request-URI), o enrutado en
   general entre agentes del mismo proceso. Pensar si lo hace un "router" interno o
   se delega siempre en el SBC/PBX. Decisión pendiente.
3. **SETTINGS por agente:** editar bind_ip/sip_port/answer_mode (parar+recrear el
   agente lo aplica en caliente, ya que el agente puede pararse/arrancarse).
4. **TRUNKS desde la web:** API GET/POST/DELETE /api/trunks + panel; conectar
   config.Store ↔ faro dinámico (el motor ya está listo).
5. **G2.1 (pulido):** si el puerto de un agente nuevo está ocupado, hoy queda
   'running' aunque Serve falle (bind asíncrono); detectar y reflejar el error.
6. Fase 3: carga (cps + concurrencia, patrón del Dimitri-4000) + stats en vivo.
7. Media RTP (Fase 5): subir audio (MP3→G.711) y oír las llamadas.
8. G3: empaquetado Wails (app de escritorio reutilizando la web).
9. Revisar el WARN de sipgo al cerrar el socket UDP (cosmético).

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
