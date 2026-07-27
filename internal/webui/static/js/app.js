// app.js — DIMITRI//5000. Controla la web: lanza llamadas, las cuelga y refresca
// el estado de llamadas y troncales. No contiene lógica SIP: consume la API.

const POLL_MS = 1000;

// Etiquetas legibles por estado (llamadas, troncales y agentes comparten varias).
const LABEL = {
  dialing: "DIALING", ringing: "RINGING", established: "ESTABLISHED",
  ended: "ENDED", failed: "FAILED",
  up: "UP", degraded: "DEGRADED", down: "DOWN", unknown: "—",
  running: "RUNNING", stopped: "STOPPED", ok: "OK",
};

// Recordamos qué agente está elegido en PLACE CALL para no perderlo al refrescar.
let selectedAgent = "default";   // agente que ORIGINA la llamada
// Los desplegables de DESTINO guardan un valor con prefijo: "dest:<id>" (destino
// del catálogo) o "agent:<id>" (otro agente local). "" = manual / externo.
let selectedCallDest = "";       // destino de PLACE CALL
let selectedTrunkAgent = "default"; // agente que sondea con OPTIONS
let selectedTrunkDest = "";           // destino del catálogo a monitorizar
let selectedScenarioAgent = "default"; // agente que EJECUTA el escenario
let selectedScenarioFile = "";        // fichero de escenario elegido en el desplegable
let selectedScenarioDest = "";        // destino del escenario
let selectedAudioAgent = "default";   // agente al que se sube el audio (RTP)
let selectedLoadAgent = "default";    // agente que ORIGINA la prueba de carga
let selectedLoadDest = "";            // destino de la carga
let agentsCache = [];            // última lista de agentes (para resolver destino)
let destsCache = [];             // catálogo de destinos remotos (SBC, centralitas)
let uasScenariosCache = [];      // escenarios role uas disponibles (selector por agente)
let selectedCall = "";           // Call-ID elegido en el ladder
let showOptions = false;         // mostrar diálogos de OPTIONS (keepalive) en el ladder
let selectedCallId = "";         // id de la llamada seleccionada para la botonera
let ladderArrows = [];           // flechas del ladder actual (para el detalle de mensaje)

// Escapa texto para no inyectar HTML al construir filas.
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])
  );
}

// Muestra solo la hora (HH:MM:SS) de un timestamp RFC3339, o "—".
function hhmmss(ts) {
  if (!ts) return "—";
  const d = new Date(ts);
  return isNaN(d) ? "—" : d.toLocaleTimeString("es-ES");
}

function codeText(code, reason) {
  if (!code) return "—";
  return reason ? code + " " + reason : String(code);
}

function badge(state) {
  return '<span class="badge s-' + esc(state) + '">' +
    (LABEL[state] || esc(state)) + "</span>";
}

// ---- Lanzar / colgar llamadas ----

