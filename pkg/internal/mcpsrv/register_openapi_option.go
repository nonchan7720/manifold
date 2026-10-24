package mcpsrv

import (
	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/internal/client"
)

type registerOpenAPIOption struct {
	auth               *config.AuthValue
	oauth2             *config.OAuth2
	tokenExchange      *config.TokenExchange
	cloudAuth          client.CloudAuth
	generatedToolsFile string
	// excludeTools are tools.exclude patterns; an operation whose
	// operationId or derived tool name matches one is not turned into a
	// tool (see config.ToolsConfig).
	excludeTools []string
}

type RegisterOpenAPIOption func(opt *registerOpenAPIOption)

func WithAuth(cfg *config.AuthValue) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.auth = cfg
	}
}

func WithOAuth2(cfg *config.OAuth2) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.oauth2 = cfg
	}
}

func WithTokenExchange(cfg *config.TokenExchange) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.tokenExchange = cfg
	}
}

// WithCloudAuth sets the cloud vendor credentials (googleServiceAccount /
// awsIAM) used to authenticate backend requests. A nil auth is a no-op.
func WithCloudAuth(auth client.CloudAuth) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.cloudAuth = auth
	}
}

// WithGeneratedToolsFile makes RegisterOpenAPI build its catalog from a
// generated tools file at path instead of fetching a live spec.
func WithGeneratedToolsFile(path string) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.generatedToolsFile = path
	}
}

// WithToolsConfig applies the catalog-building part of a server's tools
// config: tools.exclude, so excluded operations are never turned into tools.
// include / overrides are not applied here; they run when the tool list is
// served. A nil cfg is a no-op.
func WithToolsConfig(cfg *config.ToolsConfig) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		if cfg == nil {
			return
		}
		opt.excludeTools = cfg.Exclude
	}
}

// WithExcludeTools keeps every operation whose operationId or derived tool
// name matches one of the path.Match patterns (tools.exclude) out of the
// catalog.
func WithExcludeTools(patterns []string) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.excludeTools = patterns
	}
}

func newRegisterOpenAPIOption(opts []RegisterOpenAPIOption) *registerOpenAPIOption {
	opt := &registerOpenAPIOption{}
	for _, fn := range opts {
		fn(opt)
	}
	return opt
}
