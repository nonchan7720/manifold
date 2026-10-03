package cmd

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/spf13/pflag"
)

// quickStartDefaultPort is the gateway port of a --openapi quick start
// (the port the README and Docker examples use).
const quickStartDefaultPort = 9999

// quickStartDefaultName is the server name of an --openapi spec given
// without a name= prefix.
const quickStartDefaultName = "api"

// quickStartNameRegex is what a "name=" prefix of --openapi must look like
// (the same characters as an mcpServers key). A URL never matches, since its
// part before the first '=' contains ':'.
var quickStartNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// quickStartFlags builds a config from command-line flags alone, so the
// gateway (and stdio mode) can run without a config file:
//
//	manifold stdio --openapi https://petstore3.swagger.io/api/v3/openapi.json
type quickStartFlags struct {
	specs      []string
	baseURL    string
	headers    []string
	include    []string
	exclude    []string
	toolSearch bool
	port       int
}

func (f *quickStartFlags) register(fs *pflag.FlagSet, withPort bool) {
	fs.StringArrayVar(&f.specs, "openapi", nil,
		"OpenAPI / Swagger spec (URL or file) to serve without a config file; "+
			"repeatable, optionally as name=spec (default name \"api\")")
	fs.StringVar(&f.baseURL, "base-url", "",
		"base URL of the API (with a single --openapi; default: derived from the spec)")
	fs.StringArrayVar(&f.headers, "header", nil,
		`header sent with every API request, as "Name: value" (repeatable)`)
	fs.StringArrayVar(&f.include, "include", nil,
		"only expose tools matching this glob pattern (repeatable)")
	fs.StringArrayVar(&f.exclude, "exclude", nil,
		"hide tools matching this glob pattern (repeatable)")
	fs.BoolVar(&f.toolSearch, "tool-search", false,
		"expose search_tools / call_tool instead of every tool (lazy tool loading)")
	if withPort {
		fs.IntVar(&f.port, "port", quickStartDefaultPort, "gateway port (with --openapi)")
	}
}

// enabled reports whether quick start flags were given, i.e. whether the
// config comes from flags instead of a config file.
func (f *quickStartFlags) enabled() bool {
	return len(f.specs) > 0
}

// parseQuickStartSpec splits "name=spec" into its parts; a plain spec gets
// an empty name.
func parseQuickStartSpec(arg string) (name, spec string) {
	if before, after, found := strings.Cut(
		arg,
		"=",
	); found &&
		quickStartNameRegex.MatchString(before) {
		return before, after
	}
	return "", arg
}

func parseQuickStartHeaders(headers []string) (map[string]string, error) {
	out := make(map[string]string, len(headers))
	for _, h := range headers {
		name, value, found := strings.Cut(h, ":")
		name = strings.TrimSpace(name)
		if !found || name == "" {
			return nil, fmt.Errorf(`--header %q: want "Name: value"`, h)
		}
		out[name] = strings.TrimSpace(value)
	}
	return out, nil
}

// config builds and validates the config the flags describe: one OpenAPI
// server per --openapi, the in-memory store, and — with more than one
// server — the aggregated /mcp endpoint.
func (f *quickStartFlags) config(ctx context.Context) (*config.Config, error) {
	if len(f.specs) > 1 && f.baseURL != "" {
		return nil, fmt.Errorf("--base-url can only be used with a single --openapi")
	}
	headers, err := parseQuickStartHeaders(f.headers)
	if err != nil {
		return nil, err
	}
	var tools *config.ToolsConfig
	if len(f.include) > 0 || len(f.exclude) > 0 {
		tools = &config.ToolsConfig{Include: f.include, Exclude: f.exclude}
	}
	var search *config.ToolSearchConfig
	if f.toolSearch {
		search = &config.ToolSearchConfig{Enabled: true}
	}

	servers := config.Servers{}
	for i, arg := range f.specs {
		name, spec := parseQuickStartSpec(arg)
		if spec == "" {
			return nil, fmt.Errorf("--openapi %q: spec is empty", arg)
		}
		if name == "" {
			name = quickStartDefaultName
			if i > 0 {
				name = fmt.Sprintf("%s%d", quickStartDefaultName, i+1)
			}
		}
		if _, dup := servers[name]; dup {
			return nil, fmt.Errorf("--openapi: server name %q is used more than once", name)
		}
		servers[name] = &config.Server{
			Description:  "OpenAPI: " + spec,
			Spec:         spec,
			BaseURL:      f.baseURL,
			ExtraHeaders: headers,
			Tools:        tools,
			ToolSearch:   search,
		}
	}

	cfg := &config.Config{
		Gateway:   config.Gateway{Port: f.port},
		MCPServer: servers,
		Memory:    &config.MemoryConfig{Enabled: true},
		FileFetch: config.FileFetchConfig{}.WithDefaults(),
		OAuth:     config.OAuthConfig{CIMD: config.CIMDConfig{}.WithDefaults()},
	}
	cfg.Authz = cfg.Authz.WithDefaults()
	if len(servers) > 1 {
		cfg.Gateway.Aggregate = config.AggregateConfig{Enabled: true, ToolSearch: search}
	}
	if _, err := cfg.ApplyEphemeralDefaults(); err != nil {
		return nil, err
	}
	if err := config.Finalize(ctx, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
