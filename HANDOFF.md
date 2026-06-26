# HANDOFF

## Última actualización
Fecha: 2026-06-26 (sesión 12: escenarios UAS — el lado que CONTESTA, scriptado)

## Sesión 12 — Escenarios UAS (runner del lado servidor, con temporización)
- OBJETIVO (pedido del usuario): que el lado que CONTESTA (B) pueda seguir un guion
  con su temporización: "cuando llega la llamada, esperar 500 ms y responder 180,
  sonar 2 s y luego 200". Antes solo había escenarios UAC; el UAS contestaba con una
  política FIJA (RingDelay+AnswerCode). Ahora el UAS es scriptable como en SIPp.
- DISEÑO (respeta capas: sipcore NO importa runner/scenario):
  - sipcore: UASPolicy gana `Script []UASStep` (tipo NEUTRO: UASPause/UASSendProvisional/
    UASSendFinal/UASWaitBye/UASSendBye). Si hay Script, onInvite delega en runUASScript
    (pausas + provisionales + final con media; el 2xx bloquea hasta el ACK). recv BYE
    => UASWaitBye, coordinado con onBye vía byeWaiters[callID] (onBye cierra el canal).
    SetUASPolicy ahora es SEGURO EN CALIENTE (uasMu RWMutex; onInvite toma copia).
    serveCtx guardado en Serve para cancelar pausas al parar el agente. reasonFor
    ampliado (100/180/183/4xx/5xx/6xx).
  - runner: `BuildUASPolicy(sc) (sipcore.UASPolicy, error)` traduce un escenario role
    uas al guion (recv INVITE=disparador; pause; send 1xx/2xx-6xx; recv ACK=implícito;
    recv BYE=WaitBye; send BYE=SendBye). Valida que reciba INVITE y envíe una final.
  - agent: SetUASScenario(name, pol)/ClearUASScenario()/UASScenario(); persiste entre
    stop/start y se aplica en caliente. Info.uas_scenario expuesto a la web.
  - webui: POST /api/agents/uas-scenario {agent_id, file} (file vacío = quitar). Carga
    el YAML (anti path-traversal), exige role uas y traduce con runner.BuildUASPolicy.
    UI: columna "UAS SCENARIO" en el panel 01 AGENTS con selector por agente
    (— auto-answer — + escenarios uas); loadScenarios llena uasScenariosCache.
- EJEMPLO: examples/scenarios/uas-lento.yaml (recv INVITE -> pause 500ms -> 180 ->
  pause 2s -> 200 -> recv ACK -> recv BYE -> 200).
- TESTS: internal/runner/uas_test.go TestUASScriptTiming (loopback: valida ORDEN y
  TEMPORIZACIÓN de 180/200 y que recv BYE desbloquea). go test ./... en verde; el
  test del runner usa puertos propios (35180/35181) y deja los servidores vivos para
  no disparar el race conocido de sipgo al cerrar el listener (lo tiene también el
  TestLlamadaLoopback de sipcore: es de la librería, no del proyecto).
- VERIFICADO e2e (binario): web :8099 + agente B :5072 con uas-lento.yaml asignado;
  A->B: en la TRAZA, 180 a +0.501s y 200 a +2.502s (clavado al guion); llamada
  established con media; quitar el escenario vuelve al auto-answer; asignar un uac
  se rechaza. Commit pendiente.
- PRÓXIMO posible: reason custom por paso; save/match reales (capturas y validaciones
  sobre el mensaje); CSV inject; o seguir con Fase 5.3/5.4/Wails.


## Sesión 11 — Carga que ejecuta un ESCENARIO por llamada (estilo SIPp)
- OBJETIVO (era el "PRÓXIMO" de la sesión 10): que el motor de carga establezca cada
  llamada ejecutando un ESCENARIO UAC (señalización dirigida por el YAML: From/To,
  cabeceras, recv esperados) en vez del INVITE básico, SOSTENIÉNDOLA hasta el STOP.
