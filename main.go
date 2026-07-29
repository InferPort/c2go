package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"c2go/config"
	"c2go/console"
	"c2go/dns"
	"c2go/history"
	"c2go/i18n"
	"c2go/ipcheck"
	"c2go/update"

	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

var createNewOption = "[+] Create new A record"
var goBackOption = "[ < Go back to domain selection ]"

func main() {
	setupFlag := flag.Bool("setup", false, "Run the interactive setup configuration")
	configFlag := flag.String("config", "", "Path to custom configuration file (e.g. /etc/c2go/config.json)")
	updateFlag := flag.Bool("update", false, "Check for and install the latest version")
	installServiceFlag := flag.Bool("install-service", false, "Install c2go as a systemd service (Linux only)")
	flag.Parse()

	if *configFlag != "" {
		config.ConfigPathOverride = *configFlag
	}

	// Initialize localization
	lang := ""
	if config.ConfigExists() {
		if cfg, err := config.Load(); err == nil && cfg != nil {
			lang = cfg.Language
		}
	}
	i18n.Init(lang)
	createNewOption = i18n.T("create_new_record_option")
	goBackOption = i18n.T("go_back_option")

	update.CleanupOldBinary()

	// 1. Update Mode
	if *updateFlag {
		runUpdate()
		os.Exit(0)
	}

	// 2. Setup Mode
	if *setupFlag {
		if err := runSetup(); err != nil {
			console.LogError("%s", i18n.T("setup_failed", err))
			os.Exit(1)
		}
		os.Exit(0)
	}

	// 2.5. Service Installation Mode
	if *installServiceFlag {
		if err := installSystemdService(); err != nil {
			console.LogError("%s", err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}

	// 3. Service Mode Integrity Checks
	if !config.ConfigExists() {
		path, _ := config.GetConfigPath()
		console.LogInfo("%s", i18n.T("config_not_found", path))
		os.Exit(1)
	}

	// Recomendar correr como servicio si no está ejecutándose como uno
	if !isRunningAsService() {
		console.LogInfo("%s", i18n.T("not_running_service"))
		console.LogInfo("%s", i18n.T("run_as_service_recommendation"))
		console.LogInfo("%s", i18n.T("run_as_service_command"))
	}

	cfg, err := config.Load()
	if err != nil {
		console.LogError("%s", i18n.T("config_error", err))
		os.Exit(1)
	}

	if cfg.CloudflareToken == "" {
		console.LogInfo("%s", i18n.T("token_not_found"))
		os.Exit(1)
	}

	console.LogInfo("%s", i18n.T("starting_service"))

	provider, err := dns.NewCloudflareProvider(cfg.CloudflareToken)
	if err != nil {
		console.LogError("%s", i18n.T("failed_init_dns", err))
		os.Exit(1)
	}

	histPath, err := config.GetHistoryPath()
	if err != nil {
		console.LogError("%s", i18n.T("failed_history_path", err))
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
		console.LogInfo("%s", i18n.T("stopping_service"))
		cancel()
	}()

	// Start background update checker
	startUpdateChecker(ctx, cfg)

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
		fmt.Println(i18n.T("invalid_response"))
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
		fmt.Printf("%s", i18n.T("select_numbers_default", defaultStr))
	} else {
		fmt.Printf("%s", i18n.T("select_numbers"))
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
			fmt.Printf("%s%s%s\n", console.ColorRed, i18n.T("input_invalid_option", p), console.ColorReset)
			continue
		}
		selected = append(selected, options[idx-1])
	}
	return selected, nil
}

