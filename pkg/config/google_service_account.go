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

// GoogleServiceAccount は Google のアクセストークンをバックエンドへのリクエストに
// 付ける認証設定。credentialsJSON / credentialsFile のどちらも無ければ Application
// Default Credentials（GKE Workload Identity・GCE メタデータサーバー・
// GOOGLE_APPLICATION_CREDENTIALS・gcloud のユーザー資格情報など）を使う。
// クラウドプロバイダ固有の認証は authValue / oauth2 / tokenExchange と並ぶ
// 兄弟のオプションとして持つ（他のプロバイダは別フィールドとして追加する）。
type GoogleServiceAccount struct {
	// CredentialsJSON はサービスアカウントキー（JSON）そのもの。
	// CredentialsFile と排他。どちらも無ければ ADC を使う。
	CredentialsJSON string `mapstructure:"credentialsJSON"`
	// CredentialsFile はサービスアカウントキー（JSON）のファイルパス。
	// CredentialsJSON と排他。
	CredentialsFile string `mapstructure:"credentialsFile"`
	// Scopes は要求する OAuth2 スコープ。
	Scopes []string `mapstructure:"scopes"`
}

// UseADC は明示的なキーが無く、ADC で資格情報を解決することを返す。
func (g *GoogleServiceAccount) UseADC() bool {
	return g.CredentialsJSON == "" && g.CredentialsFile == ""
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
			if g.CredentialsJSON != "" && g.CredentialsFile != "" {
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
				return nil // 排他違反は credentialsJSON 側で報告済み、未設定は ADC（ロード時には解決しない）
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
