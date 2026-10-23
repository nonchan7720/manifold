package mcpsrv

import "github.com/nonchan7720/manifold/pkg/config"

type registerOpenAPIOption struct {
	auth               *config.AuthValue
	oauth2             *config.OAuth2
	tokenExchange      *config.TokenExchange
	generatedToolsFile string
	// excludeOperationIDs are path.Match patterns; an operation whose
	// operationId matches one is not turned into a tool (see
	// config.ToolsConfig.ExcludeOperationIDs).
	excludeOperationIDs []string
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

// WithGeneratedToolsFile makes RegisterOpenAPI build its catalog from a
// generated tools file at path instead of fetching a live spec.
func WithGeneratedToolsFile(path string) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.generatedToolsFile = path
	}
}

// WithToolsConfig applies the catalog-building parts of a server's tools
// config (currently tools.excludeOperationIds). The exposure filter
// (include / exclude / overrides) is not applied here: it runs when the tool
// list is served. A nil cfg is a no-op.
func WithToolsConfig(cfg *config.ToolsConfig) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		if cfg == nil {
			return
		}
		opt.excludeOperationIDs = cfg.ExcludeOperationIDs
	}
}

// WithExcludeOperationIDs keeps every operation whose operationId matches one
// of the path.Match patterns out of the catalog.
func WithExcludeOperationIDs(patterns []string) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.excludeOperationIDs = patterns
	}
}

func newRegisterOpenAPIOption(opts []RegisterOpenAPIOption) *registerOpenAPIOption {
	opt := &registerOpenAPIOption{}
	for _, fn := range opts {
		fn(opt)
	}
	return opt
}
