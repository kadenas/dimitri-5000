// app.js — DIMITRI//5000. Controla la web: lanza llamadas, las cuelga y refresca
// el estado de llamadas y troncales. No contiene lógica SIP: consume la API.

const POLL_MS = 1000;

// Etiquetas legibles por estado (llamadas, troncales y agentes comparten varias).
const LABEL = {
  dialing: "DIALING", ringing: "RINGING", established: "ESTABLISHED",
  ended: "ENDED", failed: "FAILED",
  up: "UP", degraded: "DEGRADED", down: "DOWN", unknown: "—",
  running: "RUNNING", stopped: "STOPPED",
};

// Recordamos qué agente está elegido en PLACE CALL para no perderlo al refrescar.
let selectedAgent = "default";   // agente que ORIGINA la llamada
let selectedToAgent = "";        // agente DESTINO ("" = destino manual / externo)
let selectedMsgAgent = "default"; // agente que ENVÍA el mensaje
let selectedMsgToAgent = "";      // agente DESTINO del mensaje
let selectedTrunkAgent = "default"; // agente que monitoriza el trunk a dar de alta
let agentsCache = [];            // última lista de agentes (para resolver destino)
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

// ---- Mensajería SIP (MESSAGE) ----

async function sendMessage(payload) {
  const res = await fetch("/api/message", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function renderMessages(datos) {
  const tbody = document.getElementById("messages");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="6">NO MESSAGES</td></tr>';
    return;
  }
  // Las más recientes arriba.
  tbody.innerHTML = datos.slice().reverse().map((m) => {
    const dir = m.dir === "in" ? '<span class="badge s-up">IN</span>'
                               : '<span class="badge s-dialing">OUT</span>';
    let code = "—";
    if (m.error) code = '<span class="s-failed">ERR</span>';
    else if (m.code) code = esc(codeText(m.code, m.reason));
    return "<tr>" +
      "<td>" + dir + "</td>" +
      "<td>" + esc(m.agent_id) + "</td>" +
      "<td>" + esc(m.peer) + "</td>" +
      "<td>" + esc(m.body) + "</td>" +
      "<td>" + code + "</td>" +
      "<td>" + hhmmss(m.timestamp) + "</td>" +
      "</tr>";
  }).join("");
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
    return "<tr>" +
      "<td>" + esc(a.id) + "</td>" +
      "<td>" + esc(a.name || a.id) + "</td>" +
      "<td>" + esc(a.bind_ip) + ":" + esc(a.sip_port) + "</td>" +
      "<td>" + esc(String(a.transport).toUpperCase()) + "</td>" +
      "<td>" + badge(a.state) + "</td>" +
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

  // --- Destino (TO AGENT): a qué agente se llama. Primera opción = manual/externo ---
  fillDestSelect("to-agent", selectedToAgent);

  // --- Selectores equivalentes del panel de mensajes ---
  const mo = document.getElementById("msg-agent");
  if (mo) {
    if (!ids.includes(selectedMsgAgent)) selectedMsgAgent = ids[0] || "default";
    mo.innerHTML = sel.innerHTML; // mismas opciones que el origen de llamadas
    mo.value = selectedMsgAgent;
  }
  fillDestSelect("msg-to-agent", selectedMsgToAgent);

  // Selector del alta de trunks (mismo origen de agentes).
  const tr = document.getElementById("tr-agent");
  if (tr) {
    if (!ids.includes(selectedTrunkAgent)) selectedTrunkAgent = ids[0] || "default";
    tr.innerHTML = sel.innerHTML;
    tr.value = selectedTrunkAgent;
  }
}

// fillDestSelect rellena un desplegable de agente DESTINO (con opción manual).
function fillDestSelect(selId, current) {
  const dst = document.getElementById(selId);
  if (!dst) return;
  const ids = agentsCache.map((a) => a.id);
  if (current && !ids.includes(current)) current = "";
  const opciones = ['<option value="">— manual / externo —</option>'].concat(
    agentsCache.map((a) => {
      const dirn = esc(a.bind_ip) + ":" + esc(a.sip_port);
      return '<option value="' + esc(a.id) + '">' + esc(a.id) + " · " + dirn + "</option>";
    })
  );
  dst.innerHTML = opciones.join("");
  dst.value = current;
}

function renderCalls(datos) {
  const tbody = document.getElementById("calls");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="8">NO ACTIVE CALLS</td></tr>';
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
      "<td>" + badge(c.state) + "</td>" +
      "<td>" + esc(codeText(c.last_code, c.last_reason)) + "</td>" +
      "<td>" + hhmmss(c.started_at) + "</td>" +
      "<td>" + hhmmss(c.answered_at) + "</td>" +
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

// Rellena el selector de llamadas de la traza (filtra OPTIONS salvo que se marque).
function renderTraceCalls(calls) {
  const sel = document.getElementById("trace-call");
  let lista = calls || [];
  if (!showOptions) {
    lista = lista.filter((c) => !c.first_line.startsWith("OPTIONS"));
  }
  const ids = lista.map((c) => c.call_id);
  if (selectedCall && !ids.includes(selectedCall)) selectedCall = "";
  if (!selectedCall && ids.length) selectedCall = ids[0];

  sel.innerHTML = lista.map((c) => {
    // Etiqueta legible: primer método + nº de mensajes.
    const metodo = c.first_line.split(" ")[0];
    return '<option value="' + esc(c.call_id) + '">' +
      esc(metodo) + " · " + c.count + " msg · " + esc(c.call_id.slice(0, 8)) +
      "</option>";
  }).join("") || '<option value="">— sin llamadas —</option>';
  sel.value = selectedCall;
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

// ---- Refresco periódico ----

async function refresh() {
  const conn = document.getElementById("conn");
  try {
    const [agents, calls, trunks, messages] = await Promise.all([
      fetch("/api/agents").then((r) => r.json()),
      fetch("/api/calls").then((r) => r.json()),
      fetch("/api/trunks").then((r) => r.json()),
      fetch("/api/messages").then((r) => r.json()),
    ]);
    renderAgents(agents);
    renderAgentSelector(agents);
    renderCalls(calls);
    renderTrunks(trunks);
    renderMessages(messages);
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

// Recordar el agente destino elegido al cambiarlo.
document.getElementById("to-agent").addEventListener("change", (ev) => {
  selectedToAgent = ev.target.value;
});

// Selectores del panel de mensajes.
document.getElementById("msg-agent").addEventListener("change", (ev) => {
  selectedMsgAgent = ev.target.value;
});
document.getElementById("msg-to-agent").addEventListener("change", (ev) => {
  selectedMsgToAgent = ev.target.value;
});

// Envío de un MESSAGE.
document.getElementById("msg-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("msg-hint");
  const agentId = document.getElementById("msg-agent").value || "default";
  const toAgentId = document.getElementById("msg-to-agent").value;
  const body = val("msg-body");
  const to = val("msg-to");

  if (!body) {
    hint.textContent = "Escribe el texto del mensaje.";
    hint.className = "hint error";
    return;
  }

  let destHost = "";
  let destPort = 0;
  const toAgent = agentsCache.find((a) => a.id === toAgentId);
  if (toAgent) {
    destHost = toAgent.bind_ip;
    destPort = toAgent.sip_port;
  }
  if (!toAgent && !to) {
    hint.textContent = "Elige un TO AGENT o indica una TO URI.";
    hint.className = "hint error";
    return;
  }

  const payload = {
    agent_id: agentId,
    to: toAgent ? "" : to,
    dest_host: destHost,
    dest_port: destPort,
    from_user: val("msg-from-user"),
    to_user: val("msg-to-user"),
    body,
  };

  try {
    await sendMessage(payload);
    hint.textContent = "Mensaje enviado.";
    hint.className = "hint";
    document.getElementById("msg-body").value = "";
    refresh();
  } catch (e) {
    hint.textContent = "Error: " + e.message;
    hint.className = "hint error";
  }
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
  const toAgentId = document.getElementById("to-agent").value;
  const to = val("to");
  let destHost = val("dest-host");
  let destPort = parseInt(val("dest-port"), 10) || 0;

  // Si se eligió un AGENTE DESTINO, su IP:puerto manda sobre lo manual.
  const toAgent = agentsCache.find((a) => a.id === toAgentId);
  if (toAgent) {
    destHost = toAgent.bind_ip;
    destPort = toAgent.sip_port;
  }

  const hold = parseInt(document.getElementById("hold").value, 10) || 0;

  // Hace falta un destino: agente destino, DEST HOST (SBC) o la URI simple.
  if (!toAgent && !to && !destHost) {
    hint.textContent = "Elige un TO AGENT, o indica DEST HOST / TARGET URI.";
    hint.className = "hint error";
    return;
  }

  const payload = {
    agent_id: agentId,
    hold,
    // Si hay agente destino o dest host, no mandamos 'to' para que prevalezca el destino.
    to: toAgent || destHost ? "" : to,
    dest_host: destHost,
    dest_port: destPort,
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

// Alta de un trunk remoto en el agente elegido.
document.getElementById("tr-agent").addEventListener("change", (ev) => {
  selectedTrunkAgent = ev.target.value;
});
document.getElementById("trunk-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("trunk-hint");
  const payload = {
    agent_id: document.getElementById("tr-agent").value || "default",
    id: val("tr-id"),
    name: val("tr-name"),
    host: val("tr-host"),
    port: parseInt(val("tr-port"), 10) || 0,
    transport: document.getElementById("tr-transport").value,
  };
  if (!payload.id || !payload.host || !payload.port) {
    hint.textContent = "Indica al menos id, host y puerto.";
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
    hint.textContent = "Trunk añadido: " + payload.id;
    hint.className = "hint";
    document.getElementById("tr-id").value = "";
    document.getElementById("tr-host").value = "";
    document.getElementById("tr-port").value = "";
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

// ---- Controles del ladder ----
document.getElementById("trace-call").addEventListener("change", (ev) => {
  selectedCall = ev.target.value;
  refreshTrace();
});
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

tickClock();
setInterval(tickClock, 1000);
refresh();
setInterval(refresh, POLL_MS);
