// app.js — DIMITRI//5000. Controla la web: lanza llamadas, las cuelga y refresca
// el estado de llamadas y troncales. No contiene lógica SIP: consume la API.

const POLL_MS = 1000;

// Etiquetas legibles por estado (llamadas y troncales comparten varias).
const LABEL = {
  dialing: "DIALING", ringing: "RINGING", established: "ESTABLISHED",
  ended: "ENDED", failed: "FAILED",
  up: "UP", degraded: "DEGRADED", down: "DOWN", unknown: "—",
};

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

async function placeCall(to, hold) {
  const res = await fetch("/api/call", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ to, hold }),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

async function hangup(id) {
  await fetch("/api/call/hangup", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
}

// ---- Pintado de tablas ----

function renderCalls(datos) {
  const tbody = document.getElementById("calls");
  if (!datos || datos.length === 0) {
    tbody.innerHTML = '<tr class="empty"><td colspan="7">NO ACTIVE CALLS</td></tr>';
    return;
  }
  // Las más recientes arriba.
  const filas = datos.slice().reverse().map((c) => {
    const activa = c.state === "dialing" || c.state === "ringing" || c.state === "established";
    const accion = activa
      ? '<button class="btn-hangup" data-id="' + esc(c.id) + '">HANGUP</button>'
      : '<button class="btn-hangup" disabled>—</button>';
    return "<tr>" +
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
    const [calls, trunks] = await Promise.all([
      fetch("/api/calls").then((r) => r.json()),
      fetch("/api/status").then((r) => r.json()),
    ]);
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

document.getElementById("call-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const hint = document.getElementById("form-hint");
  const to = document.getElementById("to").value.trim();
  const hold = parseInt(document.getElementById("hold").value, 10) || 0;
  if (!to) {
    hint.textContent = "Indica un destino (TARGET URI).";
    hint.className = "hint error";
    return;
  }
  try {
    await placeCall(to, hold);
    hint.textContent = "Llamada lanzada → " + to;
    hint.className = "hint";
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