async function placeCall(payload) {
  const res = await fetch("/api/call", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

// Convierte el textarea de cabeceras ("Nombre: Valor" por línea) en un objeto.
function parseHeaders(texto) {
  const out = {};
  (texto || "").split("\n").forEach((linea) => {
    const l = linea.trim();
    if (!l) return;
    const i = l.indexOf(":");
    if (i > 0) out[l.slice(0, i).trim()] = l.slice(i + 1).trim();
  });
  return out;
}

// Lee un input por id y devuelve su valor recortado (o "").
function val(id) {
  const el = document.getElementById(id);
  return el ? el.value.trim() : "";
}

async function hangup(id) {
  await fetch("/api/call/hangup", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
}

// ---- Agentes (instancias SIP) ----

async function addAgent(spec) {
  const res = await fetch("/api/agents", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(spec),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

// Acción simple sobre un agente: start | stop | remove.
async function agentAction(accion, id) {
  const res = await fetch("/api/agents/" + accion, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
  if (!res.ok) throw new Error(await res.text());
}


// ---- Pintado de tablas ----

function renderAgents(datos) {
  const tbody = document.getElementById("agents");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="6">NO AGENTS</td></tr>';
    return;
  }
  tbody.innerHTML = datos.map((a) => {
    const corriendo = a.state === "running";
    // Si está corriendo se puede parar; si está parado se puede arrancar.
    const toggle = corriendo
      ? '<button class="btn-mini" data-act="stop" data-id="' + esc(a.id) + '">STOP</button>'
      : '<button class="btn-mini go" data-act="start" data-id="' + esc(a.id) + '">START</button>';
    const quitar = '<button class="btn-mini danger" data-act="remove" data-id="' + esc(a.id) + '">REMOVE</button>';
    // Selector del escenario UAS: cómo contesta este agente a las llamadas entrantes.
    // "" = auto-answer fijo (la política de su alta). Marcamos el asignado por nombre.
    const opciones = ['<option value="">— auto-answer —</option>'].concat(
      uasScenariosCache.map((s) => {
        const sel = s.name && s.name === a.uas_scenario ? " selected" : "";
        return '<option value="' + esc(s.file) + '"' + sel + ">" + esc(s.name || s.file) + "</option>";
      })
    ).join("");
    const uasSel = '<select class="uas-sel" data-uas-id="' + esc(a.id) + '">' + opciones + "</select>";
    return "<tr>" +
      "<td>" + esc(a.id) + "</td>" +
      "<td>" + esc(a.name || a.id) + "</td>" +
      "<td>" + esc(a.bind_ip) + ":" + esc(a.sip_port) + "</td>" +
      "<td>" + esc(String(a.transport).toUpperCase()) + "</td>" +
      "<td>" + badge(a.state) + "</td>" +
      "<td>" + uasSel + "</td>" +
      '<td class="right">' + toggle + " " + quitar + "</td>" +
      "</tr>";
  }).join("");

  // Enganchar las acciones de cada agente.
  tbody.querySelectorAll(".btn-mini[data-act]").forEach((b) => {
    b.addEventListener("click", () => {
      agentAction(b.dataset.act, b.dataset.id).then(refresh).catch((e) => {
        document.getElementById("agent-hint").textContent = "Error: " + e.message;
        document.getElementById("agent-hint").className = "hint error";
      });
    });
  });

  // Asignar/quitar escenario UAS al cambiar el selector de un agente.
  tbody.querySelectorAll(".uas-sel").forEach((sel) => {
    sel.addEventListener("change", async () => {
      const hint = document.getElementById("agent-hint");
      try {
        const res = await fetch("/api/agents/uas-scenario", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ agent_id: sel.dataset.uasId, file: sel.value }),
        });
        if (!res.ok) throw new Error(await res.text());
        hint.textContent = sel.value
          ? "Escenario UAS asignado a " + sel.dataset.uasId + "."
          : "Agente " + sel.dataset.uasId + " vuelve al auto-answer.";
        hint.className = "hint";
        refresh();
      } catch (e) {
        hint.textContent = "Error: " + e.message;
        hint.className = "hint error";
      }
    });
  });
}

// Rellena los selectores de agente (origen y destino) conservando la elección.
function renderAgentSelector(datos) {
  agentsCache = datos || [];
  const ids = agentsCache.map((a) => a.id);

  // --- Origen (FROM AGENT): quién lanza la llamada ---
  const sel = document.getElementById("call-agent");
  if (!ids.includes(selectedAgent)) selectedAgent = ids[0] || "default";
  sel.innerHTML = agentsCache.map((a) => {
    const marca = a.state === "running" ? "" : " (stopped)";
    return '<option value="' + esc(a.id) + '">' + esc(a.id) + marca + "</option>";
  }).join("");
  sel.value = selectedAgent;

  // --- Destino: a dónde se llama. Primera opción = manual/externo ---
  fillDestSelect("call-dest", selectedCallDest);

  // Selector del agente que sondea con OPTIONS (mismo origen de agentes).
  const tr = document.getElementById("tr-agent");
  if (tr) {
    if (!ids.includes(selectedTrunkAgent)) selectedTrunkAgent = ids[0] || "default";
    tr.innerHTML = sel.innerHTML;
    tr.value = selectedTrunkAgent;
  }

  // Selector del agente que EJECUTA escenarios (mismo origen de agentes).
  const sc = document.getElementById("sc-agent");
  if (sc) {
    if (!ids.includes(selectedScenarioAgent)) selectedScenarioAgent = ids[0] || "default";
    sc.innerHTML = sel.innerHTML;
    sc.value = selectedScenarioAgent;
  }

  // Selector del agente al que se sube el audio (mismo origen de agentes).
  const au = document.getElementById("audio-agent");
  if (au) {
    if (!ids.includes(selectedAudioAgent)) selectedAudioAgent = ids[0] || "default";
    au.innerHTML = sel.innerHTML;
    au.value = selectedAudioAgent;
  }

  // Selector del agente que ORIGINA la carga (mismo origen de agentes).
  const lo = document.getElementById("load-agent");
  if (lo) {
    if (!ids.includes(selectedLoadAgent)) selectedLoadAgent = ids[0] || "default";
    lo.innerHTML = sel.innerHTML;
    lo.value = selectedLoadAgent;
  }
  // Destinos de la carga y de los escenarios (mismo desplegable unificado).
  fillDestSelect("load-dest", selectedLoadDest);
  fillDestSelect("sc-dest", selectedScenarioDest);
}

// fillDestSelect rellena un desplegable de DESTINO: los destinos del catálogo
// (SBC, centralitas) y, aparte, los agentes locales para llamarse entre sí. El
// valor lleva prefijo ("dest:" / "agent:") porque un mismo id podría existir en
// los dos grupos y hay que saber de cuál se trata.
function fillDestSelect(selId, current) {
  const dst = document.getElementById(selId);
  if (!dst) return;

  const opciones = ['<option value="">— manual / externo —</option>'];
  if (destsCache.length) {
    opciones.push('<optgroup label="DESTINOS (catálogo)">');
    destsCache.forEach((d) => {
      const dirn = esc(d.host) + ":" + esc(d.port);
      const nombre = d.name ? esc(d.id) + " · " + esc(d.name) : esc(d.id);
      opciones.push('<option value="dest:' + esc(d.id) + '">' + nombre + " · " + dirn + "</option>");
    });
    opciones.push("</optgroup>");
  }
  if (agentsCache.length) {
    opciones.push('<optgroup label="AGENTES LOCALES">');
    agentsCache.forEach((a) => {
      const dirn = esc(a.bind_ip) + ":" + esc(a.sip_port);
      opciones.push('<option value="agent:' + esc(a.id) + '">' + esc(a.id) + " · " + dirn + "</option>");
    });
    opciones.push("</optgroup>");
  }
  dst.innerHTML = opciones.join("");

  // Si el destino recordado ya no existe (se borró del catálogo, o el agente se
  // dio de baja), el desplegable vuelve a "manual" en lugar de quedarse con un
  // valor fantasma que luego el servidor rechazaría.
  dst.value = current || "";
  if (dst.value !== (current || "")) dst.value = "";
}

// destByValue traduce el valor de un desplegable de destino a datos concretos.
// Devuelve null si el valor está vacío (destino manual) o ya no existe.
function destByValue(value) {
  if (!value) return null;
  const sep = value.indexOf(":");
  if (sep < 0) return null;
  const kind = value.slice(0, sep);
  const id = value.slice(sep + 1);
  if (kind === "agent") {
    const a = agentsCache.find((x) => x.id === id);
    return a ? { kind, id, host: a.bind_ip, port: a.sip_port } : null;
  }
  if (kind === "dest") {
    const d = destsCache.find((x) => x.id === id);
    return d ? { kind, id, host: d.host, port: d.port } : null;
  }
  return null;
}

// destPayload vuelca el destino elegido en el cuerpo de la petición. Un destino del
// catálogo viaja como 'dest_id' y lo resuelve el servidor (de su ficha salen host,
// puerto, transporte y dominio del To); un agente local va como host + puerto.
function destPayload(value) {
  const d = destByValue(value);
  if (!d) return {};
  if (d.kind === "dest") return { dest_id: d.id };
  return { dest_host: d.host, dest_port: d.port };
}

// destUri es la URI que se vuelca en el campo TARGET URI al elegir un destino:
// informativa, para ver de un vistazo a dónde va a salir el paquete.
function destUri(value) {
  const d = destByValue(value);
  return d ? "sip:" + d.host + ":" + d.port : "";
}

// renderDests pinta el catálogo de destinos y engancha sus botones de baja.
function renderDests(datos) {
  destsCache = datos || [];
  const tbody = document.getElementById("dests");
  if (!tbody) return;
  if (destsCache.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="7">NO DESTINATIONS</td></tr>';
  } else {
    tbody.innerHTML = destsCache.map((d) =>
      "<tr>" +
      "<td>" + esc(d.id) + "</td>" +
      "<td>" + esc(d.name || "—") + "</td>" +
      "<td>" + esc(d.host) + "</td>" +
      "<td>" + esc(d.port) + "</td>" +
      "<td>" + esc(d.transport || "UDP") + "</td>" +
      "<td>" + esc(d.to_domain || "—") + "</td>" +
      '<td class="right"><button class="btn-mini danger" data-dest="' + esc(d.id) + '">DELETE</button></td>' +
      "</tr>"
    ).join("");

    tbody.querySelectorAll(".btn-mini[data-dest]").forEach((b) => {
      b.addEventListener("click", async () => {
        const hint = document.getElementById("dest-hint");
        try {
          const res = await fetch("/api/destinations/remove", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ id: b.dataset.dest }),
          });
          if (!res.ok) throw new Error(await res.text());
          hint.textContent = "Destino borrado: " + b.dataset.dest;
          hint.className = "hint";
          refresh();
        } catch (e) {
          hint.textContent = "Error: " + e.message;
          hint.className = "hint error";
        }
      });
    });
  }

  // Desplegable del alta de monitorización: solo destinos del catálogo (un agente
  // no se monitoriza a sí mismo con OPTIONS desde este panel).
  const tr = document.getElementById("tr-dest");
  if (tr) {
    tr.innerHTML = destsCache.length
      ? destsCache.map((d) =>
          '<option value="' + esc(d.id) + '">' + esc(d.id) + " · " +
          esc(d.host) + ":" + esc(d.port) + "</option>").join("")
      : '<option value="">— sin destinos —</option>';
    tr.value = selectedTrunkDest || (destsCache[0] ? destsCache[0].id : "");
    selectedTrunkDest = tr.value;
  }
}

