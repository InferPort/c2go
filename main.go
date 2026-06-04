package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"c2go/config"
	"c2go/console"
	"c2go/dns"
	"c2go/history"
	"c2go/ipcheck"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const createNewOption = "[+] Crear nuevo registro A"
const goBackOption = "[ < Volver a selección de dominios ]"

func main() {
	setupFlag := flag.Bool("setup", false, "Run the interactive setup configuration")
	flag.Parse()

	// 1. Setup Mode
	if *setupFlag {
		if err := runSetup(); err != nil {
			console.LogError("Setup failed: %v", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// 2. Service Mode Integrity Checks
	if !config.ConfigExists() {
		console.LogInfo("No se encontró configuración. Por favor, ejecuta ./c2go-client --setup")
		os.Exit(1)
	}

	cfg, err := config.Load()
	if err != nil {
		console.LogError("Configuration error: %v. Run with --setup to reconfigure.", err)
		os.Exit(1)
	}

	if cfg.CloudflareToken == "" {
		console.LogInfo("Token no encontrado o inválido en el keyring. Por favor, ejecuta ./c2go-client --setup")
		os.Exit(1)
	}

	console.LogInfo("Iniciando servicio c2go...")

	provider, err := dns.NewCloudflareProvider(cfg.CloudflareToken)
	if err != nil {
		console.LogError("Failed to initialize DNS provider: %v", err)
		os.Exit(1)
	}

	histPath, err := config.GetHistoryPath()
	if err != nil {
		console.LogError("Failed to determine history path: %v", err)
		os.Exit(1)
	}
	histManager := history.NewManager(histPath)

	// Setup context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Listen for OS signals
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigs
		console.LogInfo("Deteniendo servicio c2go...")
		cancel()
	}()

	// Start the worker loop
	runWorker(ctx, cfg, provider, histManager)
}

// promptInput reads a single line of input with a default option.
func promptInput(message string, defaultValue string) (string, error) {
	reader := bufio.NewReader(os.Stdin)
	if defaultValue != "" {
		fmt.Printf("%s%s [%s]: %s", console.ColorCyan, message, defaultValue, console.ColorReset)
	} else {
		fmt.Printf("%s%s: %s", console.ColorCyan, message, console.ColorReset)
	}
	input, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		return defaultValue, nil
	}
	return input, nil
}

// promptConfirm asks a yes/no question.
func promptConfirm(message string, defaultValue bool) (bool, error) {
	options := "y/N"
	if defaultValue {
		options = "Y/n"
	}
	for {
		res, err := promptInput(fmt.Sprintf("%s (%s)", message, options), "")
		if err != nil {
			return false, err
		}
		res = strings.ToLower(strings.TrimSpace(res))
		if res == "" {
			return defaultValue, nil
		}
		if res == "y" || res == "yes" || res == "s" || res == "si" {
			return true, nil
		}
		if res == "n" || res == "no" {
			return false, nil
		}
		fmt.Println("Respuesta no válida. Por favor escribe Y o N.")
	}
}

// promptMultiSelect shows numbered options and lets the user choose multiple values separated by commas.
func promptMultiSelect(message string, options []string, defaultOptions []string) ([]string, error) {
	fmt.Printf("\n%s%s%s\n", console.ColorCyan, message, console.ColorReset)
	
	defaultIndices := []int{}
	for i, opt := range options {
		isDefault := false
		for _, d := range defaultOptions {
			if d == opt {
				isDefault = true
				defaultIndices = append(defaultIndices, i+1)
				break
			}
		}
		marker := "[ ]"
		if isDefault {
			marker = "[X]"
		}
		fmt.Printf("  %s%2d)%s %s %s\n", console.ColorCyan, i+1, console.ColorReset, marker, opt)
	}

	var defaultStr string
	if len(defaultIndices) > 0 {
		var idxStrs []string
		for _, idx := range defaultIndices {
			idxStrs = append(idxStrs, strconv.Itoa(idx))
		}
		defaultStr = strings.Join(idxStrs, ",")
		fmt.Printf("Selecciona los números separados por coma (ej: 1,3) [Default: %s]: ", defaultStr)
	} else {
		fmt.Print("Selecciona los números separados por coma (ej: 1,3): ")
	}

	reader := bufio.NewReader(os.Stdin)
	input, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	input = strings.TrimSpace(input)
	if input == "" {
		input = defaultStr
	}

	if input == "" {
		return nil, nil
	}

	var selected []string
	parts := strings.Split(input, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		idx, err := strconv.Atoi(p)
		if err != nil || idx < 1 || idx > len(options) {
			fmt.Printf("%sNúmero de opción inválido ignorado: %s%s\n", console.ColorRed, p, console.ColorReset)
			continue
		}
		selected = append(selected, options[idx-1])
	}
	return selected, nil
}

func runSetup() error {
	console.PrintBanner("C2GO - CONFIGURACIÓN INICIAL")

	var provider *dns.CloudflareProvider
	var token string

	var existingToken string
	if storedToken, err := keyring.Get(config.ServiceName, config.TokenKey); err == nil && storedToken != "" {
		existingToken = storedToken
	} else if config.ConfigExists() {
		if cfg, err := config.Load(); err == nil && cfg != nil {
			existingToken = cfg.CloudflareToken
		}
	}
	hasToken := existingToken != ""

	// 1. DATOS DE ACCESO (Loop until valid token)
	console.PrintSection("DATOS DE ACCESO")
	for {
		if hasToken {
			console.LogInfo("Token de Cloudflare recuperado exitosamente desde el keyring del sistema.")
			console.PrintPrompt("Cloudflare API Token (Enter para mantener el actual)")
		} else {
			console.PrintPrompt("Cloudflare API Token")
		}

		bytePassword, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println() // Print newline after hidden input
		if err != nil {
			return fmt.Errorf("error reading password: %w", err)
		}

		tokenStr := strings.TrimSpace(string(bytePassword))
		if tokenStr == "" && hasToken {
			token = existingToken
		} else {
			token = tokenStr
		}

		if token == "" {
			console.Fail()
			fmt.Println("El API Token es requerido.")
			continue
		}

		provider, err = dns.NewCloudflareProvider(token)
		if err != nil {
			console.LogError("Token inválido o error de inicialización: %v", err)
			continue
		}

		// Validate by listing zones
		_, err = provider.ListZones(context.Background())
		if err != nil {
			console.LogError("Token inválido o sin permisos de lectura en Cloudflare.")
			continue
		}

		break
	}

	// Read existing config early for defaults
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		cfg = &config.Config{
			HistoryEnabled: true,
			UpdateInterval: 300,
		}
	}

	// 2. DOMINIOS Y REGISTROS
	console.PrintSection("DOMINIOS Y REGISTROS")
	zones, err := provider.ListZones(context.Background())
	if err != nil {
		return fmt.Errorf("error listing zones: %w", err)
	}
	if len(zones) == 0 {
		return fmt.Errorf("no se encontraron dominios en tu cuenta de Cloudflare")
	}

	var defaultZones []string
	for _, z := range cfg.ManagedZones {
		defaultZones = append(defaultZones, z.Domain)
	}

DomainLoop:
	for {
		selectedZones, err := promptMultiSelect("Selecciona los dominios a gestionar:", zones, defaultZones)
		if err != nil {
			return err
		}

		if len(selectedZones) == 0 {
			console.LogError("Debes seleccionar al menos un dominio.")
			continue
		}

		var pendingZones []config.ManagedZone

		for i, zoneName := range selectedZones {
			fmt.Printf("\nConfigurando registros para: %s [%d/%d]\n", zoneName, i+1, len(selectedZones))

			records, err := provider.ListARecords(context.Background(), zoneName)
			if err != nil {
				return fmt.Errorf("error listing records for %s: %w", zoneName, err)
			}

			// Add creation option and go back option at the end
			records = append(records, createNewOption, goBackOption)

			var defaultRecords []string
			for _, d := range cfg.ManagedZones {
				if d.Domain == zoneName {
					defaultRecords = d.Records
					break
				}
			}

			selectedRecords, err := promptMultiSelect(fmt.Sprintf("(%s) > Registros a monitorear:", zoneName), records, defaultRecords)
			if err != nil {
				return err
			}

			if len(selectedRecords) == 0 {
				console.LogError("Debes seleccionar al menos un registro.")
				continue DomainLoop
			}

			var finalRecords []string
			var createNew bool
			for _, r := range selectedRecords {
				if r == goBackOption {
					continue DomainLoop
				}
				if r == createNewOption {
					createNew = true
				} else {
					finalRecords = append(finalRecords, r)
				}
			}

			if createNew {
				newHost, err := promptInput("Nombre del host (ej. vpn o dev)", "")
				if err != nil {
					return err
				}
				newHost = strings.TrimSpace(newHost)

				if newHost != "" {
					proxied, err := promptConfirm("¿Activar proxy de Cloudflare (Proxied)?", false)
					if err != nil {
						return err
					}

					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

					console.LogInfo("Detectando IP pública actual para crear el registro...")
					ip, err := ipcheck.GetPublicIP(ctx)
					if err != nil {
						cancel()
						return fmt.Errorf("no se pudo detectar IP pública: %w", err)
					}

					err = provider.CreateARecord(ctx, zoneName, newHost, ip, proxied)
					if err != nil {
						if strings.Contains(err.Error(), "tipo incompatible") {
							console.LogError("El registro ya existe con un tipo incompatible.")
						} else {
							console.LogError("Fallo al crear el registro: %v", err)
						}
					} else {
						fullRecordName := zoneName
						if newHost != "@" && newHost != zoneName {
							fullRecordName = fmt.Sprintf("%s.%s", newHost, zoneName)
						}
						console.LogSuccess("Registro '%s' creado exitosamente.", fullRecordName)
						finalRecords = append(finalRecords, newHost)
					}
					cancel()
				}
			}

			if len(finalRecords) == 0 {
				console.LogError("No se seleccionó ni creó ningún registro válido para %s.", zoneName)
				continue DomainLoop
			}

			pendingZones = append(pendingZones, config.ManagedZone{Domain: zoneName, Records: finalRecords})
		}

		cfg.ManagedZones = pendingZones
		break DomainLoop
	}

	// 3. PARÁMETROS
	console.PrintSection("PARÁMETROS")

	intervalStr, err := promptInput("Intervalo de chequeo (segundos)", fmt.Sprintf("%d", cfg.UpdateInterval))
	if err != nil {
		return err
	}
	if intervalStr != "" {
		if val, err := strconv.Atoi(intervalStr); err == nil && val >= 60 {
			cfg.UpdateInterval = val
		}
	}

	historyEnabled, err := promptConfirm("¿Activar historial de IPs?", cfg.HistoryEnabled)
	if err != nil {
		return err
	}
	cfg.HistoryEnabled = historyEnabled

	// 4. SISTEMA
	console.PrintSection("SISTEMA")

	cfg.CloudflareToken = token

	fmt.Printf("> %sGuardando Token en Keyring... %s", console.ColorCyan, console.ColorReset)
	if err := config.Save(cfg); err != nil {
		console.Fail()
		return fmt.Errorf("error guardando la configuración: %w", err)
	}
	console.OK()

	configPath, _ := config.GetConfigPath()
	fmt.Printf("[ OK ] Configuración guardada en: %s\n", configPath)

	var totalRecords int
	for _, mz := range cfg.ManagedZones {
		totalRecords += len(mz.Records)
	}

	histStr := "Desactivado"
	if cfg.HistoryEnabled {
		histStr = "Activado"
	}

	fmt.Println("\n[ RESUMEN ]")
	fmt.Printf("> Dominios gestionados: %d\n", len(cfg.ManagedZones))
	fmt.Printf("> Total de registros: %d\n", totalRecords)
	fmt.Printf("> Intervalo: %ds\n", cfg.UpdateInterval)
	fmt.Printf("> Historial: %s\n", histStr)

	fmt.Println("==================================================")

	return nil
}

