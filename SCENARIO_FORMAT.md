# Formato de escenarios — dimitri-5000 (v0.1)

Los escenarios describen un flujo de prueba SIP como una **secuencia de pasos**
(una máquina de estados), al estilo de SIPp pero en **YAML legible**. Este documento
es la referencia del lenguaje; el cargador y la validación viven en
`internal/scenario`.

> Estado: definido y con cargador + validación implementados. El **runner**
> (ejecución contra el motor SIP) es el siguiente paso de la Fase 2.

## Estructura general

```yaml
name: <identificador>          # obligatorio
role: uac | uas                # obligatorio
description: <texto>           # opcional

defaults:                      # opcional
  transport: udp | tcp
  recv_timeout: <duración>     # espera máxima por defecto en cada recv (ej: 5s)

variables:                     # opcional: variables iniciales
  clave: valor

steps:                         # obligatorio: al menos un paso
  - <paso>
  - <paso>

bodies:                        # opcional: cuerpos reutilizables (SDP)
  <nombre>:
    type: sdp
    media: g711                # alias de media (audio real en Fase 5)
    content: <texto>           # alternativa: cuerpo literal

inject:                        # opcional: datos por llamada desde CSV
  file: datos.csv
  order: sequential | random
```

## Tipos de paso

Cada paso tiene **una** acción principal: `send`, `recv` o `pause` (o ser un paso
de solo acciones con `save`/`log`).

### send
Envía una petición (método) o, en `role: uas`, una respuesta (código).

```yaml
- send: INVITE
  headers:                     # cabeceras que controlas tú
    From: "sip:{caller}@{domain}"
    To: "sip:{callee}@{domain}"
  body: sdp_oferta             # referencia a bodies
```

Escape de control total (como SIPp): `raw` con el mensaje crudo.

```yaml
- send: INVITE
  raw: |
    INVITE sip:{callee}@{domain} SIP/2.0
    ...
```

### recv (explícito)
Se declara **cada** mensaje esperado. Los provisionales (100/180) se marcan
`optional: true` si pueden no llegar.

```yaml
- recv: "200"                  # código de respuesta (entre comillas en YAML)
  timeout: 8s                  # sobrescribe defaults.recv_timeout
  optional: false
  save:                        # capturar valores del mensaje recibido
    remote_tag: "{header:To;tag}"
  match:                       # validar (regex) sobre el mensaje
    "header:From": ".*@midominio"
```

`recv` también acepta un método cuando se espera una petición (ej. `recv: BYE`).

### pause
```yaml
- pause: 3s                    # formato de duración Go: 500ms, 3s, 1m
```

### Paso de solo acciones (≈ nop)
```yaml
- log: "llamada a punto de colgar"
  save:
    x: "{header:Call-ID}"
```

## Placeholders de variables

Sintaxis con llaves: `{nombre}`.

- **Variables del escenario:** las definidas en `variables:`.
- **Internas automáticas** (las rellena el runner): `{call_id}`, `{branch}`,
  `{cseq}`, `{local_ip}`, `{local_port}`, `{remote_host}`, `{remote_port}`.
- **Captura desde mensajes** (en `save`/`match`):
  - `{header:To}` — valor de la cabecera.
  - `{header:To;tag}` — parámetro de una cabecera.
  - `{regex:Contact:sip:(.*)>}` — primer grupo de una expresión regular.

## Validación

El cargador valida y da errores claros indicando el número de paso. Detecta, entre
otros: `role` inválido, escenario sin pasos, `body` referenciado pero no definido,
`send` de un código de respuesta en un escenario `uac`, duraciones mal escritas,
métodos/códigos no reconocidos y **campos desconocidos** (un `stpes:` mal escrito
es un error, no un silencio).

## Ejemplos

Ver `examples/scenarios/uac-basico.yaml` y `examples/scenarios/uas-contesta.yaml`.