- RUNNER (internal/runner/runner.go) refactor + API nueva, sin tocar el modo CLI:
  - Extraído el paso INVITE a `doInvite(...)` (cabeceras + tag From + body, observa
    respuestas) y el consumo de recv a `consumeInviteResponses(...)`. runUAC los reusa.
  - NUEVO `Establish(ctx, sc, headerOverride, bodyOverride) (*UACCall, error)`: ejecuta
    INVITE -> respuestas -> ACK y DEVUELVE la llamada VIVA, sin ejecutar pausas ni BYE
    (la duración la manda la carga). headerOverride/bodyOverride imponen el SDP de
    oferta con el puerto RTP real. Solo role uac (error si no).
- LOAD (internal/load/load.go): Spec.Scenario *scenario.Scenario (nil = INVITE básico).
  El worker, si hay escenario, usa runner.Establish (ya deja la llamada establecida)
  en vez de DialInvite+WaitAnswer+Ack; el resto (media, sostener hasta STOP/BYE, colgar)
  igual. Helper scenarioTarget(inv) = sip:DestHost:DestPort del destino real (SBC/peer);
  el escenario aporta las identidades. Stats.Scenario expone el nombre del escenario.
- WEBUI: loadReq.Scenario (nombre de fichero); handleLoadStart lo carga de disco
  (filepath.Base anti path-traversal) y exige role uac (400 si no). UI: selector
  "SCENARIO (uac)" en el bloque 08 LOAD TEST (loadScenarios rellena solo los uac
  válidos + opción "(INVITE básico)"); chip SCENARIO en el panel de métricas.
- TESTS: internal/load TestCargaConEscenario (sostiene N ejecutando un escenario con
  pause 1h + BYE que DEBEN ignorarse; valida Stats.Scenario y el drenaje). go test ./...
  y go test -race ./internal/load en verde.
- VERIFICADO e2e (binario): web :8099 + agente UAS uac-2 :5072; carga default->:5072
  con scenario=uac-basico.yaml, N=10@cps20 media=true -> 10 sostenidas, scenario=
  "uac-llamada-basica", RTP 1360/1360 0 pérdida, 0 failed/ended; STOP drena a 0;
  escenario UAS rechazado con 400. Las pausas/BYE del YAML se ignoran (lo cuelga la carga).
- PRÓXIMO: validar a 1000+ contra un destino REAL (no loopback, que duplica carga).
  Pendientes mayores que quedan: Fase 5.3 (grabar RTP entrante a WAV + oírlo con <audio>),
  Fase 5.4 (HOLD real re-INVITE), G3 (empaquetado Wails).

## Sesión 10 — UX, comportamiento de llamada y Fase 3 (motor de carga)
- UX (rellenar a golpe de ratón):
  - NUEVO endpoint GET /api/netinfo -> {local_ip, ips:[{ip,label}]} usando el
    netutil ya existente (webui/server.go).
  - index.html + app.js: <datalist id="local-ips"> compartido; BIND IP (alta de
    agente) se pre-rellena con la IP principal y sugiere las detectadas; DEST HOST
    (SBC) sugiere lo mismo; al elegir TO AGENT en PLACE CALL se vuelca su
    sip:ip:puerto al TARGET URI (visible y editable).
- COMPORTAMIENTO DE LLAMADA: HOLD por defecto pasa de 10 -> 0 (∞). Una llamada
  lanzada se SOSTIENE con RTP hasta que se cuelga con HANGUP (BYE) o un extremo la
  termina; ya no se cae sola. El lado UAS ya esperaba el BYE (HoldTime 0). Campo
  HOLD conservado por si se quiere duración fija. Verificado e2e.
