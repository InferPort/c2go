package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const (
	AppName     = "c2go"
	ServiceName = "com.inferport.c2go"
	TokenKey    = "cloudflare_token"
)

type ManagedZone struct {
	Domain  string   `json:"domain"`
	Records []string `json:"records"`
}

type Config struct {
	ManagedZones    []ManagedZone `json:"managed_zones"`
	HistoryEnabled  bool          `json:"history_enabled"`
	UpdateInterval  int           `json:"update_interval"`
	CloudflareToken string        `json:"-"`
}

func GetConfigPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	appDir := filepath.Join(configDir, AppName)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(appDir, "config.json"), nil
}

func GetHistoryPath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(configDir, AppName, "history.json"), nil
}

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
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}

	token, err := keyring.Get(ServiceName, TokenKey)
	if err != nil {
		if data, err := os.ReadFile(path); err == nil {
			var legacy struct {
				CloudflareToken string `json:"cloudflare_token"`
			}
			if json.Unmarshal(data, &legacy) == nil && legacy.CloudflareToken != "" {
				cfg.CloudflareToken = legacy.CloudflareToken
				return &cfg, nil
			}
		}
		return nil, fmt.Errorf("failed to retrieve token from keyring: %w", err)
	}
	cfg.CloudflareToken = token

	if cfg.UpdateInterval <= 0 {
		cfg.UpdateInterval = 300
	}

	return &cfg, nil
}

func Save(cfg *Config) error {
	path, err := GetConfigPath()
	if err != nil {
		return err
	}

	token := cfg.CloudflareToken

	cfg.CloudflareToken = ""
	defer func() { cfg.CloudflareToken = token }()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}

	if token != "" {
		if err := keyring.Set(ServiceName, TokenKey, token); err != nil {
			return fmt.Errorf("failed to save token to keyring: %w", err)
		}
	}

	return nil
}

func ConfigExists() bool {
	path, err := GetConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return !os.IsNotExist(err)
}
