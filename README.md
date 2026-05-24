# c2go

`c2go` is a secure, lightweight, and highly interactive Cloudflare Dynamic DNS (DDNS) client written in Go. It keeps your domain records updated with your home or office's dynamic public IP address automatically. 

Featuring a modern and interactive Terminal User Interface (TUI) setup wizard, it safely stores your Cloudflare API token in your operating system's native secure keyring (such as Keychain on macOS or Secret Service on Linux) instead of storing it in plaintext configuration files.

---

## Key Features

- 🔐 **OS Keyring Security**: Uses your operating system's secure keyring (`go-keyring`) to store your Cloudflare API token (under service name `com.nicolas.c2go`). No plaintext tokens are left in configuration files or code repository commits!
- 🎛️ **Interactive Setup Wizard**: Run `./c2go-client --setup` for a clean, classic terminal-based onboarding flow (`survey/v2`) to select zones, manage records, and configure update intervals.
- 🌐 **Multi-Domain & Multi-Record Support**: Select multiple Cloudflare zones (domains) and specify exactly which `A` or `AAAA` records to keep updated.
- ➕ **On-the-Fly DNS Record Creation**: Create new `A` or `AAAA` records directly within the setup wizard, dynamically applying your current public IP with custom Cloudflare proxy (`proxied`) settings.
- 🔄 **Smart IP Checking & Fallbacks**: Retrieves your current public IP via multiple robust services (such as `ipify.org`, `ifconfig.me`, and `icanhazip.com`) to ensure maximum availability.
- ⚡ **Continuous Monitoring Service**: Runs as a lightweight daemon checking for IP changes at customizable intervals, updating records only when a change is detected.
- 📜 **Resilient Error Logging**: If updating a specific DNS record fails (e.g. because it was deleted on Cloudflare), the program logs the failure and continues processing all other records without stopping.
- 📅 **IP Change History**: Optional local change history tracking (up to 50 entries) stored in a readable JSON file.
- 🖥️ **Classic CLI Aesthetics**: Clean, distraction-free logging style utilizing classic ANSI color highlighting (Cyan for prompts, Green for success, Red for failure) with zero emojis.

---

## Project Structure

```
c2go/
├── config/       # Configuration parsing and OS user config directory paths
├── console/      # ANSI color terminal utilities and standardized logging format
├── dns/          # Cloudflare API integration, zone listing, and record management
├── history/      # JSON IP history manager
├── ipcheck/      # Public IP checker with multiple HTTP fallbacks
├── main.go       # Core entry point (coordinates --setup and Service modes)
├── go.mod        # Go module definition
└── .gitignore    # Safe defaults to prevent accidental binary/credential commits
```

---

## Installation & Requirements

### Requirements
- **Go**: 1.20 or newer.
- **Operating System Keyring**: A running OS-level credential storage service.
  - **macOS**: Keychain Services (available natively).
  - **Linux**: `dbus`, `gnome-keyring`, or `ksecretservice`.
  - **Windows**: Windows Credential Manager.

### Building from Source

1. Clone this repository:
   ```bash
   git clone https://github.com/InferPort/c2go.git
   cd c2go
   ```

2. Build the binary:
   ```bash
   go build -o c2go-client .
   ```

---

## Configuration & Usage

`c2go` operates in two distinct, mutually exclusive modes: **Setup Mode** (for interactive configuration) and **Service Mode** (for continuous background monitoring).

### 1. Setup Mode (`--setup`)

To configure the application for the first time, or to modify your managed domains, run:
```bash
./c2go-client --setup
```

#### The Configuration Wizard Flow:
1. **API Token Input**: Enter your Cloudflare API Token. The input is masked for security. If a token is already present in your OS Keyring, you can simply press **Enter** to keep it.
2. **Domain Selection**: The wizard lists all domains (zones) in your Cloudflare account. Move using the **arrow keys** and select one or more domains using the **Spacebar**, then press **Enter**.
3. **Record Configuration (Sequential)**: For each domain selected, the wizard lists existing `A` and `AAAA` records. You can:
   - Select multiple records to keep updated.
   - Choose `[+] Crear nuevo registro A` to dynamically register a new hostname under that domain, specifying whether it should be proxied by Cloudflare.
   - Choose `[ < Volver a selección de dominios ]` to return to the previous step.
