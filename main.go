package main

import (
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

	"github.com/AlecAivazis/survey/v2"
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
		console.LogInfo("Token no encontrado o inválido en Keyring. Por favor, ejecuta ./c2go-client --setup")
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

func runSetup() error {
	console.PrintBanner("C2GO - CONFIGURACIÓN INICIAL")

	var provider *dns.CloudflareProvider
	var token string

	existingToken, err := keyring.Get(config.ServiceName, config.TokenKey)
	hasToken := (err == nil && existingToken != "")

	// 1. DATOS DE ACCESO (Loop until valid token)
	console.PrintSection("DATOS DE ACCESO")
	for {
		if hasToken {
			console.LogInfo("Token de Cloudflare recuperado exitosamente desde el sistema.")
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
			console.LogError("Token inválido o sin permisos de lectura.")
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
		var selectedZones []string
		promptZones := &survey.MultiSelect{
			Message: "Selecciona los dominios a gestionar:",
			Options: zones,
			Default: defaultZones,
		}

		err = survey.AskOne(promptZones, &selectedZones, surveyIcons())
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

			var selectedRecords []string
			promptRecords := &survey.MultiSelect{
				Message: fmt.Sprintf("(%s) > Registros a monitorear (Espacio para seleccionar):", zoneName),
				Options: records,
				Default: defaultRecords,
			}

			err = survey.AskOne(promptRecords, &selectedRecords, surveyIcons())
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
				var newHost string
				promptNewHost := &survey.Input{
					Message: "Nombre del host (ej. vpn o dev):",
				}
				survey.AskOne(promptNewHost, &newHost, surveyIcons())
				newHost = strings.TrimSpace(newHost)

				if newHost != "" {
					var proxied bool
					promptProxied := &survey.Confirm{
						Message: "¿Activar proxy de Cloudflare (Proxied)?",
						Default: false,
					}
					survey.AskOne(promptProxied, &proxied, surveyIcons())

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

	intervalPrompt := &survey.Input{
		Message: "Intervalo de chequeo (segundos):",
		Default: fmt.Sprintf("%d", cfg.UpdateInterval),
	}
	var intervalStr string
	survey.AskOne(intervalPrompt, &intervalStr, surveyIcons())
	if intervalStr != "" {
		if val, err := strconv.Atoi(intervalStr); err == nil && val >= 60 {
			cfg.UpdateInterval = val
		}
	}

	var historyEnabled bool
	historyPrompt := &survey.Confirm{
		Message: "¿Activar historial de IPs?",
		Default: cfg.HistoryEnabled,
	}
	survey.AskOne(historyPrompt, &historyEnabled, surveyIcons())
	cfg.HistoryEnabled = historyEnabled

	// 4. SISTEMA
	console.PrintSection("SISTEMA")

	fmt.Printf("> %sGuardando Token en Keyring... %s", console.ColorCyan, console.ColorReset)
	// Only set the token if it's new (not empty string meaning keep current)
	if token != "" {
		if err := keyring.Set(config.ServiceName, config.TokenKey, token); err != nil {
			console.Fail()
			return fmt.Errorf("error saving token to keyring: %w", err)
		}
	}
	console.OK()

	fmt.Printf("> %sGuardando archivo de configuración... %s", console.ColorCyan, console.ColorReset)
	if err := config.Save(cfg); err != nil {
		console.Fail()
		return fmt.Errorf("error guardando el archivo de configuración: %w", err)
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

func surveyIcons() survey.AskOpt {
	return survey.WithIcons(func(icons *survey.IconSet) {
		icons.SelectFocus.Text = ">"
		icons.SelectFocus.Format = "cyan"
		icons.MarkedOption.Text = "[X]"
		icons.MarkedOption.Format = "green"
		icons.UnmarkedOption.Text = "[ ]"
		icons.Question.Text = ">"
		icons.Question.Format = "cyan"
	})
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