func runSetup() error {
	// 0. SELECCIONAR IDIOMA / SELECT LANGUAGE
	console.PrintSection("SELECT LANGUAGE / SELECCIONAR IDIOMA")
	fmt.Println("Language / Idioma:\n  1) English\n  2) Español")
	langOption, err := promptInput("Select option / Seleccione opción", "1")
	if err != nil {
		return err
	}
	selectedLang := "en"
	if langOption == "2" {
		selectedLang = "es"
	}
	i18n.Init(selectedLang)
	createNewOption = i18n.T("create_new_record_option")
	goBackOption = i18n.T("go_back_option")

	console.PrintBanner(i18n.T("setup_title"))

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
	console.PrintSection(i18n.T("access_data"))
	for {
		if hasToken {
			console.LogInfo("%s", i18n.T("token_keyring_success"))
			console.PrintPrompt(i18n.T("cf_token_prompt_keep"))
		} else {
			console.PrintPrompt(i18n.T("cf_token_prompt"))
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
			fmt.Println(i18n.T("token_required"))
			continue
		}

		provider, err = dns.NewCloudflareProvider(token)
		if err != nil {
			console.LogError("%s", i18n.T("failed_init_dns", err))
			continue
		}

		// Validate by listing zones
		_, err = provider.ListZones(context.Background())
		if err != nil {
			console.LogError("%s", i18n.T("token_not_found"))
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
	cfg.Language = selectedLang

	// 2. DOMINIOS Y REGISTROS
	console.PrintSection(i18n.T("domains_records"))
	zones, err := provider.ListZones(context.Background())
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("error_list_zones"), err)
	}
	if len(zones) == 0 {
		return fmt.Errorf("%s", i18n.T("no_zones_found"))
	}

	var defaultZones []string
	for _, z := range cfg.ManagedZones {
		defaultZones = append(defaultZones, z.Domain)
	}

DomainLoop:
	for {
		selectedZones, err := promptMultiSelect(i18n.T("select_domains"), zones, defaultZones)
		if err != nil {
			return err
		}

		if len(selectedZones) == 0 {
			console.LogError("%s", i18n.T("must_select_domain"))
			continue
		}

		var pendingZones []config.ManagedZone

		for i, zoneName := range selectedZones {
			fmt.Printf("\n%s\n", i18n.T("configuring_records", zoneName, i+1, len(selectedZones)))

			records, err := provider.ListARecords(context.Background(), zoneName)
			if err != nil {
				return fmt.Errorf("%s: %w", i18n.T("error_list_records", zoneName), err)
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

			selectedRecords, err := promptMultiSelect(i18n.T("records_monitor", zoneName), records, defaultRecords)
			if err != nil {
				return err
			}

			if len(selectedRecords) == 0 {
				console.LogError("%s", i18n.T("must_select_record"))
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
				newHost, err := promptInput(i18n.T("host_name_prompt"), "")
				if err != nil {
					return err
				}
				newHost = strings.TrimSpace(newHost)

				if newHost != "" {
					proxied, err := promptConfirm(i18n.T("cf_proxy_prompt"), false)
					if err != nil {
						return err
					}

					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

					console.LogInfo("%s", i18n.T("detecting_public_ip"))
					ip, err := ipcheck.GetPublicIP(ctx)
					if err != nil {
						cancel()
						return fmt.Errorf("%s: %w", i18n.T("failed_public_ip"), err)
					}

					err = provider.CreateARecord(ctx, zoneName, newHost, ip, proxied)
					if err != nil {
						if strings.Contains(err.Error(), "tipo incompatible") {
							console.LogError("%s", i18n.T("record_exists_incompatible"))
						} else {
							console.LogError("%s", i18n.T("failed_create_record", err))
						}
					} else {
						fullRecordName := zoneName
						if newHost != "@" && newHost != zoneName {
							fullRecordName = fmt.Sprintf("%s.%s", newHost, zoneName)
						}
						console.LogSuccess("%s", i18n.T("record_created_success", fullRecordName))
						finalRecords = append(finalRecords, newHost)
					}
					cancel()
				}
			}

			if len(finalRecords) == 0 {
				console.LogError("%s", i18n.T("no_valid_record_selected", zoneName))
				continue DomainLoop
			}

			pendingZones = append(pendingZones, config.ManagedZone{Domain: zoneName, Records: finalRecords})
		}

		cfg.ManagedZones = pendingZones
		break DomainLoop
	}

	// 3. PARÁMETROS
	console.PrintSection(i18n.T("parameters"))

	intervalStr, err := promptInput(i18n.T("check_interval"), fmt.Sprintf("%d", cfg.UpdateInterval))
	if err != nil {
		return err
	}
	if intervalStr != "" {
		if val, err := strconv.Atoi(intervalStr); err == nil && val >= 60 {
			cfg.UpdateInterval = val
		}
	}

	historyEnabled, err := promptConfirm(i18n.T("enable_history"), cfg.HistoryEnabled)
	if err != nil {
		return err
	}
	cfg.HistoryEnabled = historyEnabled

	defaultUpdateCheck := true
	if cfg.UpdateCheck != nil {
		defaultUpdateCheck = *cfg.UpdateCheck
	}
	updateCheck, err := promptConfirm(i18n.T("check_updates_prompt"), defaultUpdateCheck)
	if err != nil {
		return err
	}
	cfg.UpdateCheck = &updateCheck

	defaultAutoUpdate := false
	if cfg.AutoUpdate != nil {
		defaultAutoUpdate = *cfg.AutoUpdate
	}
	autoUpdate, err := promptConfirm(i18n.T("auto_update_prompt"), defaultAutoUpdate)
	if err != nil {
		return err
	}
	cfg.AutoUpdate = &autoUpdate

	// 4. SISTEMA
	console.PrintSection(i18n.T("system"))

	cfg.CloudflareToken = token

	fmt.Printf("> %s%s %s", console.ColorCyan, i18n.T("saving_token"), console.ColorReset)
	if err := config.Save(cfg); err != nil {
		console.Fail()
		return fmt.Errorf("%s: %w", i18n.T("error_saving_config"), err)
	}
	console.OK()

	configPath, _ := config.GetConfigPath()
	fmt.Printf("%s\n", i18n.T("config_saved", configPath))

	var totalRecords int
	for _, mz := range cfg.ManagedZones {
		totalRecords += len(mz.Records)
	}

	histStr := i18n.T("history_disabled")
	if cfg.HistoryEnabled {
		histStr = i18n.T("history_enabled")
	}

	updateCheckStr := i18n.T("history_disabled")
	if cfg.UpdateCheck != nil && *cfg.UpdateCheck {
		updateCheckStr = i18n.T("history_enabled")
	}

	autoUpdateStr := i18n.T("history_disabled")
	if cfg.AutoUpdate != nil && *cfg.AutoUpdate {
		autoUpdateStr = i18n.T("history_enabled")
	}

	fmt.Println("\n[ " + i18n.T("summary") + " ]")
	fmt.Printf("> %s\n", i18n.T("managed_domains", len(cfg.ManagedZones)))
	fmt.Printf("> %s\n", i18n.T("total_records", totalRecords))
	fmt.Printf("> %s\n", i18n.T("interval", cfg.UpdateInterval))
	fmt.Printf("> %s\n", i18n.T("history", histStr))
	fmt.Printf("> %s\n", i18n.T("search_updates_summary", updateCheckStr))
	fmt.Printf("> %s\n", i18n.T("auto_update_summary", autoUpdateStr))

	fmt.Println("==================================================")

	return nil
}

func runWorker(ctx context.Context, cfg *config.Config, provider dns.Provider, histManager *history.Manager) {
	var lastIP string

	if cfg.HistoryEnabled {
		lastIP = histManager.GetLastIP()
		if lastIP != "" {
			console.LogInfo("%s", i18n.T("loaded_last_ip", lastIP))
		}
	}

	// Execute immediately on startup
	lastIP, err := performUpdate(ctx, cfg, provider, histManager, lastIP)
	if err != nil && !errors.Is(err, context.Canceled) {
		console.LogError("%s", i18n.T("initial_check_error", err))
	}

	ticker := time.NewTicker(time.Duration(cfg.UpdateInterval) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if newCfg, changed, err := config.ReloadIfChanged(); err == nil && changed {
				console.LogInfo("%s", i18n.T("config_reloaded"))
				cfg = newCfg
				ticker.Reset(time.Duration(cfg.UpdateInterval) * time.Second)
			}

			newIP, err := performUpdate(ctx, cfg, provider, histManager, lastIP)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					console.LogError("%s", i18n.T("update_cycle_error", err))
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
			console.LogWait("%s", i18n.T("no_internet_waiting"))
			return lastIP, nil
		}
		return lastIP, err
	}

	if ip == lastIP {
		console.LogInfo("%s", i18n.T("ip_not_changed", ip))
		return lastIP, nil
	}

	console.LogInfo("%s", i18n.T("ip_change_detected", lastIP, ip))

	err = provider.UpdateDomains(ctx, ip, cfg.ManagedZones)
	if err != nil {
		return lastIP, err
	}

	console.LogSuccess("%s", i18n.T("dns_ops_completed"))

	if cfg.HistoryEnabled {
		if err := histManager.AddEntry(ip); err != nil {
			console.LogError("%s", i18n.T("history_save_failed", err))
		}
	}

	return ip, nil
}