// Resume las métricas de media (RTP) de una llamada en una celda compacta:
// códec, paquetes enviados (↑) y recibidos (↓), pérdida (✕) y jitter. El detalle
// (puertos local/remoto) va en el title para no recargar la fila.
function mediaCell(m) {
  if (!m) return '<span class="muted">—</span>';
  const jit = typeof m.jitter_ms === "number" ? m.jitter_ms.toFixed(1) : "0.0";
  const lossCls = m.lost > 0 ? ' class="m-loss"' : "";
  return '<span class="media" title="' +
      esc((m.codec || "?") + "  :" + (m.local_port || 0) + " <-> " + (m.remote_addr || "?")) +
      '"><b>' + esc(m.codec || "?") + "</b> " +
      "&uarr;" + (m.tx_packets || 0) + " &darr;" + (m.rx_packets || 0) +
      ' <span' + lossCls + ">&#10005;" + (m.lost || 0) + "</span> " +
      jit + "ms</span>";
}

function renderCalls(datos) {
  const tbody = document.getElementById("calls");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="9">NO ACTIVE CALLS</td></tr>';
    return;
  }
  // Si la llamada seleccionada ya no está, deseleccionamos.
  if (selectedCallId && !datos.some((c) => c.id === selectedCallId)) selectedCallId = "";

  // Las más recientes arriba.
  const filas = datos.slice().reverse().map((c) => {
    const activa = c.state === "dialing" || c.state === "ringing" || c.state === "established";
    const accion = activa
      ? '<button class="btn-hangup" data-id="' + esc(c.id) + '">HANGUP</button>'
      : '<button class="btn-hangup" disabled>—</button>';
    const sel = c.id === selectedCallId ? ' class="row-sel"' : "";
    return "<tr data-call=\"" + esc(c.id) + "\"" + sel + ">" +
      "<td>" + esc(c.agent_id) + "</td>" +
      "<td>" + esc(c.id) + "</td>" +
      "<td>" + esc(c.to) + "</td>" +
      "<td>" + badge(c.state) + (c.on_hold ? ' <span class="badge s-hold">ON HOLD</span>' : "") + "</td>" +
      "<td>" + esc(codeText(c.last_code, c.last_reason)) + "</td>" +
      "<td>" + hhmmss(c.started_at) + "</td>" +
      "<td>" + hhmmss(c.answered_at) + "</td>" +
      "<td>" + mediaCell(c.media) + "</td>" +
      '<td class="right">' + accion + "</td>" +
      "</tr>";
  });
  tbody.innerHTML = filas.join("");

  // Clic en una fila: seleccionar la llamada para la botonera.
  tbody.querySelectorAll("tr[data-call]").forEach((tr) => {
    tr.addEventListener("click", (ev) => {
      if (ev.target.closest("button")) return; // no robar el clic a HANGUP
      selectedCallId = tr.dataset.call;
      updateCallControl();
      renderCalls(datos); // re-pinta para resaltar la fila
    });
  });

  // Botones de colgar por fila.
  tbody.querySelectorAll(".btn-hangup[data-id]").forEach((b) => {
    b.addEventListener("click", () => hangup(b.dataset.id).then(refresh));
  });

  updateCallControl();
}

// Refleja la llamada seleccionada en la botonera (id mostrado).
function updateCallControl() {
  const inp = document.getElementById("sel-call");
  if (inp) inp.value = selectedCallId || "";
}