- FASE 3 — MOTOR DE CARGA (paquete SEPARADO internal/load; NO importa sipgo):
  - Modelo elegido: "objetivo de N concurrentes". Sube a N llamadas establecidas
    al ritmo CPS, las SOSTIENE con RTP (repone las que caen) y STOP las cuelga
    todas con BYE. Spec{Invite, Concurrent, CPS, MaxCalls, Audio, WithMedia}.
  - Generator con un objeto `run` POR EJECUCIÓN (contadores atómicos aislados):
    así un STOP seguido de START no mezcla contadores mientras la anterior drena.
    launchLoop (ticker a 1/cps, lanza si active+pending < N), worker por llamada
    (DialInvite -> WaitAnswer -> Ack -> media -> espera STOP(ctx) o BYE remoto
    (UACCall.Done())). Stop no bloquea: drena en segundo plano (Stopping=true).
  - sipcore: NUEVO UACCall.Done() = session.Context().Done() (sipgo cancela el ctx
    del diálogo al pasar a Ended -> detecta el BYE remoto para reponer).
  - control: StartLoad/StopLoad/LoadStats (reutiliza el WAV del agente si WithMedia
    y no se pasa audio). Una ejecución de carga por agente (loadGen en el Controller).
  - webui: POST /api/load/start, POST /api/load/stop, GET /api/load (stats por
    agente). UI: bloque 08 LOAD TEST (FROM/TO AGENT, TARGET URI, CONCURRENT N, CPS,
    MAX 0=∞, checkbox MEDIA, START/STOP) + panel de chips con métricas en vivo
    (TARGET/ACTIVE/PENDING/LAUNCHED/ESTABLISHED/FAILED/ENDED + RTP ↑↓/LOST). CSS
    .load-stats/.load-chip.
  - TESTS: internal/load/load_test.go (sostiene N y drena al parar) + go test -race
    LIMPIO (el detector SÍ está disponible en este PC). Toda la batería en verde.
  - VERIFICADO e2e (binario): N=20@cps30 sube en ~1s y se sostiene con RTP, 0
    pérdida; STOP drena a 0. N=300@cps200 con media (600 diálogos + 600 sockets
    RTP en un proceso loopback): 300 sostenidas, 0 fallidas, 0 pérdida, sin
    errores/panics. ulimit -n = 1M (FDs no son límite).
- PRÓXIMO (pedido por el usuario, "escenarios estilo SIPp"): que la carga pueda
  ejecutar un ESCENARIO por llamada en vez del INVITE básico. El runner ya existe
  pero hace su propio Dial+hangup; hay que adaptarlo para "sostener hasta STOP" y
  exponer su ciclo de vida, y añadir un campo Scenario a load.Spec (el worker
  ejecutaría el escenario). Validación a 1000+ conviene hacerla contra un destino
  REAL (no loopback, que duplica la carga al tener UAC+UAS en el mismo proceso).

## Sesión 9 — Fase 5.1 (media RTP base) COMPLETA y verificada e2e
- NUEVO PAQUETE internal/media (sin dependencias externas; NO importa sipgo):
  - rtp.go: cabecera RTP de 12 bytes (RFC 3550). Marshal simple (CC=0); ParsePacket
    tolera CSRC y cabecera de extensión de otros emisores.
  - g711.go: G.711 µ-law/A-law a mano, port fiel de la referencia ITU/Sun (sin CGO).
  - sdp.go: BuildOffer/BuildAnswer (audio PCMU/PCMA), Parse (c=/m=audio/ptime) y
    ChooseCodec (prefiere PCMU). Tolera c= de sesión y de media.
  - session.go: socket UDP RTP; envía un tono 440 Hz codificado en G.711 cada 20 ms,
    recibe y MIDE tx/rx (paquetes y bytes), pérdida (secuencia, RFC 3550 A.1) y
    jitter de interarribo (§6.4.1). Open (puerto efímero) -> LocalPort -> Start.
  - 13 tests en verde (silencio/monotonía G.711, round-trip RTP+CSRC, SDP, loopback).
