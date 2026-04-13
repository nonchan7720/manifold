package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/compose-spec/compose-go/v2/template"
	validation "github.com/go-ozzo/ozzo-validation/v4"
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
func Load(ctx context.Context) (*Config, error) {
	once.Do(func() {
		cachedConfig, errLoad = loadInternal(ctx)
	})
	return cachedConfig, errLoad
}

func loadInternal(ctx context.Context) (*Config, error) {
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
			expanded, err := template.Substitute(val, os.LookupEnv)
			if err != nil {
				return nil, err
			}
			v.Set(key, expanded)
		}
	}

	var conf Config
	if err := v.Unmarshal(&conf); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %w", err)
	}

	for name, srv := range conf.MCPServer {
		srv.Name = name
	}

	if err := validation.ValidateWithContext(ctx, &conf); err != nil {
		return nil, err
	}
	return &conf, nil
}
