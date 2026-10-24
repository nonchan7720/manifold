package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
	"golang.org/x/oauth2/google"
)

// GoogleServiceAccount は Google のサービスアカウント鍵で OAuth2 アクセストークンを
// 取得し、バックエンドへのリクエストに Authorization: Bearer として付与する認証方式。
// authValue / oauth2 / tokenExchange / awsIAM とは排他。
//
// credentialsJSON は ${VAR} の環境変数展開（設定ローダー共通）で渡せる。
// credentialsFile と credentialsJSON はどちらか一方だけを指定する。
type GoogleServiceAccount struct {
	CredentialsJSON string   `mapstructure:"credentialsJSON"`
	CredentialsFile string   `mapstructure:"credentialsFile"`
	Scopes          []string `mapstructure:"scopes"`
}

// LoadCredentials は鍵 JSON の中身を返す。credentialsFile の読み込みは呼び出し時に行う。
func (g *GoogleServiceAccount) LoadCredentials() ([]byte, error) {
	if g.CredentialsJSON != "" {
		return []byte(g.CredentialsJSON), nil
	}
	raw, err := os.ReadFile(filepath.Clean(g.CredentialsFile))
	if err != nil {
		return nil, fmt.Errorf("read googleServiceAccount.credentialsFile: %w", err)
	}
	return raw, nil
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
		validation.Field(&g.CredentialsFile, validation.By(func(any) error {
			// ファイルの読み込み失敗は起動後の初回リクエストで報告する
			// （検証だけを別環境で行うケースを妨げない）。読めた場合のみ中身を検証する。
			if g.CredentialsFile == "" || g.CredentialsJSON != "" {
				return nil
			}
			raw, err := os.ReadFile(filepath.Clean(g.CredentialsFile))
			if err != nil {
				return nil //nolint:nilerr // reported when the first request is made
			}
			return validateServiceAccountJSON(raw)
		})),
		validation.Field(&g.Scopes, validation.Required, validation.Each(validation.Required)),
	)
}

// validateServiceAccountJSON は鍵 JSON がサービスアカウント鍵として解釈できることを確認する。
// エラーメッセージに鍵の中身が含まれないよう、元のエラーは出さない。
func validateServiceAccountJSON(raw []byte) error {
	if _, err := google.JWTConfigFromJSON(
		raw,
		"https://www.googleapis.com/auth/cloud-platform",
	); err != nil {
		return fmt.Errorf("is not a valid service account key JSON (type: service_account)")
	}
	return nil
}

// AWSIAM は AWS の認証情報（既定の認証情報チェーン）で SigV4 署名を行う認証方式。
// authValue / oauth2 / tokenExchange / googleServiceAccount とは排他。
type AWSIAM struct {
	Region  string `mapstructure:"region"`
	Service string `mapstructure:"service"`
	// Profile は共有設定ファイルのプロファイル名（任意）。
	Profile string `mapstructure:"profile"`
	// RoleARN を指定すると、既定の認証情報で sts:AssumeRole したうえで署名する（任意）。
	RoleARN string `mapstructure:"roleARN"`
}

func (a *AWSIAM) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, a,
		validation.Field(&a.Region, validation.Required),
		validation.Field(&a.Service, validation.Required),
		validation.Field(&a.RoleARN, validation.By(func(any) error {
			if a.RoleARN != "" && !strings.HasPrefix(a.RoleARN, "arn:") {
				return fmt.Errorf("must be an ARN (arn:...)")
			}
			return nil
		})),
	)
}

// ServerAuth は下流（MCP クライアント → Manifold）の認証設定。
// 未設定ならこれまでどおり Bearer トークンを検証せず透過する。
type ServerAuth struct {
	JWT *DownstreamJWT `mapstructure:"jwt"`
}

func (a *ServerAuth) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, a, validation.Field(&a.JWT))
}

// DownstreamJWT は /mcp/{server_name} の Bearer JWT を Manifold が検証する設定。
// 検証ロジックは identities の source: jwt と共通（issuer / JWKS 署名 / audience）。
type DownstreamJWT struct {
	Issuer   string `mapstructure:"issuer"`
	JWKSURL  string `mapstructure:"jwksURL"`
	Audience string `mapstructure:"audience"`
}

func (j *DownstreamJWT) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, j,
		validation.Field(&j.Issuer, validation.Required, is.RequestURL),
		validation.Field(&j.JWKSURL, validation.Required, is.RequestURL),
	)
}

// Profile は identity パッケージの JWT 検証にそのまま渡せるプロファイルへ変換する。
func (j *DownstreamJWT) Profile() *IdentityProfile {
	return &IdentityProfile{
		Source:   IdentitySourceJWT,
		Issuer:   j.Issuer,
		JWKSURL:  j.JWKSURL,
		Audience: j.Audience,
	}
}

// cloudAuthCount は設定されているクラウドベンダー認証の数を返す。
func (s Server) cloudAuthCount() int {
	n := 0
	if s.GoogleServiceAccount != nil {
		n++
	}
	if s.AWSIAM != nil {
		n++
	}
	return n
}
