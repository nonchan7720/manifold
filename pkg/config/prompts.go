package config

import (
	"fmt"
	"net/url"
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

// validatePrompts は mcpServers.<name>.prompts を検証する。
func (s Server) validatePrompts(any) error {
	p := s.Prompts
	if p == nil {
		return nil
	}
	if !s.IsOpenAPI() {
		return fmt.Errorf(
			"prompts is only supported in OpenAPI mode (MCP backends relay the backend's own prompts)",
		)
	}
	switch {
	case p.URL == "" && p.File == "":
		return fmt.Errorf("one of url or file is required")
	case p.URL != "" && p.File != "":
		return fmt.Errorf("url and file are mutually exclusive")
	case p.URL != "":
		u, err := url.Parse(p.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("url must be an absolute http(s) URL")
		}
	}
	return nil
}
