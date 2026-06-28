package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config holds per-user CLI settings, persisted to ~/.config/watcloud/config.json.
type Config struct {
	DiscordWebhook string `json:"discord_webhook,omitempty"`
}

// path returns the per-user config file location (respects $XDG_CONFIG_HOME).
func path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "watcloud", "config.json"), nil
}

// Load reads the config file. A missing file is not an error — it returns an
// empty Config so callers can treat "unset" uniformly.
func Load() (Config, error) {
	var cfg Config
	p, err := path()
	if err != nil {
		return cfg, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Save writes the config file with owner-only permissions. The webhook is a
// secret, so the directory is 0700 and the file is 0600 — unreadable by other
// users on a shared login node.
func Save(cfg Config) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o600)
}
