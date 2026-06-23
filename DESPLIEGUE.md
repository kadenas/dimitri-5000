# DESPLIEGUE — dimitri-5000

dimitri-5000 es una aplicación Go que compila a un **único binario autocontenido**
(la interfaz web va embebida con `go:embed`). No requiere instalar nada más en la
máquina de destino: se copia el binario y se ejecuta.

## Requisitos de compilación

- Go 1.23 o superior instalado en la máquina de desarrollo.
- Conexión a internet la primera vez (para `go mod download`).

## Construcción

Desde la raíz del proyecto:

```bash
go mod download    # solo la primera vez
go build -o dist/  ./...
```

### Compilación optimizada (binario más pequeño)

`-ldflags "-s -w"` elimina la tabla de símbolos y la info de depuración:

```bash
go build -ldflags "-s -w" -o dist/dimitri-5000 .
```

### Compilación cruzada (un SO desde otro)

Go compila para otro sistema sin emuladores, cambiando `GOOS`/`GOARCH`:

```bash
# Binario para Windows (64 bits)
GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o dist/dimitri-5000.exe .

# Binario para Linux/Ubuntu (64 bits)
GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o dist/dimitri-5000 .
```

En PowerShell (Windows), fija las variables así antes de compilar:

```powershell
$env:GOOS="linux"; $env:GOARCH="amd64"; go build -ldflags "-s -w" -o dist/dimitri-5000 .
```

## Despliegue

### Windows

1. Copiar `dimitri-5000.exe` a la máquina destino.
2. (Opcional) Copiar y ajustar un `config.json` junto al binario.
3. Ejecutar desde consola:
   ```
   dimitri-5000.exe --config config.json --web 127.0.0.1:8080
   ```
4. Abrir `http://127.0.0.1:8080`.

> Nota: el tráfico SIP usa la red. Si Windows Defender Firewall pregunta, permite
> el acceso en la red correspondiente.

### Ubuntu / Linux

1. Copiar el binario `dimitri-5000`.
2. Dar permiso de ejecución: `chmod +x dimitri-5000`.
3. Ejecutar:
   ```
   ./dimitri-5000 --config config.json --web 127.0.0.1:8080
   ```
4. Abrir `http://127.0.0.1:8080`.

## Configuración necesaria

- `--config` (opcional): ruta a un fichero JSON. Si se omite, se usan valores por defecto.
- `--web`: dirección de la interfaz web (por defecto `127.0.0.1:8080`, solo local).
- `--bind-ip`: IP local de origen del tráfico SIP (Via/Contact). Vacío = la del config.

Para exponer SIP hacia la red, la `bind_ip` debe ser una IP real de la máquina, no
`127.0.0.1`.

## App Android

No aplica: dimitri-5000 es una herramienta de escritorio/servidor, no una app Android.
