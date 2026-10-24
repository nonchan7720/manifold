package config

import (
	"context"
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

func testServiceAccountJSON(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]string{
		"type":           "service_account",
		"client_email":   "sa@example.iam.gserviceaccount.com",
		"private_key_id": "kid",
		"private_key":    string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"token_uri":      "https://oauth2.googleapis.com/token",
	})
	require.NoError(t, err)
	return string(raw)
}

func TestGoogleServiceAccount_Validate(t *testing.T) {
	creds := testServiceAccountJSON(t)
	file := filepath.Join(t.TempDir(), "sa.json")
	require.NoError(t, os.WriteFile(file, []byte(creds), 0o600))
	badFile := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(badFile, []byte("{}"), 0o600))
	scopes := []string{"https://www.googleapis.com/auth/analytics.readonly"}

	tests := []struct {
		name    string
		cfg     GoogleServiceAccount
		wantErr bool
	}{
		{"json", GoogleServiceAccount{CredentialsJSON: creds, Scopes: scopes}, false},
		{"file", GoogleServiceAccount{CredentialsFile: file, Scopes: scopes}, false},
		{
			"missing file is tolerated",
			GoogleServiceAccount{CredentialsFile: "/nonexistent/sa.json", Scopes: scopes},
			false,
		},
		{"neither", GoogleServiceAccount{Scopes: scopes}, true},
		{
			"both",
			GoogleServiceAccount{CredentialsJSON: creds, CredentialsFile: file, Scopes: scopes},
			true,
		},
		{"no scopes", GoogleServiceAccount{CredentialsJSON: creds}, true},
		{"empty scope", GoogleServiceAccount{CredentialsJSON: creds, Scopes: []string{""}}, true},
		{"invalid json", GoogleServiceAccount{CredentialsJSON: "not json", Scopes: scopes}, false},
		{
			"invalid file content",
			GoogleServiceAccount{CredentialsFile: badFile, Scopes: scopes},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.ValidateWithContext(t.Context())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServer_Validate_GoogleServiceAccountExclusive(t *testing.T) {
	creds := testServiceAccountJSON(t)
	google := func() *GoogleServiceAccount {
		return &GoogleServiceAccount{CredentialsJSON: creds, Scopes: []string{"s"}}
	}

	s := baseValidServer()
	s.GoogleServiceAccount = google()
	require.NoError(t, s.ValidateWithContext(t.Context()))

	s = baseValidServer()
	s.GoogleServiceAccount = google()
	s.AuthValue = &AuthValue{Header: "X-Api-Key", Value: "secret"}
	require.Error(t, s.ValidateWithContext(t.Context()))

	s = baseValidServer()
	s.GoogleServiceAccount = google()
	s.TokenExchange = &TokenExchange{URL: "https://example.com/token"}
	require.Error(t, s.ValidateWithContext(t.Context()))

	s = baseValidServer()
	s.GoogleServiceAccount = google()
	s.AWSIAM = &AWSIAM{Region: "us-east-1", Service: "execute-api"}
	require.Error(t, s.ValidateWithContext(t.Context()))
}

func TestAWSIAM_Validate(t *testing.T) {
	require.NoError(
		t,
		(&AWSIAM{Region: "us-east-1", Service: "execute-api"}).ValidateWithContext(t.Context()),
	)
	require.NoError(t, (&AWSIAM{
		Region: "us-east-1", Service: "execute-api", Profile: "p", RoleARN: "arn:aws:iam::123456789012:role/r",
	}).ValidateWithContext(t.Context()))
	require.Error(t, (&AWSIAM{Service: "execute-api"}).ValidateWithContext(t.Context()))
	require.Error(t, (&AWSIAM{Region: "us-east-1"}).ValidateWithContext(t.Context()))
	require.Error(
		t,
		(&AWSIAM{Region: "us-east-1", Service: "s", RoleARN: "role"}).ValidateWithContext(
			t.Context(),
		),
	)

	s := baseValidServer()
	s.AWSIAM = &AWSIAM{Region: "us-east-1", Service: "execute-api"}
	s.OAuth2 = &OAuth2{
		ClientID: "id", ClientSecret: "secret",
		AuthURL: "https://example.com/auth", TokenURL: "https://example.com/token",
	}
	require.Error(t, s.ValidateWithContext(t.Context()))
}

func TestServer_Validate_AuthJWT(t *testing.T) {
	valid := func() *ServerAuth {
		return &ServerAuth{JWT: &DownstreamJWT{
			Issuer:   "https://issuer.example.com",
			JWKSURL:  "https://issuer.example.com/keys",
			Audience: "manifold",
		}}
	}
	s := baseValidServer()
	s.Auth = valid()
	require.NoError(t, s.ValidateWithContext(t.Context()))

	s = baseValidServer()
	s.Auth = &ServerAuth{}
	require.NoError(t, s.ValidateWithContext(t.Context()))

	s = baseValidServer()
	s.Auth = valid()
	s.Auth.JWT.Audience = ""
	require.NoError(t, s.ValidateWithContext(t.Context()), "audience is optional")

	for name, mutate := range map[string]func(*DownstreamJWT){
		"no issuer":       func(j *DownstreamJWT) { j.Issuer = "" },
		"no jwksURL":      func(j *DownstreamJWT) { j.JWKSURL = "" },
		"relative issuer": func(j *DownstreamJWT) { j.Issuer = "issuer.example.com" },
	} {
		t.Run(name, func(t *testing.T) {
			s := baseValidServer()
			s.Auth = valid()
			mutate(s.Auth.JWT)
			require.Error(t, s.ValidateWithContext(t.Context()))
		})
	}

	s = Server{
		Description: "d", Transport: MCPTransportReverse, Origin: "https://app.example.com",
		Identity: "p", Auth: valid(),
	}
	err := s.ValidateWithContext(context.WithValue(t.Context(), edgeContextKey{}, EdgeConfig{}))
	require.ErrorContains(t, err, "auth.jwt is not supported for the reverse transport")
}

// credentialsJSON は ${VAR} の環境変数展開で渡せる（秘密鍵の JSON をそのまま入れられる）。
func TestLoadInternal_GoogleServiceAccount_EnvExpansion(t *testing.T) {
	creds := testServiceAccountJSON(t)
	t.Setenv("TEST_GA4_SA_JSON", creds)
	cfg, err := loadFromYAML(t, `
mcpServers:
  ga4:
    description: ga4
    spec: https://example.com/openapi.yaml
    baseURL: https://analyticsdata.googleapis.com
    googleServiceAccount:
      credentialsJSON: ${TEST_GA4_SA_JSON}
      scopes:
        - https://www.googleapis.com/auth/analytics.readonly
    auth:
      jwt:
        issuer: https://issuer.example.com
        jwksURL: https://issuer.example.com/keys
        audience: manifold
`)
	require.NoError(t, err)
	srv := cfg.MCPServer["ga4"]
	require.Equal(t, creds, srv.GoogleServiceAccount.CredentialsJSON)
	require.Equal(
		t,
		[]string{"https://www.googleapis.com/auth/analytics.readonly"},
		srv.GoogleServiceAccount.Scopes,
	)
	require.Equal(t, "manifold", srv.Auth.JWT.Audience)
	require.Equal(t, "https://issuer.example.com/keys", srv.Auth.JWT.JWKSURL)
}
