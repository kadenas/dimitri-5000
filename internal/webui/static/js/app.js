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
let selectedAgent = "default";

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

// Rellena el selector de agente de PLACE CALL conservando la elección previa.
function renderAgentSelector(datos) {
  const sel = document.getElementById("call-agent");
  const ids = (datos || []).map((a) => a.id);
  // Si el agente elegido ya no existe, caemos al primero disponible.
  if (!ids.includes(selectedAgent)) selectedAgent = ids[0] || "default";
  sel.innerHTML = (datos || []).map((a) => {
    const marca = a.state === "running" ? "" : " (stopped)";
    return '<option value="' + esc(a.id) + '">' + esc(a.id) + marca + "</option>";
  }).join("");
  sel.value = selectedAgent;
}

function renderCalls(datos) {
  const tbody = document.getElementById("calls");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="8">NO ACTIVE CALLS</td></tr>';
    return;
  }
  // Las más recientes arriba.
  const filas = datos.slice().reverse().map((c) => {
    const activa = c.state === "dialing" || c.state === "ringing" || c.state === "established";
    const accion = activa
      ? '<button class="btn-hangup" data-id="' + esc(c.id) + '">HANGUP</button>'
      : '<button class="btn-hangup" disabled>—</button>';
    return "<tr>" +
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

  // Enganchar los botones de colgar.
  tbody.querySelectorAll(".btn-hangup[data-id]").forEach((b) => {
    b.addEventListener("click", () => hangup(b.dataset.id).then(refresh));
  });
}

function renderTrunks(datos) {
  const tbody = document.getElementById("trunks");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="8">NO TRUNKS</td></tr>';
    return;
  }
  tbody.innerHTML = datos.map((t) =>
    "<tr>" +
    "<td>" + esc(t.name || t.id) + "</td>" +
    "<td>" + esc(t.host) + ":" + esc(t.port) + "</td>" +
    "<td>" + badge(t.status) + "</td>" +
    "<td>" + esc(codeText(t.last_code, t.last_reason)) + "</td>" +
    "<td>" + (t.last_rtt_ms > 0 ? t.last_rtt_ms + " ms" : "—") + "</td>" +
    "<td>" + (t.ok || 0) + "</td>" +
    "<td>" + (t.other || 0) + "</td>" +
    "<td>" + (t.timeout || 0) + "</td>" +
    "</tr>"
  ).join("");
}

// ---- Refresco periódico ----

async function refresh() {
  const conn = document.getElementById("conn");
  try {
    const [agents, calls, trunks] = await Promise.all([
      fetch("/api/agents").then((r) => r.json()),
      fetch("/api/calls").then((r) => r.json()),
      fetch("/api/status").then((r) => r.json()),
    ]);
    renderAgents(agents);
    renderAgentSelector(agents);
    renderCalls(calls);
    renderTrunks(trunks);
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

// Recordar el agente elegido al cambiarlo en el desplegable.
document.getElementById("call-agent").addEventListener("change", (ev) => {
  selectedAgent = ev.target.value;
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
  const to = val("to");
  const destHost = val("dest-host");
  const hold = parseInt(document.getElementById("hold").value, 10) || 0;

  // Hace falta un destino: o la URI simple o el DEST HOST (SBC).
  if (!to && !destHost) {
    hint.textContent = "Indica un destino: TARGET URI o DEST HOST.";
    hint.className = "hint error";
    return;
  }

  const payload = {
    agent_id: agentId,
    hold,
    to,
    dest_host: destHost,
    dest_port: parseInt(val("dest-port"), 10) || 0,
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

tickClock();
setInterval(tickClock, 1000);
refresh();
setInterval(refresh, POLL_MS);
