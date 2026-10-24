package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"golang.org/x/oauth2/google"
)

// GoogleServiceAccount は Google サービスアカウントのキーで取得したアクセス
// トークン（JWT bearer grant）をバックエンドへのリクエストに付ける認証設定。
// クラウドプロバイダ固有の認証は authValue / oauth2 / tokenExchange と並ぶ
// 兄弟のオプションとして持つ（他のプロバイダは別フィールドとして追加する）。
type GoogleServiceAccount struct {
	// CredentialsJSON はサービスアカウントキー（JSON）そのもの。
	// CredentialsFile と排他。
	CredentialsJSON string `mapstructure:"credentialsJSON"`
	// CredentialsFile はサービスアカウントキー（JSON）のファイルパス。
	// CredentialsJSON と排他。
	CredentialsFile string `mapstructure:"credentialsFile"`
	// Scopes は要求する OAuth2 スコープ。
	Scopes []string `mapstructure:"scopes"`
}

// Credentials はサービスアカウントキー（JSON）を返す。credentialsFile の場合は
// ファイルを読む。
func (g *GoogleServiceAccount) Credentials() ([]byte, error) {
	if g.CredentialsFile != "" {
		raw, err := os.ReadFile(filepath.Clean(g.CredentialsFile))
		if err != nil {
			return nil, fmt.Errorf("read credentialsFile: %w", err)
		}
		return raw, nil
	}
	return []byte(g.CredentialsJSON), nil
}

func (g *GoogleServiceAccount) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, g,
		validation.Field(&g.CredentialsJSON, validation.By(func(any) error {
			switch {
			case g.CredentialsJSON == "" && g.CredentialsFile == "":
				return fmt.Errorf("one of credentialsJSON or credentialsFile is required")
			case g.CredentialsJSON != "" && g.CredentialsFile != "":
				return fmt.Errorf("credentialsJSON and credentialsFile are mutually exclusive")
			}
			return nil
		})),
		validation.Field(&g.Scopes, validation.Required, validation.By(func(any) error {
			for _, scope := range g.Scopes {
				if strings.TrimSpace(scope) == "" {
					return fmt.Errorf("scope must not be empty")
				}
			}
			return nil
		})),
		// キーの形式は起動後の最初のリクエストで初めて失敗しないよう、設定ロード時に
		// 確認する（ネットワークには接続しない）。
		validation.Field(&g.CredentialsFile, validation.By(func(any) error {
			if (g.CredentialsJSON == "") == (g.CredentialsFile == "") {
				return nil // 排他違反・未設定は credentialsJSON 側で報告済み
			}
			raw, err := g.Credentials()
			if err != nil {
				return err
			}
			if _, err := google.JWTConfigFromJSON(raw, g.Scopes...); err != nil {
				return fmt.Errorf("invalid service account credentials: %w", err)
			}
			return nil
		})),
	)
}