func runUpdate() {
	ctx := context.Background()

	console.LogInfo("%s", i18n.T("checking_updates"))
	result, err := update.CheckForUpdate(ctx)
	if err != nil {
		console.LogError("%s", i18n.T("update_check_error", err))
		os.Exit(1)
	}

	if !result.HasUpdate {
		console.LogInfo("%s", i18n.T("latest_version_already", update.Version))
		return
	}

	fmt.Printf("\n%s\n", i18n.T("new_version_available", result.CurrentVersion, result.LatestVersion))
	if result.ReleaseNotes != "" {
		fmt.Printf("%s\n%s\n", i18n.T("release_notes"), result.ReleaseNotes)
	}

	confirmed, err := promptConfirm(i18n.T("download_install_prompt"), true)
	if err != nil || !confirmed {
		console.LogInfo("%s", i18n.T("update_cancelled"))
		return
	}

	tmpDir, err := os.MkdirTemp("", "c2go-update")
	if err != nil {
		console.LogError("%s", i18n.T("tmp_dir_error", err))
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	console.LogInfo("%s", i18n.T("downloading_asset", result.AssetName))
	binPath, err := result.DownloadAndVerify(ctx, tmpDir)
	if err != nil {
		console.LogError("%s", i18n.T("download_error", err))
		os.Exit(1)
	}

	console.LogInfo("%s", i18n.T("installing_update"))
	if err := update.ApplyUpdate(binPath); err != nil {
		console.LogError("%s", i18n.T("install_error", err))
		os.Exit(1)
	}

	console.LogSuccess("%s", i18n.T("updated_success", result.LatestVersion))
}

func startUpdateChecker(ctx context.Context, cfg *config.Config) {
	if cfg.UpdateCheck == nil || !*cfg.UpdateCheck {
		return
	}

	go func() {
		ticker := time.NewTicker(2 * time.Minute)
		defer ticker.Stop()

		checkAndHandleUpdate(ctx, cfg)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				checkAndHandleUpdate(ctx, cfg)
			}
		}
	}()
}

