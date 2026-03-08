package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Settings struct {
	ListenAddr string `json:"listen_addr"`
	RemoteURL  string `json:"remote_url"`
	Capture    bool   `json:"capture"`
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".what-the-claude")
}

func sessionsDir() string {
	return filepath.Join(configDir(), "sessions")
}

func settingsPath() string {
	return filepath.Join(configDir(), "settings.json")
}

func defaultSettings() Settings {
	return Settings{
		ListenAddr: "127.0.0.1:6543",
		RemoteURL:  "https://api.anthropic.com",
		Capture:    true,
	}
}

func loadSettings() (Settings, error) {
	s := defaultSettings()

	if err := os.MkdirAll(configDir(), 0755); err != nil {
		return s, err
	}
	if err := os.MkdirAll(sessionsDir(), 0755); err != nil {
		return s, err
	}

	data, err := os.ReadFile(settingsPath())
	if os.IsNotExist(err) {
		return s, saveSettings(s)
	}
	if err != nil {
		return s, err
	}

	if err := json.Unmarshal(data, &s); err != nil {
		return defaultSettings(), err
	}
	return s, nil
}

func saveSettings(s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(settingsPath(), data, 0644)
}