function renderTrunks(datos) {
  const tbody = document.getElementById("trunks");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="10">NO TRUNKS</td></tr>';
    return;
  }
  tbody.innerHTML = datos.map((t) =>
    "<tr>" +
    "<td>" + esc(t.agent_id) + "</td>" +
    "<td>" + esc(t.name || t.id) + "</td>" +
    "<td>" + esc(t.host) + ":" + esc(t.port) + "</td>" +
    "<td>" + badge(t.status) + "</td>" +
    "<td>" + esc(codeText(t.last_code, t.last_reason)) + "</td>" +
    "<td>" + (t.last_rtt_ms > 0 ? t.last_rtt_ms + " ms" : "—") + "</td>" +
    "<td>" + (t.ok || 0) + "</td>" +
    "<td>" + (t.other || 0) + "</td>" +
    "<td>" + (t.timeout || 0) + "</td>" +
    '<td class="right"><button class="btn-mini danger" data-agent="' + esc(t.agent_id) +
      '" data-id="' + esc(t.id) + '">REMOVE</button></td>' +
    "</tr>"
  ).join("");

  // Enganchar los botones de baja de trunk.
  tbody.querySelectorAll(".btn-mini[data-id]").forEach((b) => {
    b.addEventListener("click", () => {
      fetch("/api/trunks/remove", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ agent_id: b.dataset.agent, id: b.dataset.id }),
      }).then(refresh);
    });
  });
}

// ---- Traza SIP / diagrama de escalera ----

// Clase CSS de color para cada estado (mimetiza el visor de un SBC).
function stateClass(state) {
  if (!state) return "st-dim";
  if (state.startsWith("ESTABLISHED")) return "st-estab";
  if (state.startsWith("FAILED")) return "st-failed";
  if (state.startsWith("TERMINATED")) return "st-term";
  if (state === "RINGING" || state === "EARLY") return "st-early";
  return "st-dim";
}

// Hora legible "2026-06-29 09:04:39.123" a partir del ISO con 'T'.
function traceStart(t) {
  return t ? t.replace("T", " ") : "—";
}

// Celda con texto recortado por CSS y el valor completo en el tooltip.
function cell(val) {
  const v = val || "—";
  return '<td title="' + esc(v) + '">' + esc(v) + "</td>";
}

// Pinta la tabla de diálogos tipo SBC (filtra OPTIONS salvo que se marque).
function renderTraceCalls(calls) {
  const tbody = document.getElementById("trace-table");
  let lista = calls || [];
  if (!showOptions) {
    lista = lista.filter((c) => c.method !== "OPTIONS");
  }
  // Si la llamada seleccionada ya no está, soltamos la selección.
  if (selectedCall && !lista.some((c) => c.call_id === selectedCall)) {
    selectedCall = "";
  }
  if (lista.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="11">NO TRACE</td></tr>';
    return;
  }

  tbody.innerHTML = lista.map((c) => {
    const sel = c.call_id === selectedCall ? " sel" : "";
    const dur = c.duration_sec ? c.duration_sec : 0;
    return '<tr class="trace-row' + sel + '" data-call-id="' + esc(c.call_id) + '">' +
      cell(traceStart(c.start_time)) +
      '<td class="' + stateClass(c.state) + '">' + esc(c.state || "—") + "</td>" +
      cell(c.call_id) +
      cell(c.req_uri) +
      cell(c.from_uri) +
      cell(c.to_uri) +
      cell(c.orig_agent) +
      cell(c.dest_agent) +
      cell(c.laddr) +
      cell(c.raddr) +
      '<td class="right">' + dur + "</td>" +
      "</tr>";
  }).join("");

  // Clic en una fila: seleccionar esa llamada y mostrar su ladder.
  tbody.querySelectorAll(".trace-row").forEach((row) => {
    row.addEventListener("click", () => {
      selectedCall = row.dataset.callId;
      tbody.querySelectorAll(".trace-row.sel").forEach((r) => r.classList.remove("sel"));
      row.classList.add("sel");
      refreshTrace();
    });
  });
}

// Construye las flechas del ladder a partir de los eventos, deduplicando el mismo
// mensaje visto en los dos extremos (cuando ambos agentes son locales).
function buildArrows(events) {
  const arrows = [];
  const seen = new Set();
  for (const e of events) {
    const sender = e.dir === "out" ? e.laddr : e.raddr;
    const receiver = e.dir === "out" ? e.raddr : e.laddr;
    const key = sender + "|" + receiver + "|" + e.cseq + "|" + e.first_line;
    if (seen.has(key)) continue;
    seen.add(key);
    let label;
    if (e.kind === "response") {
      const reason = e.first_line.split(" ").slice(2).join(" ");
      label = e.code + " " + reason + " (" + e.method + ")";
    } else {
      label = e.method;
    }
    arrows.push({ time: e.time, sender, receiver, label, kind: e.kind, raw: e.raw });
  }
  return arrows;
}

function renderLadder(events) {
  const cont = document.getElementById("ladder-rows");
  if (!events || events.length === 0) {
    cont.innerHTML = '<div class="lad-empty">SIN MENSAJES</div>';
    document.getElementById("lane-left").textContent = "—";
    document.getElementById("lane-right").textContent = "—";
    return;
  }
  const arrows = buildArrows(events);

  // Carriles: los dos primeros extremos que aparecen.
  const lanes = [];
  arrows.forEach((a) => {
    [a.sender, a.receiver].forEach((p) => { if (!lanes.includes(p)) lanes.push(p); });
  });
  const left = lanes[0] || "—";
  const right = lanes[1] || "—";
  document.getElementById("lane-left").textContent = left;
  document.getElementById("lane-right").textContent = right;

  ladderArrows = arrows; // para el panel de detalle (cabeceras + cuerpo)
  cont.innerHTML = arrows.map((a, i) => {
    const haciaDerecha = a.sender === left;
    const flecha = haciaDerecha
      ? '<span class="lad-line">────────▶</span>'
      : '<span class="lad-line rev">◀────────</span>';
    const clase = a.kind === "response" ? "resp" : "req";
    const hora = (a.time.split("T")[1] || a.time);
    return '<div class="lad-row lad-click" data-idx="' + i + '">' +
      '<span class="lad-time">' + esc(hora) + "</span>" +
      '<div class="lad-flow ' + (haciaDerecha ? "r" : "l") + '">' +
        '<span class="lad-label ' + clase + '">' + esc(a.label) + "</span>" +
        flecha +
      "</div>" +
      "</div>";
  }).join("");

  // Clic en un mensaje: mostrar su contenido completo (cabeceras + cuerpo).
  cont.querySelectorAll(".lad-click").forEach((row) => {
    row.addEventListener("click", () => {
      const a = ladderArrows[parseInt(row.dataset.idx, 10)];
      document.getElementById("lad-detail").textContent = a && a.raw ? a.raw : "(sin contenido)";
    });
  });
}

