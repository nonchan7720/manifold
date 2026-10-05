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
//
// viper.AllKeys does not descend into sequences, so a list-valued setting is
// reached as a single leaf and its elements are substituted by walking the
// value itself (see substituteEnvVars). The value is only written back when a
// substitution actually happened, so that settings left untouched here keep
// resolving through viper's normal precedence (AutomaticEnv over the config
// file) instead of being pinned into the override layer.
func expandEnvVars(v *viper.Viper) error {
	for _, key := range v.AllKeys() {
		if elems, ok := v.Get(key).([]any); ok {
			expanded, changed, err := substituteEnvVars(elems)
			if err != nil {
				return err
			}
			if changed {
				v.Set(key, expanded)
			}
			continue
		}
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

// substituteEnvVars walks value, substituting ${VAR} references in every
// string it contains, and reports whether anything changed.
func substituteEnvVars(value any) (any, bool, error) {
	switch typed := value.(type) {
	case string:
		if !strings.Contains(typed, "$") {
			return value, false, nil
		}
		expanded, err := template.Substitute(typed, os.LookupEnv)
		if err != nil {
			return nil, false, err
		}
		return expanded, expanded != typed, nil
	case []any:
		out := make([]any, len(typed))
		changed := false
		for i, elem := range typed {
			expanded, elemChanged, err := substituteEnvVars(elem)
			if err != nil {
				return nil, false, err
			}
			out[i] = expanded
			changed = changed || elemChanged
		}
		return out, changed, nil
	case map[string]any:
		out := make(map[string]any, len(typed))
		changed := false
		for key, elem := range typed {
			expanded, elemChanged, err := substituteEnvVars(elem)
			if err != nil {
				return nil, false, err
			}
			out[key] = expanded
			changed = changed || elemChanged
		}
		return out, changed, nil
	default:
		return value, false, nil
	}
}

// configDecodeHook returns the mapstructure.DecodeHookFunc used to unmarshal
// the loaded config. It composes viper's default hooks (time.Duration and
// comma-separated slice decoding) with stringToJSONHookFunc, since
// supplying viper.DecodeHook replaces viper's defaults entirely.
// stringToJSONHookFunc runs before StringToSliceHookFunc so that a JSON array
// reaches it as a string rather than being split on commas first.
func configDecodeHook() mapstructure.DecodeHookFunc {
	return mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
		stringToJSONHookFunc(),
		mapstructure.StringToSliceHookFunc(","),
	)
}

// stringToJSONHookFunc decodes a string source into a map or slice by parsing
// it as JSON, so a whole map- or slice-typed field (e.g. telemetry headers or
// oauth2 clients) can be supplied through a single ${VAR} environment variable
// expansion. A slice is only decoded when the value looks like a JSON array,
// leaving comma-separated values to StringToSliceHookFunc.
func stringToJSONHookFunc() mapstructure.DecodeHookFunc {
	return func(f, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		raw, ok := data.(string)
		if !ok {
			return data, nil
		}
		if t.Kind() == reflect.Map && raw == "" {
			return reflect.Zero(t).Interface(), nil
		}
		// スライスは JSON 配列に見えるときだけ扱う。カンマ区切りの値は
		// StringToSliceHookFunc に任せる。
		isJSONSlice := t.Kind() == reflect.Slice &&
			strings.HasPrefix(strings.TrimSpace(raw), "[")
		if t.Kind() != reflect.Map && !isJSONSlice {
			return data, nil
		}
		out := reflect.New(t)
		if err := json.Unmarshal([]byte(raw), out.Interface()); err != nil {
			return nil, fmt.Errorf("decoding JSON into %s: %w", t, err)
		}
		return out.Elem().Interface(), nil
	}
}

// baseURLKeys returns the (lower-cased) names of the mcpServers entries whose
// baseURL key is present in the loaded config, after include merging, so an
// empty value can be told apart from an absent key (see markBaseURLSet).
//
// It must run before expandEnvVars. That writes every value it expands into
// viper's override layer, and once any key under mcpServers sits there,
// v.Get("mcpServers") returns the override layer's subtree alone — the keys
// no ${VAR} touched (a baseURL: "" next to a headers.Authorization: ${TOKEN},
// for instance) are not merged in — so the presence check would miss them.
// Before expansion the whole subtree still comes from the config layer.
func baseURLKeys(v *viper.Viper) map[string]bool {
	raw, _ := v.Get("mcpServers").(map[string]any)
	present := make(map[string]bool, len(raw))
	for name, fields := range raw {
		fields, _ := fields.(map[string]any)
		if _, ok := fields["baseurl"]; ok {
			present[strings.ToLower(name)] = true
		}
	}
	return present
}

// markBaseURLSet sets Server.BaseURLSet for each server named in present
// (the result of baseURLKeys). viper lower-cases keys, hence the
// case-insensitive match.
func markBaseURLSet(servers Servers, present map[string]bool) {
	for name, srv := range servers {
		srv.BaseURLSet = present[strings.ToLower(name)]
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

	// Same reasoning as fileFetch above — also makes OAUTH_CIMD_ENABLED,
	// OAUTH_CIMD_ALLOWEDORIGINS, OAUTH_CIMD_CACHETTL and
	// OAUTH_CIMD_MAXDOCUMENTSIZE effective overrides.
	v.SetDefault("oauth.cimd.enabled", false)
	v.SetDefault("oauth.cimd.allowedOrigins", []string{})
	v.SetDefault("oauth.cimd.cacheTTL", DefaultCIMDCacheTTL)
	v.SetDefault("oauth.cimd.maxDocumentSize", DefaultCIMDMaxDocumentSize)

	// Same reasoning as fileFetch above — also makes GATEWAY_TOOLSEARCH_ENABLED,
	// GATEWAY_TOOLSEARCH_THRESHOLD, GATEWAY_TOOLSEARCH_DEFAULTLIMIT,
	// GATEWAY_TOOLSEARCH_RESULTFORMAT and GATEWAY_TOOLSEARCH_DIGESTMAXTOOLS
	// effective overrides.
	v.SetDefault("gateway.toolSearch.enabled", false)
	v.SetDefault("gateway.toolSearch.threshold", DefaultToolSearchThreshold)
	v.SetDefault("gateway.toolSearch.defaultLimit", DefaultToolSearchLimit)
	v.SetDefault("gateway.toolSearch.resultFormat", ToolSearchResultFormatDefault)
	v.SetDefault("gateway.toolSearch.digestMaxTools", DefaultToolSearchDigestMaxTools)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	// Merge files listed under the top-level include key into the config.
	merged, err := loadWithIncludes(v.ConfigFileUsed())
	if err != nil {
		return nil, err
	}
	if err := v.MergeConfigMap(merged); err != nil {
		return nil, fmt.Errorf("error merging included config files: %w", err)
	}

	// Which servers spell out a baseURL key: read before env expansion, see
	// baseURLKeys.
	baseURLPresent := baseURLKeys(v)

	// Expand shell variables for string values loaded from yaml, supporting ${VAR:-default}
	if err := expandEnvVars(v); err != nil {
		return nil, err
	}

	var conf Config
	if err := v.Unmarshal(&conf, viper.DecodeHook(configDecodeHook())); err != nil {
		return nil, fmt.Errorf("unable to decode into struct: %w", err)
	}

	markBaseURLSet(conf.MCPServer, baseURLPresent)

	// Defensive fallback: guarantees a sane MaxSize even if a caller constructs
	// Config directly (bypassing viper), or explicitly sets fileFetch.maxSize: 0.
	conf.FileFetch = conf.FileFetch.WithDefaults()
	conf.OAuth.CIMD = conf.OAuth.CIMD.WithDefaults()
	conf.Gateway.ToolSearch = conf.Gateway.ToolSearch.WithDefaults()

	for name, srv := range conf.MCPServer {
		srv.Name = name
		for agentName, agent := range srv.Agents {
			if agent != nil {
				agent.Name = agentName
			}
		}
	}
	for name, agent := range conf.Agents {
		agent.Name = name
	}
	normalizeReverseOrigins(conf.MCPServer)

	if err := validation.ValidateWithContext(ctx, &conf); err != nil {
		return nil, err
	}
	// agents は両方を個別に検証した後でサーバー一覧へ加える。こうすることで
	// エージェントが Server.ValidateWithContext を通ることはなく、上の名前
	// 重複チェックも設定ファイルどおりの 2 つの map を比較できる。
	conf.MCPServer = mergeAgentsIntoServers(conf.MCPServer, conf.Agents)
	resolveServiceNames(conf.MCPServer)
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
