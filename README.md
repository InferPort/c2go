# c2go

`c2go` is a secure, lightweight Cloudflare Dynamic DNS (DDNS) client written in Go. It keeps your domain A/AAAA records updated with your dynamic public IP address automatically.

## Key Features

- **OS Keyring Security**: Uses your operating system's native secure keyring (`go-keyring`) to store your Cloudflare API token. The token is never saved in plaintext on disk.
- **Interactive Setup Wizard**: Run `./c2go-client --setup` for a terminal-based onboarding flow to select zones, manage records, and configure update intervals.
- **Multi-Domain & Multi-Record Support**: Select multiple Cloudflare zones and specify exactly which A or AAAA records to keep updated.
- **On-the-Fly DNS Record Creation**: Create new A or AAAA records directly within the setup wizard using your current public IP.
- **Smart IP Checking with Fallbacks**: Retrieves your public IP via multiple services (`ipify.org`, `ifconfig.me`, `icanhazip.com`) for maximum availability.
- **Concurrent DNS Updates**: DNS records across zones are updated concurrently for minimal update latency.
- **Resilient Error Handling**: If updating a specific record fails, the program logs the failure and continues processing all other records.
- **IP Change History**: Optional local change history (up to 50 entries) stored in a readable JSON file.
- **ANSI Terminal UI**: Clean logging with color-coded output (cyan for info, green for success, red for errors).

## Project Structure

```
c2go/
├── config/       # Configuration parsing and OS keyring integration
├── console/      # ANSI terminal utilities and logging
├── dns/          # Cloudflare API integration (raw HTTP, no SDK)
├── history/      # JSON IP history manager
├── ipcheck/      # Public IP checker with HTTP fallbacks
├── main.go       # Entry point (setup wizard + service loop)
├── go.mod        # Go module definition
└── .gitignore
```

## Requirements

- **Go**: 1.20 or newer.
- **OS Keyring**: A running OS-level credential storage service.
  - **macOS**: Keychain (native).
  - **Linux**: `dbus`, `gnome-keyring`, or `ksecretservice`.
  - **Windows**: Windows Credential Manager.

## Build

```bash
git clone https://github.com/InferPort/c2go.git
cd c2go
go build -o c2go-client .
```

## Usage

### 1. Setup Mode (`--setup`)

Configure the application for the first time:

```bash
./c2go-client --setup
```

The wizard guides you through:
1. **API Token**: Enter your Cloudflare API Token (input is masked). If a token exists in your keyring, press Enter to keep it.
2. **Domain Selection**: Select one or more domains to manage.
3. **Record Configuration**: Select which A/AAAA records to update, or create new ones on the fly.
4. **Parameters**: Set the update interval (minimum 60s, default 300s) and toggle IP history.
5. **Saving**: The token is stored in your OS keyring; non-sensitive config is saved to a JSON file in your user config directory.

### 2. Service Mode

Run without flags to start the monitoring service:

```bash
./c2go-client
```

The service will periodically check your public IP and update Cloudflare records when a change is detected. Stop gracefully with Ctrl+C.

## Configuration

Files are stored in the OS user config directory:

- **macOS**: `~/Library/Application Support/c2go/config.json`
- **Linux**: `~/.config/c2go/config.json`
- **Windows**: `%APPDATA%\c2go\config.json`

The JSON config file contains only non-sensitive settings:

```json
{
  "managed_zones": [
    {
      "domain": "example.com",
      "records": ["@", "vpn"]
    }
  ],
  "history_enabled": true,
  "update_interval": 300
}
```

The Cloudflare API token is stored securely in your OS keyring (service name: `com.inferport.c2go`).

## License

MIT
