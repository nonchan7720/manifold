package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/compose-spec/compose-go/v2/template"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-viper/mapstructure/v2"
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
func Load(ctx context.Context, configName string) (*Config, error) {
	once.Do(func() {
		cachedConfig, errLoad = loadInternal(ctx, configName)
	})
	return cachedConfig, errLoad
}

// expandEnvVars substitutes ${VAR} / ${VAR:-default} references in every
// string value viper has loaded, using template.Substitute against the
// process environment.
func expandEnvVars(v *viper.Viper) error {
	for _, key := range v.AllKeys() {
		val := v.GetString(key)
		if !strings.Contains(val, "$") {
			continue
		}
		expanded, err := template.Substitute(val, os.LookupEnv)
		if err != nil {
			return err
		}
		v.Set(key, expanded)
	}
	return nil
}

// configDecodeHook returns the mapstructure.DecodeHookFunc used to unmarshal
// the loaded config. It composes viper's default hooks (time.Duration and
// comma-separated slice decoding) with stringToJSONMapHookFunc, since
// supplying viper.DecodeHook replaces viper's defaults entirely.
func configDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
		stringToJSONMapHookFunc(),
	)
}

// stringToJSONMapHookFunc decodes a string source into a map by parsing it as
// JSON, so a map-typed field (e.g. telemetry headers) can be supplied whole
// through a single ${VAR} environment variable expansion.
func stringToJSONMapHookFunc() mapstructure.DecodeHookFunc {
	return func(f, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String || t.Kind() != reflect.Map {
			return data, nil
		}
		raw, ok := data.(string)
		if !ok {
			return data, nil
		}
		if raw == "" {
			return reflect.Zero(t).Interface(), nil
		}
		out := reflect.New(t)
		if err := json.Unmarshal([]byte(raw), out.Interface()); err != nil {
			return nil, fmt.Errorf("decoding JSON into %s: %w", t, err)
		}
		return out.Elem().Interface(), nil
	}
}

func loadInternal(ctx context.Context, configName string) (*Config, error) {
	if configName == "" {
		configName = "config"
	}
	root := findProjectRoot()

	v := viper.New()
	v.SetConfigName(configName)
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	v.AddConfigPath(root)
	v.AddConfigPath(filepath.Join(root, "config")) // Optional alternative

	// Enable ENV expansion
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Defaults must be registered explicitly for viper.AutomaticEnv() to take effect
	// during Unmarshal — keys with no default, config-file entry, or bound env var
	// are simply absent from the decoded struct's source data. Registering these
	// defaults also makes the corresponding env vars (FILEFETCH_MAXSIZE,
	// FILEFETCH_ALLOWLOCAL, FILEFETCH_ALLOWEDHOSTS) effective overrides.
	v.SetDefault("fileFetch.maxSize", DefaultFileFetchMaxSize)
	v.SetDefault("fileFetch.allowLocal", false)
	v.SetDefault("fileFetch.allowedHosts", []string{})

	// Same reasoning as fileFetch above — also makes AUTHZ_ENABLED, AUTHZ_OPAURL,
	// AUTHZ_TIMEOUT, AUTHZ_DECISIONPATH_LIST, AUTHZ_DECISIONPATH_CALL,
	// AUTHZ_DECISIONPATH_CATALOG, AUTHZ_HEADERS_USERID, AUTHZ_HEADERS_USERGROUPS,
	// AUTHZ_HEADERS_BYPASS, AUTHZ_INPUT_USER, AUTHZ_INPUT_GROUPS, AUTHZ_INPUT_SERVER,
	// AUTHZ_INPUT_TOOL, AUTHZ_INPUT_TOOLS, AUTHZ_INPUT_TOOLNAME effective overrides.
	v.SetDefault("authz.enabled", false)
	v.SetDefault("authz.opaURL", DefaultAuthzOPAURL)
	v.SetDefault("authz.timeout", DefaultAuthzTimeout)
	v.SetDefault("authz.decisionPath.list", DefaultAuthzDecisionPathList)
	v.SetDefault("authz.decisionPath.call", DefaultAuthzDecisionPathCall)
	v.SetDefault("authz.decisionPath.catalog", DefaultAuthzDecisionPathCatalog)
	v.SetDefault("authz.headers.userID", DefaultAuthzHeaderUserID)
	v.SetDefault("authz.headers.userGroups", DefaultAuthzHeaderUserGroups)
	v.SetDefault("authz.headers.bypass", DefaultAuthzHeaderBypass)
	v.SetDefault("authz.input.user", DefaultAuthzInputUser)
	v.SetDefault("authz.input.groups", DefaultAuthzInputGroups)
	v.SetDefault("authz.input.server", DefaultAuthzInputServer)
	v.SetDefault("authz.input.tool", DefaultAuthzInputTool)
	v.SetDefault("authz.input.tools", DefaultAuthzInputTools)
	v.SetDefault("authz.input.toolName", DefaultAuthzInputToolName)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Expand shell variables for string values loaded from yaml, supporting ${VAR:-default}
	if err := expandEnvVars(v); err != nil {
		return nil, err
	}

	var conf Config
	if err := v.Unmarshal(&conf, viper.DecodeHook(configDecodeHook())); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %w", err)
	}

	// Defensive fallback: guarantees a sane MaxSize even if a caller constructs
	// Config directly (bypassing viper), or explicitly sets fileFetch.maxSize: 0.
	conf.FileFetch = conf.FileFetch.WithDefaults()

	for name, srv := range conf.MCPServer {
		srv.Name = name
	}
	normalizeReverseOrigins(conf.MCPServer)

	if err := validation.ValidateWithContext(ctx, &conf); err != nil {
		return nil, err
	}
	return &conf, nil
}

// normalizeReverseOrigins rewrites each reverse Server's Origin to its
// normalized form (see NormalizeOrigin) so downstream origin comparisons
// (config uniqueness, app.up matching) don't need to re-normalize. Servers
// with an origin that fails to normalize are left untouched; validation
// reports the bad value.
func normalizeReverseOrigins(servers Servers) {
	for _, srv := range servers {
		if srv.Transport != MCPTransportReverse {
			continue
		}
		if normalized, err := NormalizeOrigin(srv.Origin); err == nil {
			srv.Origin = normalized
		}
	}
}
