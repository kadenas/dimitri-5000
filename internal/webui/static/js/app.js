// app.js — pide el estado del faro cada pocos segundos y pinta la tabla.
// No contiene lógica SIP: solo consume la API /api/status que expone el motor.

const POLL_MS = 2000; // cada cuánto refrescamos la vista

// Traducción de estado interno -> etiqueta legible en español.
const ETIQUETA = {
  up: "Activa",
  degraded: "Degradada",
  down: "Caída",
  unknown: "Sin datos",
};

// Pinta una latencia en ms, o un guion si no la hay.
function latencia(ms) {
  return ms > 0 ? ms + " ms" : "—";
}

// Pinta un código SIP (0 = no hubo respuesta).
function codigo(code, reason) {
  if (!code) return "—";
  return reason ? code + " " + reason : String(code);
}

// Escapa texto para no inyectar HTML al construir las filas.
function esc(s) {
  return String(s == null ? "" : s).replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c])
  );
}

// Construye una fila de la tabla a partir del estado de una troncal.
function fila(t) {
  const tr = document.createElement("tr");
  tr.innerHTML =
    '<td class="name">' + esc(t.name || t.id) + "</td>" +
    "<td>" + esc(t.host) + ":" + esc(t.port) + "</td>" +
    '<td><span class="badge ' + esc(t.status) + '"><span class="dot"></span>' +
      (ETIQUETA[t.status] || esc(t.status)) + "</span></td>" +
    "<td>" + esc(codigo(t.last_code, t.last_reason)) + "</td>" +
    "<td>" + latencia(t.last_rtt_ms) + "</td>" +
    "<td>" + (t.ok || 0) + "</td>" +
    "<td>" + (t.other || 0) + "</td>" +
    "<td>" + (t.timeout || 0) + "</td>";
  return tr;
}

async function refrescar() {
  const updated = document.getElementById("updated");
  try {
    const res = await fetch("/api/status");
    const datos = await res.json();
    const tbody = document.getElementById("rows");
    tbody.innerHTML = "";

    if (!datos || datos.length === 0) {
      tbody.innerHTML =
        '<tr class="empty"><td colspan="8">Sin troncales configuradas.</td></tr>';
    } else {
      datos.forEach((t) => tbody.appendChild(fila(t)));
    }
    updated.textContent =
      "Actualizado " + new Date().toLocaleTimeString("es-ES");
  } catch (e) {
    updated.textContent = "Sin conexión con el motor";
  }
}

refrescar();
setInterval(refrescar, POLL_MS);
