package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spf13/viper"
)

var (
	cachedConfig *Config
	once         sync.Once
	errLoad      error
)

func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

// Load reads the configuration from the config.yaml file and environment variables.
func Load() (*Config, error) {
	once.Do(func() {
		cachedConfig, errLoad = loadInternal()
	})
	return cachedConfig, errLoad
}

func loadInternal() (*Config, error) {
	root := findProjectRoot()

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath(root)
	v.AddConfigPath(filepath.Join(root, "config")) // Optional alternative

	// Enable ENV expansion
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Expand shell variables for string values loaded from yaml, supporting ${VAR:-default}
	for _, key := range v.AllKeys() {
		val := v.GetString(key)
		if strings.Contains(val, "$") {
			expanded := os.Expand(val, func(envVar string) string {
				parts := strings.SplitN(envVar, ":-", 2)
				val := os.Getenv(parts[0])
				if val == "" && len(parts) == 2 {
					return parts[1]
				}
				return val
			})
			v.Set(key, expanded)
		}
	}

	var conf Config
	if err := v.Unmarshal(&conf); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %w", err)
	}

	return &conf, nil
}
