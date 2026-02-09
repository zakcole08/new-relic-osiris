package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func debugLog(msg string) {
	logPath := filepath.Join(os.Getenv("HOME"), ".osiris", "debug.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\n", msg)
}

type Config struct {
	APIKey          string
	AccountID       string
	RefreshInterval int
}

func LoadConfig() *Config {
	cfg := &Config{
		RefreshInterval: 30,
	}

	configPath := getConfigPath()
	debugLog("Loading config from: " + configPath)
	
	file, err := os.Open(configPath)
	if err != nil {
		debugLog("Config not found at: " + configPath)
		return cfg
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "api_key":
			cfg.APIKey = value
			debugLog("Loaded API key (first 10 chars): " + value[:10])
		case "account_id":
			cfg.AccountID = value
			debugLog("Loaded account ID: " + value)
		case "refresh_interval":
			if interval, err := strconv.Atoi(value); err == nil {
				cfg.RefreshInterval = interval
			}
		}
	}

	return cfg
}

func getConfigPath() string {
	// Windows: %APPDATA%\.osiris\config
	// Linux/Mac: ~/.osiris/config
	if home, err := os.UserHomeDir(); err == nil {
		configDir := filepath.Join(home, ".osiris")
		return filepath.Join(configDir, "config")
	}
	return ".osiris/config"
}
