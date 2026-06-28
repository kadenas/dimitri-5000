# Arranque de la appweb de Dimitri-5000 (modo web) en Windows (PowerShell).
#
# Uso (en una terminal PowerShell, desde la carpeta del proyecto):
#   .\run-web.ps1
#
# Si PowerShell bloquea el script por la politica de ejecucion, lanzalo asi:
#   powershell -ExecutionPolicy Bypass -File .\run-web.ps1
#
# Luego abre el navegador en la URL que se imprime (por defecto http://127.0.0.1:8080).

# Nos situamos en la carpeta del proyecto (donde viven este script, go.mod y main.go),
# para que --scenarios-dir (examples/scenarios, relativo) se resuelva bien aunque
# lances el script desde otro directorio.
Set-Location -Path $PSScriptRoot

# === Configuracion (edita a tu gusto) =======================================
$BindIP  = "127.0.0.1"        # IP SIP local. Pon "" para autodetectar la IP de tu tarjeta de red
                              # (necesario para hablar con un SBC/Asterisk real).
$SipPort = "5070"             # Puerto SIP local (5070 evita chocar con softphones en 5060).
$WebAddr = "127.0.0.1:8080"   # Direccion de la web. Pon 0.0.0.0:8080 para abrirla desde otro equipo de la LAN.
# ============================================================================

Write-Host "Arrancando Dimitri-5000 (web) -> http://$WebAddr"
Write-Host "  SIP: bind-ip='$BindIP' (vacio = autodetectar)  puerto=$SipPort"

# Construimos los argumentos en un array: si BindIP esta vacio NO pasamos --bind-ip
# (el binario ya autodetecta por defecto). Asi evitamos pasar un argumento vacio,
# que en PowerShell podria desplazar el resto de parametros.
$callArgs = @("--mode", "web", "--sip-port", $SipPort, "--web", $WebAddr)
if ($BindIP -ne "") { $callArgs += @("--bind-ip", $BindIP) }

# 'go run .' compila y ejecuta en un solo paso (no deja un binario suelto en disco).
go run . @callArgs