- CABLEADO al flujo de llamada:
  - sipcore: Core.EnableMedia(); answerWithMedia parsea la oferta entrante, abre la
    sesión, responde 200 con SDP (DialogServerSession.RespondSDP) y arranca el RTP.
    Sesiones indexadas por Call-ID; se liberan en onBye, en el HoldTime del UAS y por
    un backstop en Serve (ctx.Done) y en Close. UACCall.AnswerSDP() lee el SDP del 200.
  - control: el UAC abre el socket, oferta SDP en el INVITE (Content-Type lo ponemos
    nosotros; sipgo solo añade Content-Length), negocia la respuesta y arranca el RTP
    (startUACMedia). Métricas en vivo en CallRec.Media, expuestas en /api/calls.
  - agent: EnableMedia() al arrancar (entrantes contestan con SDP; salientes ofertan).
  - web: nueva columna MEDIA (RTP) en el bloque CALLS (códec, ↑tx ↓rx, ✕pérdida en
    rojo si >0, jitter ms; puertos en el title). mediaCell() en app.js, CSS .media.
- VERIFICADO e2e (binario real): default -> uac-2 (:5072), SDP PCMU negociado
  (41339<->50838), RTP bidireccional ~50 paq/s (99->200->300 tx/rx), 172 B/paq
  (12 RTP + 160 G.711), 0 pérdida, jitter ~0.5 ms. Commits 5fecf9f (backend) y
  6448fde (web). Build/vet/tests del proyecto en verde.
- ENTORNO de este PC: Go 1.26.4 en ~/.local/go (instalado sin sudo; PATH en ~/.bashrc);
  el proyecto es un clon git limpio de github.com/kadenas/dimitri-5000 (rama main).
- LÍMITES conocidos: (1) la media del rol UAS (llamada ENTRANTE) fluye pero no
  muestra métricas en CALLS, porque las entrantes no se registran como CallRec (las
  salientes sí). (2) Sin re-INVITE/HOLD aún. (3) Solo G.711.

## Sesión 9 (cont.) — Fase 5.2 (enviar audio propio) primera entrega
- FUENTE DE AUDIO (internal/media/source.go): interfaz Source.NextFrame; ToneSource
  (tono 440 Hz, por defecto) y PCMSource (reproduce un buffer PCM en bucle). El bucle
  de envío de la sesión RTP tira de la fuente; Session.SetSource la cambia antes de
  Start. Así el audio enviado deja de ser solo el tono.
- DECODIFICADOR WAV (internal/media/wav.go, sin dependencias): DecodeWAV parsea los
  chunks RIFF, soporta PCM 8/16 bits y cualquier nº de canales/frecuencia: mezcla a
  mono y resamplea a 8 kHz por interpolación lineal. Tests de WAV y de las fuentes.
- CABLEADO: control y core guardan el audio (SetAudio/ClearAudio); el Agent lo
  persiste entre stop/start y lo aplica a su control (salientes) y su core (entrantes).
  webui: POST /api/media (multipart agent_id+WAV) decodifica y carga; GET /api/media
  (estado por agente); POST /api/media/clear (vuelve al tono). UI: panel AUDIO en
  PLACE CALL (selector de agente, subir WAV, estado en segundos, CLEAR).
- VERIFICADO e2e: subir un WAV 44100 Hz ESTÉREO -> 8000 muestras (1.0s; downmix +
  resampleo a 8 kHz correctos) -> llamada default->uac-2 con RTP fluyendo desde el
  audio (150/150 paq, 0 pérdida); CLEAR vuelve al tono. Commit 57e015c.
- PRÓXIMO (Fase 5.3): grabar el RTP entrante a WAV y oírlo en el navegador con
  <audio>. Luego 5.4: HOLD real (re-INVITE a=sendonly/inactive). Opcional: MP3 con
  github.com/hajimehoshi/go-mp3 (Go puro, sin CGO; APROBAR antes de añadir la dep).
  Opcional: registrar las llamadas ENTRANTES como CallRec para ver sus métricas.

## Sesión 8 — centrado de la web + Paso B (escenarios en la web) + plan Fase 5
- CENTRADO UI (solo CSS): body pasa a flex column (min-height:100vh) y main se
  centra con margin:auto (vertical + horizontal). Header/footer siguen full-width.
