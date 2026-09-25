package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"c2go/config"
	"c2go/console"
	"c2go/dns"
	"c2go/history"
	"c2go/i18n"
	"c2go/ipcheck"

	"github.com/zalando/go-keyring"
)

type Server struct {
	Host       string
	Port       int
	httpServer *http.Server
}

func NewServer(host string, port int) *Server {
	if host == "" {
		if envHost := os.Getenv("CONFIG_HOST"); envHost != "" {
			host = envHost
		} else {
			host = "127.0.0.1"
		}
	}
	if port == 0 {
		if envPort := os.Getenv("CONFIG_PORT"); envPort != "" {
			if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
				port = p
			}
		}
		if port == 0 {
			port = 8080
		}
	}

	return &Server{
		Host: host,
		Port: port,
	}
}

type verifyTokenReq struct {
	Token string `json:"token"`
}

type createRecordReq struct {
	Token   string `json:"token"`
	Name    string `json:"name"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

type recordItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Proxied bool   `json:"proxied"`
}

type zoneItem struct {
	ZoneID  string       `json:"zone_id"`
	Domain  string       `json:"domain"`
	Records []recordItem `json:"records"`
}

type saveConfigReq struct {
	Token               string     `json:"token"`
	Language            string     `json:"language"`
	UpdateInterval      int        `json:"update_interval"`
	PreferredInterfaces []string   `json:"preferred_interfaces"`
	HistoryEnabled      bool       `json:"history_enabled"`
	CheckUpdates        bool       `json:"check_updates"`
	AutoUpdate          bool       `json:"auto_update"`
	ManagedZones        []zoneItem `json:"managed_zones"`
}

func (s *Server) Start(ctx context.Context) error {
	staticHandler, err := GetFileSystem()
	if err != nil {
		return fmt.Errorf("failed to load embedded web assets: %w", err)
	}

	mux := http.NewServeMux()

	// API Routes
	mux.HandleFunc("/api/initial-data", s.handleInitialData)
	mux.HandleFunc("/api/verify-token", s.handleVerifyToken)
	mux.HandleFunc("/api/zones/", s.handleZones)
	mux.HandleFunc("/api/save-config", s.handleSaveConfig)

	// Static UI
	mux.Handle("/", staticHandler)

	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	console.LogInfo("Web Setup UI active at: http://%s", addr)
	console.LogInfo("Press Ctrl+C in terminal to abort setup.")

	errChan := make(chan error, 1)
	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errChan <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	case err := <-errChan:
		return err
	}
}

func (s *Server) handleInitialData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]interface{}{
		"language":        "en",
		"update_interval": 300,
		"history_enabled": true,
		"check_updates":   true,
		"auto_update":     false,
		"token":           "",
		"public_ip":       "",
	}

	isConfigured := false
	if cfg, err := config.Load(); err == nil && cfg != nil {
		if len(cfg.ManagedZones) > 0 {
			isConfigured = true
			response["managed_zones"] = cfg.ManagedZones
		}
		if cfg.Language != "" {
			response["language"] = cfg.Language
		}
		if cfg.UpdateInterval > 0 {
			response["update_interval"] = cfg.UpdateInterval
		}
		response["history_enabled"] = cfg.HistoryEnabled
		if cfg.UpdateCheck != nil {
			response["check_updates"] = *cfg.UpdateCheck
		}
		if cfg.AutoUpdate != nil {
			response["auto_update"] = *cfg.AutoUpdate
		}
		if cfg.CloudflareToken != "" {
			response["token"] = cfg.CloudflareToken
		}
		if len(cfg.PreferredInterfaces) > 0 {
			response["preferred_interfaces"] = cfg.PreferredInterfaces
		}
	} else if token, err := keyring.Get(config.ServiceName, config.TokenKey); err == nil && token != "" {
		response["token"] = token
	}
	response["is_configured"] = isConfigured

	if ip, err := ipcheck.GetPublicIP(r.Context()); err == nil {
		response["public_ip"] = ip
	}

	if ifaces, err := ipcheck.GetNetworkInterfaces(); err == nil {
		response["network_interfaces"] = ifaces
	} else {
		response["network_interfaces"] = []ipcheck.InterfaceInfo{}
	}

	if histPath, err := config.GetHistoryPath(); err == nil {
		histManager := history.NewManager(histPath)
		if entries, err := histManager.GetEntries(); err == nil && len(entries) > 0 {
			response["history_entries"] = entries
		} else {
			response["history_entries"] = []history.Entry{}
		}
	} else {
		response["history_entries"] = []history.Entry{}
	}

	svcInfo := detectServiceStatus()
	response["os"] = runtime.GOOS
	response["is_service_installed"] = svcInfo.IsInstalled
	response["is_service_running"] = svcInfo.IsRunning
	response["start_cmd"] = svcInfo.SuggestedCmd
	response["service_status"] = svcInfo

	writeJSON(w, http.StatusOK, response)
}

type ServiceStatusInfo struct {
	Type         string `json:"type"`          // "systemd", "launchd", "windows", "standalone"
	IsInstalled  bool   `json:"is_installed"`  // true if service file/config exists
	IsRunning    bool   `json:"is_running"`    // true if service or daemon is currently running
	PID          int    `json:"pid,omitempty"` // background PID if detected
	StatusLabel  string `json:"status_label"`  // user-friendly status badge
	SuggestedCmd string `json:"suggested_cmd"` // executable command to start/restart
	ActionNote   string `json:"action_note"`   // instructional note
}

func findOtherC2goProcess(currentPID int) int {
	out, err := exec.Command("pgrep", "-x", "c2go").Output()
	if err != nil {
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if pid, err := strconv.Atoi(line); err == nil && pid != currentPID {
			return pid
		}
	}
	return 0
}

func detectServiceStatus() ServiceStatusInfo {
	currentPID := os.Getpid()
	info := ServiceStatusInfo{
		Type:         "standalone",
		SuggestedCmd: "./c2go",
		StatusLabel:  "Standalone CLI (Not running as service)",
		ActionNote:   "Run ./c2go in your terminal to start real-time dynamic DNS synchronization.",
	}

	switch runtime.GOOS {
	case "linux":
		info.Type = "systemd"
		systemdFiles := []string{
			"/etc/systemd/system/c2go.service",
			"/usr/lib/systemd/system/c2go.service",
		}
		if home, err := os.UserHomeDir(); err == nil {
			systemdFiles = append(systemdFiles, filepath.Join(home, ".config/systemd/user/c2go.service"))
		}
		for _, f := range systemdFiles {
			if _, err := os.Stat(f); err == nil {
				info.IsInstalled = true
				break
			}
		}

		out, err := exec.Command("systemctl", "is-active", "c2go").Output()
		if err == nil && strings.TrimSpace(string(out)) == "active" {
			info.IsRunning = true
			info.StatusLabel = "Service Active (systemd)"
			info.SuggestedCmd = "sudo systemctl restart c2go"
			info.ActionNote = "c2go is running as a systemd background service. Restart the service to apply changes."
			return info
		}

		if otherPID := findOtherC2goProcess(currentPID); otherPID > 0 {
			info.IsRunning = true
			info.PID = otherPID
			info.StatusLabel = fmt.Sprintf("Background Process Active (PID %d)", otherPID)
			info.SuggestedCmd = fmt.Sprintf("kill -HUP %d || ./c2go", otherPID)
			info.ActionNote = fmt.Sprintf("A background c2go instance is running with PID %d.", otherPID)
			return info
		}

		if info.IsInstalled {
			info.StatusLabel = "Service Installed (systemd stopped)"
			info.SuggestedCmd = "sudo systemctl start c2go"
			info.ActionNote = "c2go is installed as a systemd service. Start it to begin background syncing."
			return info
		}

		info.StatusLabel = "Standalone CLI (No background service)"
		info.SuggestedCmd = "./c2go"
		info.ActionNote = "Run ./c2go to start syncing, or install as a systemd background service with: sudo ./c2go --install-service"

	case "darwin":
		info.Type = "launchd"
		home, _ := os.UserHomeDir()
		plistPaths := []string{
			filepath.Join(home, "Library/LaunchAgents/com.inferport.c2go.plist"),
			filepath.Join(home, "Library/LaunchAgents/c2go.plist"),
			"/Library/LaunchAgents/com.inferport.c2go.plist",
			"/Library/LaunchAgents/c2go.plist",
			"/Library/LaunchDaemons/com.inferport.c2go.plist",
		}
		for _, p := range plistPaths {
			if _, err := os.Stat(p); err == nil {
				info.IsInstalled = true
				break
			}
		}

		cmd := exec.Command("launchctl", "list", "com.inferport.c2go")
		if out, err := cmd.CombinedOutput(); err == nil && !strings.Contains(string(out), "Could not find") {
			info.IsRunning = true
			info.StatusLabel = "Service Active (LaunchAgent)"
			info.SuggestedCmd = fmt.Sprintf("launchctl kickstart -k gui/%d/com.inferport.c2go", os.Getuid())
			info.ActionNote = "c2go is running as a background macOS LaunchAgent. Restart the service to apply changes."
			return info
		}

		if otherPID := findOtherC2goProcess(currentPID); otherPID > 0 {
			info.IsRunning = true
			info.PID = otherPID
			info.StatusLabel = fmt.Sprintf("Background Process Active (PID %d)", otherPID)
			info.SuggestedCmd = fmt.Sprintf("kill -HUP %d || ./c2go", otherPID)
			info.ActionNote = fmt.Sprintf("A background c2go instance is running with PID %d.", otherPID)
			return info
		}

		if info.IsInstalled {
			info.StatusLabel = "Service Installed (LaunchAgent inactive)"
			info.SuggestedCmd = fmt.Sprintf("launchctl load %s", filepath.Join(home, "Library/LaunchAgents/com.inferport.c2go.plist"))
			info.ActionNote = "LaunchAgent plist is present. Load it with launchctl to start background syncing."
			return info
		}

		info.StatusLabel = "Standalone CLI (Not running as service)"
		info.SuggestedCmd = "./c2go"
		info.ActionNote = "Run ./c2go in your terminal to start real-time dynamic DNS synchronization."

	case "windows":
		info.Type = "windows"
		cmd := exec.Command("sc.exe", "query", "c2go")
		if out, err := cmd.CombinedOutput(); err == nil {
			info.IsInstalled = true
			if strings.Contains(string(out), "RUNNING") {
				info.IsRunning = true
				info.StatusLabel = "Service Active (Windows Service)"
				info.SuggestedCmd = "net stop c2go && net start c2go"
				info.ActionNote = "c2go is running as a Windows Service. Restart the service to apply changes."
				return info
			}
			info.StatusLabel = "Service Installed (Stopped)"
			info.SuggestedCmd = "net start c2go"
			info.ActionNote = "Start the Windows Service to begin dynamic DNS background sync."
			return info
		}

		info.StatusLabel = "Standalone Executable (No service)"
		info.SuggestedCmd = ".\\c2go.exe"
		info.ActionNote = "Run .\\c2go.exe in PowerShell or Command Prompt to start synchronization."
	}

	return info
}

func (s *Server) handleVerifyToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req verifyTokenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Token) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   "Token is required",
		})
		return
	}

	provider, err := dns.NewCloudflareProvider(req.Token)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	zones, err := provider.ListZones(r.Context())
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	var publicIP string
	if ip, err := ipcheck.GetPublicIP(r.Context()); err == nil {
		publicIP = ip
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":   true,
		"zones":     zones,
		"public_ip": publicIP,
	})
}

func (s *Server) handleZones(w http.ResponseWriter, r *http.Request) {
	// Paths: /api/zones/{domain}/records
	path := strings.TrimPrefix(r.URL.Path, "/api/zones/")
	parts := strings.Split(path, "/")
	if len(parts) < 2 || parts[1] != "records" {
		http.NotFound(w, r)
		return
	}
	domain := parts[0]

	if r.Method == http.MethodGet {
		token := r.URL.Query().Get("token")
		if token == "" {
			if stored, err := keyring.Get(config.ServiceName, config.TokenKey); err == nil {
				token = stored
			}
		}
		if token == "" {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "Token is required"})
			return
		}

		provider, err := dns.NewCloudflareProvider(token)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		records, err := provider.ListDNSRecordsDetails(r.Context(), domain)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"records": records,
		})
		return
	}

	if r.Method == http.MethodPost {
		var req createRecordReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "Invalid request"})
			return
		}

		provider, err := dns.NewCloudflareProvider(req.Token)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		err = provider.CreateARecord(r.Context(), domain, req.Name, req.Content, req.Proxied)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": true,
			"record": map[string]interface{}{
				"id":      req.Name,
				"name":    req.Name,
				"type":    "A",
				"proxied": req.Proxied,
				"content": req.Content,
			},
		})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req saveConfigReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "Invalid JSON"})
		return
	}

	if strings.TrimSpace(req.Token) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{"success": false, "error": "Token is required"})
		return
	}

	// Save token in keyring
	if err := keyring.Set(config.ServiceName, config.TokenKey, req.Token); err != nil {
		// Keyring might fail in some headless environments, proceed to save fallback
		console.LogInfo("Keyring warning: %v. Storing in protected config fallback.", err)
	}

	var managedZones []config.ManagedZone
	for _, z := range req.ManagedZones {
		var recNames []string
		for _, rec := range z.Records {
			recNames = append(recNames, rec.Name)
		}
		if len(recNames) > 0 {
			managedZones = append(managedZones, config.ManagedZone{
				Domain:  z.Domain,
				Records: recNames,
			})
		}
	}

	checkUpdates := req.CheckUpdates
	autoUpdate := req.AutoUpdate

	cfg := &config.Config{
		ManagedZones:        managedZones,
		PreferredInterfaces: req.PreferredInterfaces,
		HistoryEnabled:      req.HistoryEnabled,
		UpdateInterval:      req.UpdateInterval,
		UpdateCheck:         &checkUpdates,
		AutoUpdate:          &autoUpdate,
		Language:            req.Language,
		CloudflareToken:     req.Token,
	}

	if err := config.Save(cfg); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}

	i18n.Init(req.Language)
	console.LogSuccess("Configuration saved successfully via Web UI.")

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