// Trae las llamadas de la traza y, si hay una elegida, sus eventos para el ladder.
async function refreshTrace() {
  const calls = await fetch("/api/trace/calls").then((r) => r.json());
  renderTraceCalls(calls);
  if (selectedCall) {
    const ev = await fetch("/api/trace?call_id=" + encodeURIComponent(selectedCall))
      .then((r) => r.json());
    renderLadder(ev);
  } else {
    renderLadder([]);
  }
}

// ---- Escenarios (Fase 2) ----

// Carga la lista de escenarios disponibles en disco y rellena el desplegable,
// conservando la elección. Se llama al arranque y con el botón RELOAD: NO en cada
// refresco, porque listar implica leer y parsear los YAML y no cambian a menudo.
async function loadScenarios() {
  const sel = document.getElementById("sc-file");
  if (!sel) return;
  try {
    const lista = await fetch("/api/scenarios").then((r) => r.json());
    const files = lista.map((s) => s.file);
    if (selectedScenarioFile && !files.includes(selectedScenarioFile)) selectedScenarioFile = "";
    if (!selectedScenarioFile && files.length) selectedScenarioFile = files[0];
    sel.innerHTML = lista.map((s) => {
      // Si el YAML no carga, marcamos el fichero para que se vea cuál falla.
      const etiqueta = s.error
        ? s.file + " · ⚠ ERROR"
        : s.file + " · " + String(s.role || "?").toUpperCase() + " · " + (s.name || "");
      return '<option value="' + esc(s.file) + '">' + esc(etiqueta) + "</option>";
    }).join("") || '<option value="">— sin escenarios —</option>';
    sel.value = selectedScenarioFile;

    // Selector del motor de CARGA: solo escenarios UAC válidos (los que originan
    // llamadas), con una opción para el INVITE básico. Conserva la elección.
    const selLoad = document.getElementById("load-scenario");
    if (selLoad) {
      const prev = selLoad.value;
      const uac = lista.filter((s) => !s.error && String(s.role).toLowerCase() === "uac");
      selLoad.innerHTML =
        '<option value="">(INVITE básico)</option>' +
        uac.map((s) => '<option value="' + esc(s.file) + '">' + esc(s.file + " · " + (s.name || "")) + "</option>").join("");
      if (prev && uac.some((s) => s.file === prev)) selLoad.value = prev;
    }

    // Cache de escenarios UAS válidos: alimenta el selector por agente (cómo
    // contesta cada uno a las llamadas entrantes).
    uasScenariosCache = lista.filter((s) => !s.error && String(s.role).toLowerCase() === "uas");
  } catch (e) {
    sel.innerHTML = '<option value="">— error al listar —</option>';
  }
}

// Pinta el historial de ejecuciones de escenario (las más recientes arriba).
function renderScenarioRuns(datos) {
  const tbody = document.getElementById("scenario-runs");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="7">NO RUNS</td></tr>';
    return;
  }
  tbody.innerHTML = datos.slice().reverse().map((s) => {
    // En caso de fallo mostramos el motivo como tooltip de la pastilla de estado.
    const estado = s.error
      ? '<span class="badge s-failed" title="' + esc(s.error) + '">FAILED</span>'
      : badge(s.state);
    return "<tr>" +
      "<td>" + esc(s.agent_id) + "</td>" +
      "<td>" + esc(s.name || "—") + "</td>" +
      "<td>" + esc(s.file) + "</td>" +
      "<td>" + esc(s.target || "—") + "</td>" +
      "<td>" + estado + "</td>" +
      "<td>" + hhmmss(s.started_at) + "</td>" +
      "<td>" + hhmmss(s.ended_at) + "</td>" +
      "</tr>";
  }).join("");
}

// ---- Refresco periódico ----

async function refresh() {
  const conn = document.getElementById("conn");
  try {
    const [agents, calls, dests, trunks, scenarioRuns, audio, loadStats] = await Promise.all([
      fetch("/api/agents").then((r) => r.json()),
      fetch("/api/calls").then((r) => r.json()),
      fetch("/api/destinations").then((r) => r.json()),
      fetch("/api/trunks").then((r) => r.json()),
      fetch("/api/scenarios/runs").then((r) => r.json()),
      fetch("/api/media").then((r) => r.json()),
      fetch("/api/load").then((r) => r.json()),
    ]);
    renderAgents(agents);
    // El catálogo se pinta ANTES que los selectores: estos incluyen sus destinos.
    renderDests(dests);
    renderAgentSelector(agents);
    renderCalls(calls);
    renderTrunks(trunks);
    renderScenarioRuns(scenarioRuns);
    renderAudioStatus(audio);
    renderLoad(loadStats);
    await refreshTrace();
    conn.textContent = "ONLINE";
    conn.className = "conn ok";
  } catch (e) {
    conn.textContent = "OFFLINE";
    conn.className = "conn bad";
  }
}

// Reloj de la barra de sistema.
function tickClock() {
  document.getElementById("clock").textContent =
    new Date().toLocaleTimeString("es-ES");
}

// ---- Arranque ----

// Recordar el agente de origen elegido al cambiarlo.
document.getElementById("call-agent").addEventListener("change", (ev) => {
  selectedAgent = ev.target.value;
});