- PASO B COMPLETO (escenarios SIPp en la web), verificado e2e por API:
  - internal/scenario/list.go (NUEVO): List(dir) lista/resume los .yaml de una
    carpeta; un YAML roto NO tumba la lista (aparece con su campo Error).
  - control: RunScenario(sc, file, target) + ScenariosSnapshot() — ejecuta el
    runner en goroutine sobre el Core del agente; estados running/ok/failed.
  - webui: GET /api/scenarios, POST /api/scenarios/run, GET /api/scenarios/runs.
    Anti path-traversal (filepath.Base). webui.New recibe scenariosDir.
  - main: flag --scenarios-dir (por defecto examples/scenarios); cableado en
    runWeb y runMonitor.
  - UI v0.13: bloque 07 SCENARIOS (selector FROM AGENT, selector SCENARIO,
    TARGET URI, RUN, RELOAD; tabla de ejecuciones en vivo). Estado 'ok' => .s-ok.
  - Verificado: build + vet + tests en verde; e2e: GET lista 2 escenarios; run
    uac-basico.yaml default->:5072 (UAS secundario) => state=ok.
  - LÍMITE conocido: el runner solo ejecuta escenarios UAC; UAS/save/match/CSV
    siguen pendientes (ampliación posterior del runner).
- FASE 5 (RTP/media) PLANIFICADA Y DECIDIDA — alcance COMPLETO CON AUDIO,
  incremental en sub-pasos:
  - 5.1 Media base (SIN dependencias): nuevo paquete internal/media (socket RTP,
    cabecera RTP y G.711 µ-law/A-law a mano); SDP oferta/respuesta con IP:puerto
    reales; parseo del SDP remoto; métricas (paquetes tx/rx, pérdida, jitter) en
    el bloque CALLS. A verificar: API sipgo para responder 200 con cuerpo SDP y
    para el re-INVITE.
  - 5.2 Enviar audio propio: subir fichero -> G.711 -> RTP. EMPEZAR POR WAV (sin
    deps); MP3 DESPUÉS con github.com/hajimehoshi/go-mp3 (Go puro, sin CGO;
    APROBAR antes de añadirla).
  - 5.3 Oír las llamadas: grabar el RTP entrante a WAV + reproducir en el
    navegador con <audio> (portable; sin audio del SO en el servidor).
  - 5.4 HOLD real: re-INVITE a=sendonly/inactive (activa los botones HOLD/RESUME
    que ya existen deshabilitados).
- PRÓXIMO ARRANQUE (mañana, desde otro PC): empezar por el sub-paso 5.1.

## Sesión 7 (cont.) — REFER, botonera y traza con headers (v0.11/v0.12)
- DESVÍO (REFER): sipcore.UACCall.Refer(referTo) envía REFER in-dialog (sipgo monta
  cabeceras de diálogo; fijamos Request-URI=contacto remoto + Refer-To). onRefer
  acepta entrantes con 202. control.Transfer(id, referTo). webui POST
  /api/call/transfer. Verificado: REFER->202, CSeq correcto, visible en traza.
- BOTONERA de la llamada SELECCIONADA (panel 03 CALLS): clic en una fila la
  selecciona (resaltada); barra con HOLD/RESUME (deshabilitados: requieren media),
  XFER (usa input REFER-TO) y HANGUP. Resuelve operar 1 llamada entre muchas.
- TRAZA con CONTENIDO: el Store ya guardaba 'raw'; ahora clic en un mensaje del
  ladder muestra el SIP completo (cabeceras+cuerpo) en panel #lad-detail. v0.12.

## Pendientes pedidos por el usuario (entorno real Asterisk/Oracle SBC/Kamailio)
- RTP/media (Fase 5): SDP con puertos negociados, RTP G.711 enviar/recibir/medir;
  habilita HOLD real (re-INVITE a=sendonly/inactive) y oír llamadas. G.729: solo
  medir/ofertar (decode necesitaría bcg729/cgo GPL).
- Escenarios SIPp en la web (Paso B): runner ya existe; falta UI para listar/lanzar.

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
