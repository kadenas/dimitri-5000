#!/usr/bin/env bash
#
# Arranque de la appweb de Dimitri-5000 (modo web) en Ubuntu/Linux.
#
# Uso:
#   chmod +x run-web.sh   # solo la primera vez (dar permiso de ejecucion)
#   ./run-web.sh
# o, sin dar permisos:
#   bash run-web.sh
#
# Luego abre el navegador en la URL que se imprime (por defecto http://127.0.0.1:8080).

set -euo pipefail

# Nos situamos en la carpeta del proyecto (donde viven este script, go.mod y main.go),
# para que --scenarios-dir (examples/scenarios, relativo) se resuelva bien aunque
# lances el script desde otro directorio.
cd "$(dirname "$0")"

# === Configuracion (edita a tu gusto) =======================================
BIND_IP="127.0.0.1"        # IP SIP local. Pon "" para autodetectar la IP de tu tarjeta de red
                           # (necesario para hablar con un SBC/Asterisk real).
SIP_PORT="5070"            # Puerto SIP local (5070 evita chocar con softphones en 5060).
WEB_ADDR="127.0.0.1:8080"  # Direccion de la web. Pon 0.0.0.0:8080 para abrirla desde otro equipo de la LAN.
# ============================================================================

echo "Arrancando Dimitri-5000 (web) -> http://${WEB_ADDR}"
echo "  SIP: bind-ip='${BIND_IP}' (vacio = autodetectar)  puerto=${SIP_PORT}"

# 'go run .' compila y ejecuta en un solo paso (no deja un binario suelto en disco).
# exec reemplaza el proceso del script por el de la app, asi Ctrl-C la para directamente.
exec go run . --mode web --bind-ip "${BIND_IP}" --sip-port "${SIP_PORT}" --web "${WEB_ADDR}"
