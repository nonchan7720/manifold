package env

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// テストプロセスは実際の LOCAL/CI/TEST 環境変数を継承している可能性があるため、
// 各テストケースで LOCAL/CI/TEST 全てを明示的に t.Setenv して hermetic にする
// （t.Setenv はテスト終了時に自動で元の値へ復元する）。
// 「未設定」ケースは t.Setenv(key, "") で表現する。os.Getenv は未設定の変数に対しても
// 空文字列を返すため、strconv.ParseBool("") の挙動という観点では未設定と等価になる。

func TestEnvBool(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "invalid value", value: "yes", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const key = "MANIFOLD_TEST_ENV_BOOL"
			t.Setenv(key, tt.value)
			require.Equal(t, tt.want, envBool(key))
		})
	}
}

func TestIsLocal(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "invalid value", value: "yes", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("CI", "false")
			t.Setenv("TEST", "false")
			t.Setenv("LOCAL", tt.value)
			require.Equal(t, tt.want, IsLocal())
		})
	}
}

func TestIsCI(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "invalid value", value: "yes", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOCAL", "false")
			t.Setenv("TEST", "false")
			t.Setenv("CI", tt.value)
			require.Equal(t, tt.want, IsCI())
		})
	}
}

func TestIsTest(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "unset", value: "", want: false},
		{name: "true", value: "true", want: true},
		{name: "false", value: "false", want: false},
		{name: "invalid value", value: "yes", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOCAL", "false")
			t.Setenv("CI", "false")
			t.Setenv("TEST", tt.value)
			require.Equal(t, tt.want, IsTest())
		})
	}
}

func TestIsLocalOrCIOrTest(t *testing.T) {
	tests := []struct {
		name  string
		local string
		ci    string
		test  string
		want  bool
	}{
		{name: "all unset/false", local: "", ci: "", test: "", want: false},
		{name: "only LOCAL true", local: "true", ci: "false", test: "false", want: true},
		{name: "only CI true", local: "false", ci: "true", test: "false", want: true},
		{name: "only TEST true", local: "false", ci: "false", test: "true", want: true},
		{name: "all true", local: "true", ci: "true", test: "true", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("LOCAL", tt.local)
			t.Setenv("CI", tt.ci)
			t.Setenv("TEST", tt.test)
			require.Equal(t, tt.want, IsLocalOrCIOrTest())
		})
	}
}
