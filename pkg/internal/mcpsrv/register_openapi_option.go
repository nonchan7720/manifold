package mcpsrv

import "github.com/nonchan7720/manifold/pkg/config"

type registerOpenAPIOption struct {
	auth               *config.AuthValue
	oauth2             *config.OAuth2
	tokenExchange      *config.TokenExchange
	generatedToolsFile string
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
// generated tools file (tools.file) instead of fetching specPath: it reads
// path via oastomcptool.LoadGeneratedSpecSource (no network access) and
// verifies the result against the file's own "tools" section before
// returning. An empty path (the default) leaves the existing
// fetch-specPath behavior unchanged.
func WithGeneratedToolsFile(path string) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.generatedToolsFile = path
	}
}
