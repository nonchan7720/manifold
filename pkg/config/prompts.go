package config

import (
	"context"
	"regexp"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

// PromptsConfig は OpenAPI モードのサーバーが公開する prompts の取得元
// （mcpServers.<name>.prompts）。URL と File のどちらか一方を指定する。
//
//   - URL: prompts/list・prompts/get のたびに取得する。バックエンドが prompts の
//     定義を配信し、ゲートウェイのリリースと合わせずに更新できる。リクエストには
//     ツール呼び出しと同じ認証（authValue / oauth2 / tokenExchange）と headers が付く。
//   - File: 起動時に読み込み、以降は変更を監視して読み直す。Kubernetes の
//     ConfigMap をボリュームとしてマウントしたファイル（..data シンボリックリンクの
//     張り替えで更新される）を想定する。
type PromptsConfig struct {
	URL  string `mapstructure:"url"`
	File string `mapstructure:"file"`
}

// httpURLRegex は prompts.url に許すスキーム。is.RequestURL は file:// 等の
// スキームも通すため、http(s) に絞る。
var httpURLRegex = regexp.MustCompile(`^https?://`)

func (p PromptsConfig) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &p,
		validation.Field(&p.URL,
			validation.When(p.File == "",
				validation.Required.Error("one of url or file is required")),
			validation.When(p.File != "",
				validation.Empty.Error("url and file are mutually exclusive")),
			is.RequestURL.Error("must be an absolute http(s) URL"),
			validation.Match(httpURLRegex).Error("must be an absolute http(s) URL"),
		),
	)
}
