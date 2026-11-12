package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"

	"go.yaml.in/yaml/v3"
)

// envRef は設定ファイルの値の中の ${VAR} / ${VAR:-default}。
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expandEnv は s の ${VAR} / ${VAR:-default} を環境変数の値に置き換える。既定値の無い
// 未設定の変数はエラーにする（秘匿値の指定漏れを、空の値のまま起動して見逃さない）。
func expandEnv(s string) (string, error) {
	var missing string
	out := envRef.ReplaceAllStringFunc(s, func(ref string) string {
		m := envRef.FindStringSubmatch(ref)
		if value, ok := os.LookupEnv(m[1]); ok {
			return value
		}
		if m[2] != "" {
			return m[3]
		}
		missing = m[1]
		return ""
	})
	if missing != "" {
		return "", fmt.Errorf("environment variable %s is not set", missing)
	}
	return out, nil
}

// applyConfigFile は YAML の設定ファイル path を fs に反映する。
//
// キーはフラグの名前（先頭の -- を除いたもの）。繰り返せるフラグは YAML のリストで、
// name=value のフラグは YAML のマップで書く。値の中の ${VAR} / ${VAR:-default} は
// 環境変数に置き換える。コマンドラインで指定したフラグ（explicit）は、ファイルより優先する。
func applyConfigFile(fs *flag.FlagSet, path string, explicit map[string]bool) error {
	raw, err := os.ReadFile(path) //nolint: gosec // 運用者が指定した設定ファイル
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	names := make([]string, 0, len(doc))
	for name := range doc {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		f := fs.Lookup(name)
		if f == nil || name == configFlag {
			return fmt.Errorf("config: unknown setting %q", name)
		}
		if explicit[name] {
			continue
		}
		values, err := configValues(f, doc[name])
		if err != nil {
			return fmt.Errorf("config: %s: %w", name, err)
		}
		for _, value := range values {
			// 値は秘匿値を含みうるので、エラー文には出さない。
			if fs.Set(name, value) != nil {
				return fmt.Errorf("config: %s: invalid value", name)
			}
		}
	}
	return nil
}

// configValues は、設定ファイルの値 v を、フラグ f に Set する文字列の列にする。
func configValues(f *flag.Flag, v any) ([]string, error) {
	scalar := func(v any) (string, error) {
		switch v := v.(type) {
		case string:
			return expandEnv(v)
		case int, int64, float64, bool:
			return fmt.Sprint(v), nil
		default:
			return "", fmt.Errorf("must be a string, a number or a boolean")
		}
	}
	switch v := v.(type) {
	case []any:
		if _, ok := f.Value.(*stringList); !ok {
			return nil, fmt.Errorf("does not take a list")
		}
		values := make([]string, 0, len(v))
		for _, item := range v {
			value, err := scalar(item)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case map[string]any:
		if _, ok := f.Value.(keyValues); !ok {
			return nil, fmt.Errorf("does not take a map")
		}
		values := make([]string, 0, len(v))
		for key, item := range v {
			value, err := scalar(item)
			if err != nil {
				return nil, err
			}
			values = append(values, key+"="+value)
		}
		return values, nil
	case nil:
		return nil, nil
	default:
		value, err := scalar(v)
		if err != nil {
			return nil, err
		}
		return []string{value}, nil
	}
}