// Recordar el destino elegido al cambiarlo y, de paso, volcar su IP:puerto al
// campo TARGET URI (visible y editable) para no teclearlo a mano.
document.getElementById("call-dest").addEventListener("change", (ev) => {
  selectedCallDest = ev.target.value;
  const uri = destUri(selectedCallDest);
  if (uri) document.getElementById("to").value = uri;
});

// Mostrar/ocultar el bloque de valores SIP avanzados.
document.getElementById("adv-toggle").addEventListener("click", () => {
  const adv = document.getElementById("adv");
  const btn = document.getElementById("adv-toggle");
  const oculto = adv.classList.toggle("hidden");
  btn.textContent = (oculto ? "▸" : "▾") + " VALORES SIP / SBC";
});

document.getElementById("call-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("form-hint");
  const agentId = document.getElementById("call-agent").value || "default";
  const destSel = document.getElementById("call-dest").value;
  const elegido = destByValue(destSel); // destino del desplegable (catálogo o agente)
  const to = val("to");
  const destHost = val("dest-host");
  const destPort = parseInt(val("dest-port"), 10) || 0;

  const hold = parseInt(document.getElementById("hold").value, 10) || 0;

  // Hace falta un destino: el desplegable, DEST HOST (SBC) o la URI simple.
  if (!elegido && !to && !destHost) {
    hint.textContent = "Elige un DESTINATION, o indica DEST HOST / TARGET URI.";
    hint.className = "hint error";
    return;
  }

  // Si hay destino en el desplegable, es EL destino: no mandamos ni la URI ni el
  // DEST HOST manual. El bloque de valores SIP está plegado y podría llevar un
  // host viejo de otra prueba; que decidiera él a dónde va la llamada sería una
  // trampa difícil de ver.
  const destino = elegido
    ? destPayload(destSel)
    : { to, dest_host: destHost, dest_port: destPort };

  const payload = {
    agent_id: agentId,
    hold,
    ...destino,
    from_user: val("from-user"),
    from_domain: val("from-domain"),
    from_display: val("from-display"),
    to_user: val("to-user"),
    to_domain: val("to-domain"),
    pai_user: val("pai-user"),
    headers: parseHeaders(val("headers")),
  };

  try {
    const r = await placeCall(payload);
    hint.textContent = "Llamada lanzada (" + agentId + ") · id " + r.id;
    hint.className = "hint";
    refresh();
  } catch (e) {
    hint.textContent = "Error: " + e.message;
    hint.className = "hint error";
  }
});

// Alta de un agente nuevo.
document.getElementById("agent-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("agent-hint");
  const spec = {
    id: document.getElementById("ag-id").value.trim(),
    name: document.getElementById("ag-name").value.trim(),
    bind_ip: document.getElementById("ag-ip").value.trim(),
    sip_port: parseInt(document.getElementById("ag-port").value, 10) || 0,
    transport: document.getElementById("ag-transport").value,
    answer_code: parseInt(document.getElementById("ag-answer").value, 10) || 200,
  };
  if (!spec.id || !spec.bind_ip || !spec.sip_port) {
    hint.textContent = "Indica al menos id, bind IP y puerto.";
    hint.className = "hint error";
    return;
  }
  try {
    await addAgent(spec);
    hint.textContent = "Agente creado y arrancado: " + spec.id;
    hint.className = "hint";
    document.getElementById("ag-id").value = "";
    document.getElementById("ag-name").value = "";
    document.getElementById("ag-port").value = "";
    refresh();
  } catch (e) {
    hint.textContent = "Error: " + e.message;
    hint.className = "hint error";
  }
});

// ---- Catálogo de destinos ----

// Alta de un destino en el catálogo (se persiste en el config.json del servidor).
document.getElementById("dest-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("dest-hint");
  const payload = {
    id: val("dst-id"),
    name: val("dst-name"),
    host: val("dst-host"),
    port: parseInt(val("dst-port"), 10) || 0,
    transport: document.getElementById("dst-transport").value,
    to_domain: val("dst-todomain"),
  };
  if (!payload.id || !payload.host) {
    hint.textContent = "Indica al menos ID y HOST (el puerto por defecto es 5060).";
    hint.className = "hint error";
    return;
  }
  try {
    const res = await fetch("/api/destinations", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!res.ok) throw new Error(await res.text());
    hint.textContent = "Destino guardado: " + payload.id + " (ya disponible en PLACE CALL, SCENARIOS y LOAD TEST).";
    hint.className = "hint";
    ["dst-id", "dst-name", "dst-host", "dst-port", "dst-todomain"].forEach((id) => {
      document.getElementById(id).value = "";
    });
    refresh();
  } catch (e) {
    hint.textContent = "Error: " + e.message;
    hint.className = "hint error";
  }
});

// Alta de monitorización: el agente elegido empieza a sondear con OPTIONS un
// destino del catálogo.
document.getElementById("tr-agent").addEventListener("change", (ev) => {
  selectedTrunkAgent = ev.target.value;
});
document.getElementById("tr-dest").addEventListener("change", (ev) => {
  selectedTrunkDest = ev.target.value;
});
document.getElementById("trunk-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("trunk-hint");
  const payload = {
    agent_id: document.getElementById("tr-agent").value || "default",
    dest_id: document.getElementById("tr-dest").value,
  };
  if (!payload.dest_id) {
    hint.textContent = "Primero da de alta un destino en el catálogo.";
    hint.className = "hint error";
    return;
  }
  try {
    const res = await fetch("/api/trunks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!res.ok) throw new Error(await res.text());
    hint.textContent = "Monitorizando " + payload.dest_id + " desde " + payload.agent_id + ".";
    hint.className = "hint";
    refresh();
  } catch (e) {
    hint.textContent = "Error: " + e.message;
    hint.className = "hint error";
  }
});

// ---- Botonera de la llamada seleccionada ----
document.getElementById("btn-hangup").addEventListener("click", () => {
  if (!selectedCallId) return;
  hangup(selectedCallId).then(refresh);
});
document.getElementById("btn-hold").addEventListener("click", () => callAction("/api/call/hold"));
document.getElementById("btn-resume").addEventListener("click", () => callAction("/api/call/resume"));

