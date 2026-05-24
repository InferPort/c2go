package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

const (
	AppName     = "c2go"
	ServiceName = "com.inferport.c2go"
	TokenKey    = "cloudflare_token"
)

type ManagedZone struct {
	Domain  string   `yaml:"domain"`
	Records []string `yaml:"records"`
}

type Config struct {
	ManagedZones   []ManagedZone `yaml:"managed_zones"`
	HistoryEnabled bool          `yaml:"history_enabled"`
	UpdateInterval int           `yaml:"update_interval"` // in seconds

	// Not exported to YAML
	CloudflareToken string `yaml:"-"`
}

// GetConfigPath returns the path to the configuration file.
func GetConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	appDir := filepath.Join(configDir, AppName)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(appDir, "config.yaml"), nil
}

// GetHistoryPath returns the path to the history file.
func GetHistoryPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, AppName, "history.json"), nil
}

// Load reads the config from disk and the token from the keyring.
func Load() (*Config, error) {
	path, err := GetConfigPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	// Retrieve the token from the keyring
	token, err := keyring.Get(ServiceName, TokenKey)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve token from keyring: %w", err)
	}
	cfg.CloudflareToken = token

	// Apply defaults
	if cfg.UpdateInterval <= 0 {
		cfg.UpdateInterval = 300
	}

	return &cfg, nil
}

// Save writes the configuration to disk.
func Save(cfg *Config) error {
	path, err := GetConfigPath()
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// ConfigExists checks if the configuration file already exists.
func ConfigExists() bool {
	path, err := GetConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return !os.IsNotExist(err)
}