func checkAndHandleUpdate(ctx context.Context, cfg *config.Config) {
	result, err := update.CheckForUpdate(ctx)
	if err != nil {
		return
	}

	if !result.HasUpdate {
		return
	}

	if cfg.AutoUpdate != nil && *cfg.AutoUpdate {
		console.LogInfo("%s", i18n.T("new_version_detected", result.LatestVersion))

		tmpDir, err := os.MkdirTemp("", "c2go-update")
		if err != nil {
			console.LogError("%s", i18n.T("tmp_dir_error", err))
			return
		}
		defer os.RemoveAll(tmpDir)

		binPath, err := result.DownloadAndVerify(ctx, tmpDir)
		if err != nil {
			console.LogError("%s", i18n.T("download_error", err))
			return
		}

		console.LogInfo("%s", i18n.T("installing_update"))
		if err := update.ApplyUpdate(binPath); err != nil {
			console.LogError("%s", i18n.T("install_error", err))
			return
		}

		console.LogSuccess("%s", i18n.T("updated_success_restart", result.LatestVersion))
	} else {
		console.LogInfo("%s", i18n.T("new_version_available_manual", result.LatestVersion))
	}
}

func isRunningAsService() bool {
	return os.Getenv("INVOCATION_ID") != ""
}

func installSystemdService() error {
	if runtime.GOOS != "linux" {
		return errors.New(i18n.T("install_service_linux_only"))
	}

	// Validar que se corre como root
	if os.Getuid() != 0 {
		return errors.New(i18n.T("install_service_root_req"))
	}

	// 1. Obtener ruta del binario actual
	execPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_determine_exec"), err)
	}

	targetBinPath := "/usr/local/bin/c2go"
	console.LogInfo("%s", i18n.T("copying_exec", targetBinPath))

	// 2. Copiar binario a /usr/local/bin/c2go
	srcFile, err := os.Open(execPath)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_open_src"), err)
	}
	defer srcFile.Close()

	destFile, err := os.OpenFile(targetBinPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_create_dest"), err)
	}
	defer destFile.Close()

	if _, err := io.Copy(destFile, srcFile); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_copy_bin"), err)
	}
	destFile.Close() // Cerrar explícitamente antes de cambiar permisos

	// Forzar permisos de ejecución (chmod +x)
	if err := os.Chmod(targetBinPath, 0755); err != nil {
		return fmt.Errorf("failed to set execution permissions on %s: %w", targetBinPath, err)
	}

	// 3. Determinar el usuario real que invocó sudo
	user := os.Getenv("SUDO_USER")
	if user == "" {
		user = "root" // Fallback si fue ejecutado directamente por root sin sudo
	}

	console.LogInfo("%s", i18n.T("configuring_service_user", user))

	// 4. Crear archivo de servicio en /etc/systemd/system/c2go.service
	serviceContent := fmt.Sprintf(`[Unit]
Description=c2go Dynamic DNS Client
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
ExecStart=%s
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
`, user, targetBinPath)

	servicePath := "/etc/systemd/system/c2go.service"
	console.LogInfo("%s", i18n.T("creating_service_file", servicePath))
	if err := os.WriteFile(servicePath, []byte(serviceContent), 0644); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_create_service"), err)
	}

	// 5. Recargar daemon, habilitar e iniciar el servicio
	console.LogInfo("%s", i18n.T("reloading_systemd"))
	if err := exec.Command("systemctl", "daemon-reload").Run(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_daemon_reload"), err)
	}

	console.LogInfo("%s", i18n.T("enabling_service"))
	if err := exec.Command("systemctl", "enable", "c2go").Run(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_enable_service"), err)
	}

	console.LogInfo("%s", i18n.T("starting_c2go_service"))
	if err := exec.Command("systemctl", "start", "c2go").Run(); err != nil {
		return fmt.Errorf("%s: %w", i18n.T("failed_start_service"), err)
	}

	console.LogSuccess("%s", i18n.T("service_installed_success"))
	return nil
}