func runWorker(ctx context.Context, cfg *config.Config, provider dns.Provider, histManager *history.Manager) {
	var lastIP string

	if cfg.HistoryEnabled {
		lastIP = histManager.GetLastIP()
		if lastIP != "" {
			console.LogInfo("Cargada última IP conocida desde el historial: %s", lastIP)
		}
	}

	// Execute immediately on startup
	lastIP, err := performUpdate(ctx, cfg, provider, histManager, lastIP)
	if err != nil && !errors.Is(err, context.Canceled) {
		console.LogError("Error durante el chequeo inicial: %v", err)
	}

	ticker := time.NewTicker(time.Duration(cfg.UpdateInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			newIP, err := performUpdate(ctx, cfg, provider, histManager, lastIP)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					console.LogError("Error en el ciclo de actualización: %v", err)
				}
			} else {
				lastIP = newIP
			}
		}
	}
}

func performUpdate(ctx context.Context, cfg *config.Config, provider dns.Provider, histManager *history.Manager, lastIP string) (string, error) {
	ip, err := ipcheck.GetPublicIP(ctx)
	if err != nil {
		if errors.Is(err, ipcheck.ErrNoInternet) {
			console.LogWait("Sin conexión a internet. Esperando al próximo ciclo.")
			return lastIP, nil
		}
		return lastIP, err
	}

	if ip == lastIP {
		console.LogInfo("La IP no ha cambiado (%s). Omitiendo actualización DNS.", ip)
		return lastIP, nil
	}

	console.LogInfo("Cambio de IP detectado: %s -> %s", lastIP, ip)

	err = provider.UpdateDomains(ctx, ip, cfg.ManagedZones)
	if err != nil {
		return lastIP, err
	}

	console.LogSuccess("Operaciones DNS completadas.")

	if cfg.HistoryEnabled {
		if err := histManager.AddEntry(ip); err != nil {
			console.LogError("Fallo al guardar IP en historial: %v", err)
		}
	}

	return ip, nil
}
