package mcpsrv

import "github.com/nonchan7720/manifold/pkg/config"

type registerOpenAPIOption struct {
	auth               *config.AuthValue
	oauth2             *config.OAuth2
	tokenExchange      *config.TokenExchange
	googleSA           *config.GoogleServiceAccount
	generatedToolsFile string
	// excludeTools are tools.exclude patterns; an operation whose
	// operationId or derived tool name matches one is not turned into a
	// tool (see config.ToolsConfig).
	excludeTools []string
	// resultFilter は tools.resultFilter。有効なら、対象のツールに結果の JSON の
	// フィールド一覧を持たせる（WithResultFilter）。
	resultFilter *config.ResultFilterConfig
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

func WithGoogleServiceAccount(cfg *config.GoogleServiceAccount) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.googleSA = cfg
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

// WithResultFilter は、cfg（tools.resultFilter）が有効なときに、絞り込みの対象になるツールへ
// 結果の JSON のフィールド一覧（spec のレスポンス定義から読む）を持たせる。一覧は結果の
// 絞り込みのミドルウェアが tools/list で使い、_meta からは取り除くので、そのミドルウェアを
// 入れるサーバー（ServerToolMiddlewares）の構築にだけ渡す。nil や無効な cfg では何もしない。
func WithResultFilter(cfg *config.ResultFilterConfig) RegisterOpenAPIOption {
	return func(opt *registerOpenAPIOption) {
		opt.resultFilter = cfg
	}
}

// describesResult は、ツール name（元のツール名）に結果のフィールド一覧を持たせるかを返す。
func (opt *registerOpenAPIOption) describesResult(name string) bool {
	return opt.resultFilter != nil && opt.resultFilter.Enabled && opt.resultFilter.Applies(name)
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