4. **Parameters**: Enter the update check interval in seconds (minimum 60s, default 300s) and choose whether to enable the IP change history.
5. **System Storage**: The token is saved to your Keyring, and your preferences are saved to a standard YAML configuration file.

At the end of a successful setup, the tool displays the path of the saved configuration and a summary:
```text
==================================================
  C2GO - CONFIGURACIÓN INICIAL
==================================================

[DATOS DE ACCESO]
Token de Cloudflare recuperado exitosamente desde el sistema.
> Cloudflare API Token (Enter para mantener el actual): **************

[DOMINIOS Y REGISTROS]
Selecciona los dominios a gestionar: [X] example.com  [X] mydomain.org

Configurando registros para: example.com [1/2]
> (example.com) > Registros a monitorear (Espacio para seleccionar): [X] @, [X] vpn, [ ] dev, [+] Crear nuevo registro A

Configurando registros para: mydomain.org [2/2]
> (mydomain.org) > Registros a monitorear (Espacio para seleccionar): [X] @

[PARÁMETROS]
> Intervalo de chequeo (segundos): 300
> ¿Activar historial de IPs? Sí

[SISTEMA]
> Guardando Token en Keyring... [ OK ]
> Guardando archivo de configuración... [ OK ]
[ OK ] Configuración guardada en: /Users/username/Library/Application Support/c2go/config.yaml

[ RESUMEN ]
> Dominios gestionados: 2
> Total de registros: 3
> Intervalo: 300s
> Historial: Activado
==================================================
```

---

### 2. Service Mode

Once configured, run the application without any flags to start the monitoring service:
```bash
./c2go-client
```

The service will:
1. Verify the integrity of the config files and keyring token.
2. Query your current public IP address.
3. Compare it with the last registered IP address.
4. Update all designated Cloudflare records to point to the new IP if a change is detected.
5. Loop indefinitely according to your specified `update_interval`.

You can gracefully stop the service at any time by sending a termination signal (e.g., `Ctrl+C`).

```text
2026-05-24 11:45:10 [ INFO ] Iniciando servicio c2go...
2026-05-24 11:45:10 [ INFO ] Cargada última IP conocida desde el historial: 198.51.100.42
2026-05-24 11:45:11 [ INFO ] Cambio de IP detectado: 198.51.100.42 -> 203.0.113.85
2026-05-24 11:45:12 [ OK ] Actualizado example.com apuntando a 203.0.113.85
2026-05-24 11:45:12 [ OK ] Actualizado vpn.example.com apuntando a 203.0.113.85
2026-05-24 11:45:13 [ OK ] Actualizado mydomain.org apuntando a 203.0.113.85
2026-05-24 11:45:13 [ OK ] Operaciones DNS completadas.
```

---

## Configuration Paths & Files

`c2go` relies on the OS user standard directory (`os.UserConfigDir()`) to ensure configurations are sandbox-compliant and isolated for your system user:

- **macOS**: `~/Library/Application Support/c2go/`
- **Linux**: `~/.config/c2go/`
- **Windows**: `%APPDATA%\c2go\`

### 1. `config.yaml`
```yaml
managed_zones:
  - domain: example.com
    records:
      - "@"
      - vpn
  - domain: mydomain.org
    records:
      - "@"
history_enabled: true
update_interval: 300
```

### 2. `history.json` (Optional)
Tracks past public IP shifts to minimize unnecessary DNS queries on daemon boot.
```json
[
  {
    "timestamp": "2026-05-24T11:45:13-05:00",
    "ip": "203.0.113.85"
  }
]
```

---

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
