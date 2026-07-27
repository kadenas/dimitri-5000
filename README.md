# dimitri-5000

***English** · [Español](README.es.md)*

**Test SIP/VoIP PBXs, trunks and SBCs from your browser.** Place and receive
calls with real audio, generate load of thousands of calls per second, and watch
the SIP trace as a live ladder diagram. A single binary, nothing to install.

[![Go](https://img.shields.io/badge/Go-1.23%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)
![Platforms](https://img.shields.io/badge/Windows%20%7C%20Linux-single%20binary-blue)
![SIP](https://img.shields.io/badge/SIP-RFC%203261-orange)

A modern alternative to **SIPp**: the same power to test VoIP with controlled
traffic, but with **readable YAML** scenarios instead of XML and a **real-time
web UI** instead of an ncurses screen.

![Load test running: 50 concurrent calls at 10 cps with live PDD and RTP counters](docs/load-test.png)

<sub>A load test in flight: 50 concurrent calls, live PDD (INVITE→200 OK) and RTP packet counters.</sub>

#### At a glance

- 📞 **Real calls** UAC/UAS with RTP audio (G.711), HOLD/RESUME and REFER.
- 🧪 **YAML scenarios**, reproducible, for both the calling and the answering side.
- 🚀 **Load testing** at thousands of cps with live stats (verified at 2000 cps).
- 🔍 **SBC-style trace**: every call, its ladder diagram and the raw message.
- 🖥️ **Multi-agent** on a single machine, all from the browser.

## What can you do with it?

- **Place calls** (UAC) and **receive them** (UAS) with realistic SIP
  identities: From/To with number and domain, P-Asserted-Identity, arbitrary
  headers… whatever it takes to traverse an SBC like a real call would.
- **Real audio**: RTP media with G.711, with live metrics (packets, jitter,
  loss). You can send a tone or your own WAV file.
- **Control the ongoing call**: hang up, put on hold and resume (HOLD/RESUME
  with a real re-INVITE) or transfer it (REFER).
- **Reproducible scenarios** in YAML, SIPp-style but readable: for both the
  calling side (UAC) and the answering side (UAS), with timings, optional
  responses and variables. Examples live in `examples/scenarios/`.
- **Load testing**: N calls at a configurable rate (cps), each running a full
  scenario, with live statistics. You can pin the A number (caller) and the B
  number (callee) from the panel: every call in the test goes out with that
  numbering (in the From and in the To/Request-URI), ready to route by number in
  your SBC or PBX; if the load uses a scenario, those numbers override its
  `{caller}` and `{callee}` variables.
- **Reusable destinations**: add your SBC or PBX once (name, IP, port, transport
  and To domain) and then pick it from a dropdown to place calls, run scenarios
  or start a load test — no need to retype the URI. The catalog is stored in
  `config.json`, so it is still there after a restart.
- **Monitor trunks** with OPTIONS: status, response code and RTT of each trunk,
  with a configurable failure threshold.
- **See what goes over the wire**: an SBC-style trace viewer with every call
  (status, duration, source/destination) and, on click, its ladder diagram
  message by message; each message can be opened raw.
- **Several agents at once**: each agent is an independent SIP instance (its own
  IP, port and answering behavior), so you can simulate both ends of a call on
  the same machine.

Everything lives in the web UI, organized into 7 panels: AGENTS, PLACE CALL,
CALLS, TRUNKS/DESTINATIONS, SIP TRACE, SCENARIOS and LOAD TEST.

## Screenshots

**SIP trace** — every call, its ladder diagram and the raw message with its SDP:

![SIP trace: call list, ladder diagram and raw INVITE with SDP](docs/sip-trace.png)

<details>
<summary><b>More screenshots</b> — agents, place call, trunks, scenarios</summary>

**01 AGENTS · 02 PLACE CALL** — each agent is an independent SIP instance:

![AGENTS and PLACE CALL panels](docs/agents-and-call.png)

**03 CALLS · 04 TRUNKS/OPTIONS** — live calls with HOLD/RESUME/XFER, and trunk monitoring:

![CALLS and TRUNKS panels](docs/calls-and-trunks.png)

**06 SCENARIOS · 07 LOAD TEST** — run a YAML scenario, or drive it as load:

![SCENARIOS and LOAD TEST panels](docs/scenarios-and-load.png)

</details>

## Quick start

You need [Go 1.23+](https://go.dev/dl/). From the project folder:

```bash
# Linux / macOS
./run-web.sh
```

```powershell
# Windows
.\run-web.ps1
```

Open `http://127.0.0.1:8080` and you're in. The script starts on loopback (SIP
on 127.0.0.1:5070), which is perfect for a first taste: create a second agent in
the AGENTS panel (say on port 5071), place a call between the two from PLACE
CALL, and watch the trace in SIP TRACE.

To talk to real equipment (an Asterisk, an SBC…), edit the variables at the top
of `run-web.sh` / `run-web.ps1`: leave `BIND_IP` empty so it autodetects your
network card's IP, and set `WEB_ADDR` to `0.0.0.0:8080` if you want to open the
UI from another machine on the LAN.

If you prefer the direct command:

```bash
go run . --mode web --bind-ip "" --sip-port 5070 --web 127.0.0.1:8080
```

## Execution modes

The `web` mode is the main one and what you'll want almost always. The others
are handy for automation or for debugging without a browser:

| Mode | What it does | Example |
|---|---|---|
| `web` | Full workstation: agents, calls, scenarios, load and traces from the browser | `go run . --mode web` |
| `uac` | Places ONE call, holds it for a while and hangs up | `go run . --mode uac --to sip:192.0.2.10:5060 --hold 10s` |
| `uas` | Listens and answers incoming calls | `go run . --mode uas --sip-port 5060 --answer-code 200` |
| `scenario` | Runs a YAML scenario from the CLI | `go run . --mode scenario --file examples/scenarios/uac-basico.yaml --to sip:192.0.2.30:5060` |
| `monitor` | Only the trunk beacon (OPTIONS) + status web | `go run . --mode monitor --config config.json` |

`go run . --help` lists every flag (UDP/TCP transport, From domain, UAS response
code, ringing time…).

## Configuration

For monitor mode (or to pin IP/port without flags) you can use a JSON file:

```bash
cp config.example.json config.json   # edit it with your IPs and trunks
go run . --config config.json
```

`config.json` is kept out of the repository on purpose: it usually holds
internal IPs. In `web` mode it stores the **destination catalog** (whatever you
add in the TRUNKS/DESTINATIONS panel): that is the only thing that survives a
restart. Agents, and which agent monitors what, live in memory while the app
runs.

## Scenarios

A scenario describes a SIP flow as a sequence of steps. This one, for example,
is a basic call from the calling side:

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

You can also script the answering side (`role: uas`): how long it takes to send
the 180, which code it answers with, when it hangs up. The full format reference
is in `SCENARIO_FORMAT.md`, and `examples/scenarios/` has ready-to-use scenarios
for both sides. Scenarios run from the web (SCENARIOS panel), from the CLI
(`--mode scenario`), or as the template for each call in a load test.

## Build a binary

```bash
go build -ldflags "-s -w" -o dist/dimitri-5000 .
```

You get a single self-contained executable (the web UI is embedded): copy it to
the target machine and you're done, nothing else to install. `DESPLIEGUE.md` has
the details, including cross-compilation Windows ⇄ Linux.

## Who is it for?

VoIP engineers, QA and carriers who need to test PBXs, trunks and SBCs: validate
call flows, measure behavior under load and reproduce incidents without
wrestling with XML.

## Documentation

Project docs are written in Spanish:

- `FICHA_TECNICA.md` — architecture, stack and phased plan.
- `SCENARIO_FORMAT.md` — scenario language reference.
- `DESPLIEGUE.md` — how to build and deploy on Windows and Ubuntu.
- `HANDOFF.md` — development log: what's done and what's left.

## License

This project is distributed under the **MIT** license — Copyright (c) 2026
Jerónimo Mosquera. Full text in [`LICENSE`](LICENSE).

### Third-party dependencies

The SIP engine builds on **[sipgo](https://github.com/emiago/sipgo)** by Emir
Aganovic, distributed under the **BSD 2-Clause** license. Thanks to that project
for solving the hardest part of RFC 3261 (transactions, retransmissions, dialogs
and digest auth).

The remaining dependencies are equally permissive and MIT-compatible:

| Dependency | License |
|---|---|
| `github.com/emiago/sipgo` | BSD 2-Clause |
| `gopkg.in/yaml.v3` | MIT / Apache-2.0 |
| `github.com/gobwas/ws`, `gobwas/pool`, `gobwas/httphead` | MIT |
| `github.com/icholy/digest` | MIT |
| `github.com/kr/text` | MIT |
| `github.com/google/uuid` | BSD 3-Clause |
| `golang.org/x/sync`, `golang.org/x/sys` | BSD 3-Clause |

Full license texts for each dependency are in
[`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md).