// callAction lanza una acción sobre la llamada seleccionada (hold/resume) y refresca.
async function callAction(url) {
  if (!selectedCallId) {
    alert("Selecciona primero una llamada (clic en su fila).");
    return;
  }
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: selectedCallId }),
  });
  if (!res.ok) alert("Error: " + (await res.text()));
  refresh();
}

document.getElementById("btn-xfer").addEventListener("click", async () => {
  if (!selectedCallId) {
    alert("Selecciona primero una llamada (clic en su fila).");
    return;
  }
  const destino = val("xfer-to");
  if (!destino) {
    alert("Indica el destino del desvío (REFER-TO).");
    return;
  }
  const res = await fetch("/api/call/transfer", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: selectedCallId, refer_to: destino }),
  });
  if (!res.ok) alert("Error en desvío: " + (await res.text()));
  refresh();
});

// ---- Controles del visor de trazas ----
document.getElementById("trace-options").addEventListener("change", (ev) => {
  showOptions = ev.target.checked;
  selectedCall = ""; // recalcula la selección con el nuevo filtro
  refreshTrace();
});
document.getElementById("trace-clear").addEventListener("click", async () => {
  await fetch("/api/trace/clear", { method: "POST" });
  selectedCall = "";
  refreshTrace();
});

// ---- Escenarios: selección, recarga y ejecución ----
document.getElementById("sc-agent").addEventListener("change", (ev) => {
  selectedScenarioAgent = ev.target.value;
});
document.getElementById("sc-file").addEventListener("change", (ev) => {
  selectedScenarioFile = ev.target.value;
});
document.getElementById("sc-dest").addEventListener("change", (ev) => {
  selectedScenarioDest = ev.target.value;
  const uri = destUri(selectedScenarioDest);
  if (uri) document.getElementById("sc-target").value = uri;
});
document.getElementById("sc-reload").addEventListener("click", loadScenarios);
document.getElementById("scenario-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("scenario-hint");
  const agentId = document.getElementById("sc-agent").value || "default";
  const file = document.getElementById("sc-file").value;
  const destSel = document.getElementById("sc-dest").value;
  const elegido = destByValue(destSel);
  // Un destino del catálogo viaja por id (el servidor arma el Request-URI); si no
  // hay ninguno elegido, vale la URI escrita a mano.
  const target = elegido && elegido.kind === "dest" ? "" : val("sc-target");
  const destId = elegido && elegido.kind === "dest" ? elegido.id : "";
  if (!file) {
    hint.textContent = "No hay escenario seleccionado.";
    hint.className = "hint error";
    return;
  }
  try {
    const res = await fetch("/api/scenarios/run", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: agentId, file, target, dest_id: destId }),
    });
    if (!res.ok) throw new Error(await res.text());
    const r = await res.json();
    hint.textContent = "Escenario lanzado (" + agentId + ") · id " + r.id;
    hint.className = "hint";
    refresh();
  } catch (e) {
    hint.textContent = "Error: " + e.message;
    hint.className = "hint error";
  }
});

// ---- Audio (RTP) ----

// Estado del audio por agente (cacheado para repintar al cambiar de agente).
let audioCache = [];
function renderAudioStatus(datos) {
  audioCache = datos || [];
  const el = document.getElementById("audio-status");
  if (!el) return;
  const a = audioCache.find((x) => x.agent_id === selectedAudioAgent);
  if (a && a.samples > 0) {
    el.textContent = "♪ " + a.seconds.toFixed(1) + "s";
    el.className = "audio-status on";
  } else {
    el.textContent = "tono";
    el.className = "audio-status";
  }
}

document.getElementById("audio-agent").addEventListener("change", (ev) => {
  selectedAudioAgent = ev.target.value;
  renderAudioStatus(audioCache);
});

document.getElementById("audio-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const el = document.getElementById("audio-status");
  const agentId = document.getElementById("audio-agent").value || "default";
  const fileInput = document.getElementById("audio-file");
  if (!fileInput.files || fileInput.files.length === 0) {
    el.textContent = "elige un WAV";
    el.className = "audio-status err";
    return;
  }
  const fd = new FormData();
  fd.append("agent_id", agentId);
  fd.append("file", fileInput.files[0]);
  try {
    const res = await fetch("/api/media", { method: "POST", body: fd });
    if (!res.ok) throw new Error(await res.text());
    const r = await res.json();
    el.textContent = "♪ " + r.seconds.toFixed(1) + "s cargados";
    el.className = "audio-status on";
    fileInput.value = "";
    refresh();
  } catch (e) {
    el.textContent = "Error: " + e.message;
    el.className = "audio-status err";
  }
});

document.getElementById("audio-clear").addEventListener("click", async () => {
  const el = document.getElementById("audio-status");
  const agentId = document.getElementById("audio-agent").value || "default";
  try {
    const res = await fetch("/api/media/clear", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: agentId }),
    });
    if (!res.ok) throw new Error(await res.text());
    el.textContent = "tono";
    el.className = "audio-status";
    refresh();
  } catch (e) {
    el.textContent = "Error: " + e.message;
    el.className = "audio-status err";
  }
});

// Carga las IPv4 locales detectadas: rellena el datalist (sugerencias al teclear
// en BIND IP / DEST HOST) y, si el campo BIND IP del alta de agente está vacío, lo
// pre-rellena con la IP principal de la máquina. Así el usuario no la teclea.
async function loadNetInfo() {
  try {
    const info = await fetch("/api/netinfo").then((r) => r.json());
    const dl = document.getElementById("local-ips");
    if (dl) {
      dl.innerHTML = (info.ips || [])
        .map((x) => '<option value="' + esc(x.ip) + '">' + esc(x.label) + "</option>")
        .join("");
    }
    const agIp = document.getElementById("ag-ip");
    if (agIp && !agIp.value && info.local_ip) agIp.value = info.local_ip;
  } catch (e) {
    /* sin red detectable: la web sigue funcionando, solo sin sugerencias */
  }
}

// ---- Pruebas de carga (Fase 3) ----

