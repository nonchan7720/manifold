package config

// A2UIConfig は OpenAPI モードのサーバーが A2UI（Agent to UI, v1.0）の UI を
// MCP 越しに配信する設定（mcpServers.<name>.a2ui）。
//
// A2UI over MCP では、サーバーがレイアウトのテンプレートを a2ui:// リソース
// （application/a2ui+json）として公開し、ツールの _meta.ui.resourceUri で
// 結び付ける。ホストはテンプレートを自身のデザインシステムでネイティブに描画する。
type A2UIConfig struct {
	// Forms は書き込み系のツール（POST / PUT / PATCH / DELETE など、読み取り専用で
	// ないツール）に入力フォームを付けるかどうか。有効にすると、A2UI 対応の
	// 呼び出し元がそのツールを呼んだとき、API は実行せずにフォームを返す。フォームの
	// 送信ボタンは A2UI の action を発火するだけで、それを受けたエージェント側が
	// <tool>__submit を呼んだ時点で実行する。
	Forms bool `mapstructure:"forms"`

	// AssumeSupport は、リクエストの _meta.a2ui で A2UI 対応を申告しない呼び出し元も
	// 対応とみなすかどうか。ゲートウェイは HTTP を Stateless で配信するため、
	// initialize の capabilities.a2ui で申告された内容は以降のリクエストでは
	// 分からない。申告があればそれに従う。
	AssumeSupport bool `mapstructure:"assumeSupport"`
}

// A2UIForms は書き込み系ツールの A2UI フォームが有効かを返す。
func (s *Server) A2UIForms() bool {
	return s != nil && s.A2UI != nil && s.A2UI.Forms
}
