package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{
		HistoryEnabled: true,
		UpdateInterval: 300,
	}
	if cfg.UpdateInterval != 300 {
		t.Errorf("expected default interval 300, got %d", cfg.UpdateInterval)
	}
}

func TestManagedZoneSerialization(t *testing.T) {
	mz := ManagedZone{Domain: "example.com", Records: []string{"@", "www"}}
	data, err := json.Marshal(mz)
	if err != nil {
		t.Fatal(err)
	}
	var decoded ManagedZone
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Domain != "example.com" {
		t.Errorf("expected example.com, got %s", decoded.Domain)
	}
	if len(decoded.Records) != 2 || decoded.Records[0] != "@" {
		t.Errorf("unexpected records: %v", decoded.Records)
	}
}

func TestConfigExcludesTokenFromJSON(t *testing.T) {
	cfg := &Config{
		ManagedZones:    []ManagedZone{{Domain: "test.com", Records: []string{"@"}}},
		HistoryEnabled:  true,
		UpdateInterval:  300,
		CloudflareToken: "super-secret-token",
	}

	tokenBackup := cfg.CloudflareToken
	cfg.CloudflareToken = ""
	defer func() { cfg.CloudflareToken = tokenBackup }()

	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	if _, exists := raw["cloudflare_token"]; exists {
		t.Error("cloudflare_token should not be serialized to JSON")
	}
}

func TestConfigFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	cfg := &Config{
		HistoryEnabled:  false,
		UpdateInterval:  60,
		CloudflareToken: "",
	}

	tokenBk := cfg.CloudflareToken
	cfg.CloudflareToken = ""

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	cfg.CloudflareToken = tokenBk

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatal(err)
	}

	perm := info.Mode().Perm()
	if perm != 0600 {
		t.Errorf("expected 0600 permissions, got %o", perm)
	}
}

func TestGetHistoryPath(t *testing.T) {
	path, err := GetHistoryPath()
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Error("history path should not be empty")
	}
	if !strings.HasSuffix(path, "history.json") {
		t.Errorf("expected history.json suffix, got %s", path)
	}
}

func TestGetConfigPath_ReturnsJSON(t *testing.T) {
	path, err := GetConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "config.json") {
		t.Errorf("expected config.json suffix, got %s", path)
	}
}
