package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

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

	// Inline は、フォーム全体（レイアウトと初期値）を A2UI Express の <a2ui> ブロックと
	// してツール結果のテキストで返すかどうか。エージェントの出力に含まれる <a2ui>
	// ブロックを A2UI Agent SDK がコンパイルして UI へ渡すホスト（MCP のリソースを
	// UI から取得できない構成）向け。エージェントはそのブロックをそのまま出力する。
	// false なら、レイアウトは a2ui:// リソースで、初期値はツール結果の埋め込み
	// リソースで返す。
	Inline bool `mapstructure:"inline"`

	// AssumeSupport は、リクエストの _meta.a2ui で A2UI 対応を申告しない呼び出し元も
	// 対応とみなすかどうか。ゲートウェイは HTTP を Stateless で配信するため、
	// initialize の capabilities.a2ui で申告された内容は以降のリクエストでは
	// 分からない。申告があればそれに従う。
	AssumeSupport bool `mapstructure:"assumeSupport"`

	// Catalog は、エージェント（とレンダラ）が使う A2UI のカタログ（catalog.json）への
	// パス。フォームはこのカタログの部品で組み立てる。未指定なら A2UI v1.0 の basic
	// カタログ。カタログはエージェント側の持ち物なので、basic カタログ以外を使う
	// エージェントには、そのカタログを指定する。
	Catalog string `mapstructure:"catalog"`

	// Components は、フォームの部品の役割（basic カタログの部品名: Text, Column, Card,
	// Button, TextField, CheckBox, ChoicePicker, DateTimeInput）ごとに、カタログの
	// どの部品のどのプロパティを使うかを指定する。カタログが basic カタログと同じ
	// 名前の部品・プロパティを持つ役割は、指定しなくてよい。
	Components map[string]A2UIComponentConfig `mapstructure:"components"`
}

// A2UIComponentConfig は、フォームの部品の役割に割り当てるカタログの部品。
type A2UIComponentConfig struct {
	// Component はカタログの部品名。未指定なら役割の名前（basic カタログの部品名）。
	Component string `mapstructure:"component"`
	// Properties は、役割のプロパティ名（basic カタログのもの。label, value など）から
	// カタログの部品のプロパティ名への対応。未指定のプロパティは同じ名前を使う。
	Properties map[string]string `mapstructure:"properties"`
}

// A2UIRoles は a2ui.components で指定できる役割（basic カタログの部品名）。
var A2UIRoles = []string{
	"Text", "Column", "Card", "Button", "TextField", "CheckBox", "ChoicePicker", "DateTimeInput",
}

// Validate は a2ui の設定を検証する。カタログの中身との突き合わせは起動時に行う。
func (c A2UIConfig) Validate() error {
	if len(c.Components) > 0 && c.Catalog == "" {
		return errors.New("components requires catalog")
	}
	for role := range c.Components {
		known := slices.ContainsFunc(A2UIRoles, func(name string) bool {
			return strings.EqualFold(name, role)
		})
		if !known {
			return fmt.Errorf("components: unknown role %q (one of %s)",
				role, strings.Join(A2UIRoles, ", "))
		}
	}
	return nil
}

// A2UIForms は書き込み系ツールの A2UI フォームが有効かを返す。
func (s *Server) A2UIForms() bool {
	return s != nil && s.A2UI != nil && s.A2UI.Forms
}
