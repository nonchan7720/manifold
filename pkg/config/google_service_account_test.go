package config

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeServiceAccountJSON は生成した RSA 鍵で組み立てた偽のサービスアカウントキー。
func fakeServiceAccountJSON(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemKey := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: mustPKCS8(t, key),
	})
	raw, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"client_email":   "sa@example.iam.gserviceaccount.com",
		"private_key_id": "kid",
		"private_key":    string(pemKey),
		"token_uri":      tokenURI,
	})
	require.NoError(t, err)
	return string(raw)
}

func mustPKCS8(t *testing.T, key *rsa.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	return der
}

const testGoogleScope = "https://www.googleapis.com/auth/analytics.readonly"

func TestGoogleServiceAccount_Validate(t *testing.T) {
	saJSON := fakeServiceAccountJSON(t, "https://oauth2.googleapis.com/token")
	file := filepath.Join(t.TempDir(), "sa.json")
	require.NoError(t, os.WriteFile(file, []byte(saJSON), 0o600))
	badFile := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(badFile, []byte("{"), 0o600))

	tests := []struct {
		name    string
		in      GoogleServiceAccount
		wantErr string
	}{
		{
			name: "credentialsJSON",
			in:   GoogleServiceAccount{CredentialsJSON: saJSON, Scopes: []string{testGoogleScope}},
		},
		{
			name: "credentialsFile",
			in:   GoogleServiceAccount{CredentialsFile: file, Scopes: []string{testGoogleScope}},
		},
		{
			name: "どちらも未設定は ADC",
			in:   GoogleServiceAccount{Scopes: []string{testGoogleScope}},
		},
		{
			name:    "ADC でも scopes は必須",
			in:      GoogleServiceAccount{},
			wantErr: "Scopes",
		},
		{
			name: "両方設定",
			in: GoogleServiceAccount{
				CredentialsJSON: saJSON, CredentialsFile: file, Scopes: []string{testGoogleScope},
			},
			wantErr: "mutually exclusive",
		},
		{
			name:    "scopes 未設定",
			in:      GoogleServiceAccount{CredentialsJSON: saJSON},
			wantErr: "Scopes",
		},
		{
			name:    "空の scope",
			in:      GoogleServiceAccount{CredentialsJSON: saJSON, Scopes: []string{" "}},
			wantErr: "scope must not be empty",
		},
		{
			name:    "JSON が不正",
			in:      GoogleServiceAccount{CredentialsJSON: "{", Scopes: []string{testGoogleScope}},
			wantErr: "invalid service account credentials",
		},
		{
			name: "ファイルが存在しない",
			in: GoogleServiceAccount{
				CredentialsFile: filepath.Join(
					t.TempDir(),
					"none.json",
				),
				Scopes: []string{testGoogleScope},
			},
			wantErr: "read credentialsFile",
		},
		{
			name: "ファイルの中身が不正",
			in: GoogleServiceAccount{
				CredentialsFile: badFile,
				Scopes:          []string{testGoogleScope},
			},
			wantErr: "invalid service account credentials",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.ValidateWithContext(t.Context())
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func validGoogleServiceAccount(t *testing.T) *GoogleServiceAccount {
	t.Helper()
	return &GoogleServiceAccount{
		CredentialsJSON: fakeServiceAccountJSON(t, "https://oauth2.googleapis.com/token"),
		Scopes:          []string{testGoogleScope},
	}
}

func TestServer_ValidateWithContext_GoogleServiceAccount_Valid(t *testing.T) {
	s := baseValidServer()
	s.GoogleServiceAccount = validGoogleServiceAccount(t)
	require.NoError(t, s.ValidateWithContext(t.Context()))
}

func TestServer_ValidateWithContext_GoogleServiceAccount_OpenAPI_Valid(t *testing.T) {
	s := Server{
		Description:          "ga4",
		Spec:                 "https://example.com/openapi.yaml",
		BaseURL:              "https://analyticsdata.googleapis.com",
		GoogleServiceAccount: validGoogleServiceAccount(t),
	}
	require.NoError(t, s.ValidateWithContext(t.Context()))
}

func TestServer_ValidateWithContext_GoogleServiceAccount_ExclusiveWithOtherAuth(t *testing.T) {
	tests := map[string]func(s *Server){
		"authValue": func(s *Server) {
			s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
		},
		"oauth2": func(s *Server) {
			s.OAuth2 = &OAuth2{
				ClientID:     "id",
				ClientSecret: "secret",
				AuthURL:      "https://example.com/auth",
				TokenURL:     "https://example.com/token",
			}
		},
		"tokenExchange": func(s *Server) {
			s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
		},
	}
	for name, set := range tests {
		t.Run(name, func(t *testing.T) {
			s := baseValidServer()
			s.GoogleServiceAccount = validGoogleServiceAccount(t)
			set(&s)
			err := s.ValidateWithContext(t.Context())
			require.Error(t, err)
			require.Contains(t, err.Error(), "googleServiceAccount")
		})
	}
}

func TestServer_ValidateWithContext_GoogleServiceAccount_RejectsStdio(t *testing.T) {
	s := Server{
		Description:          "stdio",
		Transport:            MCPTransportStdio,
		Command:              "/bin/server",
		GoogleServiceAccount: validGoogleServiceAccount(t),
	}
	require.Error(t, s.ValidateWithContext(t.Context()))
}

func TestServer_ValidateWithContext_Reverse_RejectsGoogleServiceAccount(t *testing.T) {
	s := baseValidReverseServer()
	s.GoogleServiceAccount = validGoogleServiceAccount(t)
	require.Error(t, s.ValidateWithContext(reverseValidationContext(t)))
}

func TestServer_ValidateWithContext_GoogleServiceAccount_ValidatesNested(t *testing.T) {
	s := baseValidServer()
	s.GoogleServiceAccount = &GoogleServiceAccount{}
	require.Error(t, s.ValidateWithContext(t.Context()))
}

func TestServer_ValidateWithContext_AuthJWT(t *testing.T) {
	valid := func() *ServerAuth {
		return &ServerAuth{JWT: &DownstreamJWT{
			Issuer:   "https://issuer.example.com",
			JWKSURL:  "https://issuer.example.com/keys",
			Audience: "manifold",
		}}
	}
	tests := []struct {
		name    string
		mutate  func(a *ServerAuth)
		wantErr bool
	}{
		{name: "valid", mutate: func(*ServerAuth) {}},
		{name: "issuer 必須", mutate: func(a *ServerAuth) { a.JWT.Issuer = "" }, wantErr: true},
		{name: "jwksURL 必須", mutate: func(a *ServerAuth) { a.JWT.JWKSURL = "" }, wantErr: true},
		{name: "audience 必須", mutate: func(a *ServerAuth) { a.JWT.Audience = "" }, wantErr: true},
		{
			name:    "jwksURL は絶対 URL",
			mutate:  func(a *ServerAuth) { a.JWT.JWKSURL = "/keys" },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := baseValidServer()
			s.Auth = valid()
			tt.mutate(s.Auth)
			err := s.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServer_ValidateWithContext_AuthJWT_CompatibleWithBackendAuth(t *testing.T) {
	// auth.jwt は下流の検証で、バックエンドへの認証とは独立している。
	s := baseValidServer()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	s.Auth = &ServerAuth{JWT: &DownstreamJWT{
		Issuer:   "https://issuer.example.com",
		JWKSURL:  "https://issuer.example.com/keys",
		Audience: "a",
	}}
	require.NoError(t, s.ValidateWithContext(t.Context()))
}

func TestServer_ValidateWithContext_Reverse_RejectsAuth(t *testing.T) {
	s := baseValidReverseServer()
	s.Auth = &ServerAuth{JWT: &DownstreamJWT{
		Issuer:   "https://issuer.example.com",
		JWKSURL:  "https://issuer.example.com/keys",
		Audience: "a",
	}}
	require.Error(t, s.ValidateWithContext(reverseValidationContext(t)))
}

func TestLoadInternal_GoogleServiceAccountAndAuthJWT(t *testing.T) {
	// ${...} は credentialsJSON を含む全ての文字列に展開が効く。
	t.Setenv(
		"GA4_SERVICE_ACCOUNT_JSON",
		fakeServiceAccountJSON(t, "https://oauth2.googleapis.com/token"),
	)
	t.Setenv("TEST_AUDIENCE", "manifold")
	cfg, err := loadFromYAML(t, `
mcpServers:
  ga4:
    description: ga4
    spec: https://example.com/openapi.yaml
    baseURL: https://analyticsdata.googleapis.com
    googleServiceAccount:
      credentialsJSON: ${GA4_SERVICE_ACCOUNT_JSON}
      scopes:
        - https://www.googleapis.com/auth/analytics.readonly
    auth:
      jwt:
        issuer: https://issuer.example.com
        jwksURL: https://issuer.example.com/keys
        audience: ${TEST_AUDIENCE}
`)
	require.NoError(t, err)
	srv := cfg.MCPServer["ga4"]
	require.NotNil(t, srv.GoogleServiceAccount)
	require.Contains(t, srv.GoogleServiceAccount.CredentialsJSON, "client_email")
	require.Equal(t, []string{testGoogleScope}, srv.GoogleServiceAccount.Scopes)
	require.Equal(t, "manifold", srv.Auth.JWT.Audience)
	require.Equal(t, "https://issuer.example.com/keys", srv.Auth.JWT.JWKSURL)
}