// Última foto de stats de carga por agente (para repintar al cambiar de agente).
let loadCache = [];

// Pinta las métricas agregadas de la carga del agente seleccionado. Sin prueba en
// curso, el backend conserva la foto FINAL de la última (finished_at relleno): se
// muestra como FINISHED para que el operador no pierda los resultados.
function renderLoad(datos) {
  loadCache = datos || [];
  const el = document.getElementById("load-stats");
  if (!el) return;
  const s = loadCache.find((x) => x.agent_id === selectedLoadAgent);
  if (!s || (!s.running && !s.finished_at)) {
    el.innerHTML = '<span class="load-idle">IDLE — sin prueba de carga en curso</span>';
    return;
  }
  let state;
  if (!s.running) {
    state = '<span class="badge s-ended">FINISHED</span>';
  } else if (s.stopping) {
    state = '<span class="badge s-failed">STOPPING</span>';
  } else {
    state = '<span class="badge s-up">RUNNING</span>';
  }
  const chip = (label, value, cls) =>
    '<div class="load-chip' + (cls ? " " + cls : "") + '"><span class="lc-l">' +
    label + '</span><span class="lc-v">' + value + "</span></div>";

  // Duración total de la prueba terminada (foto final).
  const dur = !s.running && s.started_at && s.finished_at
    ? Math.max(0, Math.round((new Date(s.finished_at) - new Date(s.started_at)) / 1000)) + "s"
    : "";

  let html = '<div class="load-row">' + state +
    (dur ? chip("DURATION", dur) : "") +
    (s.scenario ? chip("SCENARIO", esc(s.scenario)) : "") +
    chip("TARGET", s.target) +
    chip("ACTIVE", s.active, s.active >= s.target ? "ok" : "") +
    chip("PENDING", s.pending) +
    chip("CPS", s.cps) +
    (s.call_secs > 0 ? chip("CALL DUR", s.call_secs + "s") : "") +
    "</div>" +
    '<div class="load-row">' +
    chip("LAUNCHED", s.launched) +
    chip("ESTABLISHED", s.established) +
    chip("FAILED", s.failed, s.failed > 0 ? "bad" : "") +
    (s.cancelled > 0 ? chip("CANCELLED", s.cancelled) : "") +
    chip("ENDED", s.ended) +
    "</div>";
  // Desglose de fallos por causa: código SIP de rechazo, timeout o error.
  if (s.failed_by && Object.keys(s.failed_by).length) {
    html += '<div class="load-row">' +
      Object.keys(s.failed_by).sort()
        .map((c) => chip("FAIL " + esc(c).toUpperCase(), s.failed_by[c], "bad"))
        .join("") +
      "</div>";
  }
  // PDD (INVITE -> 2xx) de las llamadas contestadas.
  if (s.pdd_max_ms > 0) {
    html += '<div class="load-row">' +
      chip("PDD MIN", s.pdd_min_ms + " ms") +
      chip("PDD AVG", s.pdd_avg_ms + " ms") +
      chip("PDD MAX", s.pdd_max_ms + " ms") +
      "</div>";
  }
  if (s.with_media) {
    html += '<div class="load-row">' +
      chip("RTP &uarr; pkts", s.tx_packets) +
      chip("RTP &darr; pkts", s.rx_packets) +
      chip("LOST", s.lost, s.lost > 0 ? "bad" : "") +
      "</div>";
  }
  el.innerHTML = html;
}

document.getElementById("load-agent").addEventListener("change", (ev) => {
  selectedLoadAgent = ev.target.value;
  renderLoad(loadCache);
});

// Al elegir un destino, volcamos su IP:puerto al TARGET URI (visible).
document.getElementById("load-dest").addEventListener("change", (ev) => {
  selectedLoadDest = ev.target.value;
  const uri = destUri(selectedLoadDest);
  if (uri) document.getElementById("load-to").value = uri;
});

document.getElementById("load-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("load-hint");
  const agentId = document.getElementById("load-agent").value || "default";
  const destSel = document.getElementById("load-dest").value;
  const elegido = destByValue(destSel);
  const to = val("load-to");

  if (!elegido && !to) {
    hint.textContent = "Elige un DESTINATION o indica un TARGET URI.";
    hint.className = "hint error";
    return;
  }
  // Igual que en PLACE CALL: el desplegable, si hay elección, es EL destino.
  const destino = elegido ? destPayload(destSel) : { to };

  const payload = {
    agent_id: agentId,
    concurrent: parseInt(val("load-n"), 10) || 0,
    cps: parseFloat(val("load-cps")) || 0,
    max_calls: parseInt(val("load-max"), 10) || 0,
    call_secs: parseFloat(val("load-dur")) || 0,
    with_media: document.getElementById("load-media").checked,
    scenario: document.getElementById("load-scenario").value || "",
    ...destino,
    // Números A/B: identidades FIJAS de todas las llamadas de la prueba (el SBC
    // enruta por el número B; el A viaja en el From).
    from_user: val("load-from-user"),
    to_user: val("load-to-user"),
  };

  try {
    const res = await fetch("/api/load/start", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!res.ok) throw new Error(await res.text());
    hint.textContent = "Carga arrancada (" + agentId + ").";
    hint.className = "hint";
    refresh();
  } catch (e) {
    hint.textContent = "Error: " + e.message;
    hint.className = "hint error";
  }
});

document.getElementById("load-stop").addEventListener("click", async () => {
  const agentId = document.getElementById("load-agent").value || "default";
  try {
    await fetch("/api/load/stop", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_id: agentId }),
    });
    document.getElementById("load-hint").textContent = "STOP enviado: colgando las llamadas…";
    document.getElementById("load-hint").className = "hint";
    refresh();
  } catch (e) {
    /* el siguiente refresco reflejará el estado */
  }
});

loadNetInfo();   // IPs locales para sugerir BIND IP / DEST HOST
loadScenarios(); // lista inicial de escenarios disponibles en disco

tickClock();
setInterval(tickClock, 1000);
refresh();
setInterval(refresh, POLL_MS);
