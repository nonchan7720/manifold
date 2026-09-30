package config

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
)

// Service は mcpServers / agents のエントリが属するサービス。1 つのサービスが
// 提供する複数の API 群（MCP サーバー・OpenAPI・A2A エージェント）を、同じ
// Code でまとめるために使う。
//
// Code はツール認可（authz）の判定 input に service として渡るため、ポリシーは
// サーバー名（トップキー）ではなくサービス単位でツールを許可できる。Name は
// UI などで表示するための名前で、判定には使わない。
type Service struct {
	Code string `mapstructure:"code"`
	Name string `mapstructure:"name"`
}

func (s Service) ValidateWithContext(ctx context.Context) error {
	return validation.ValidateStructWithContext(ctx, &s,
		validation.Field(&s.Code, validation.By(func(value any) error {
			v, _ := value.(string)
			if v != "" && !pathRegex.MatchString(v) {
				return fmt.Errorf("must contain only letters, digits, '_' and '-'")
			}
			return nil
		})),
		validation.Field(&s.Name, validation.By(func(value any) error {
			v, _ := value.(string)
			if v != "" && strings.TrimSpace(v) == "" {
				return fmt.Errorf("must not be blank")
			}
			return nil
		})),
	)
}

// ServiceCode はこのサーバーが属するサービスのコードを返す。service.code が
// 未設定ならサーバー名（mcpServers / agents のキー）をそのまま使うため、
// service を設定していない既存の設定ではサーバー 1 つが 1 サービスになる。
func (s Server) ServiceCode() string {
	if s.Service != nil && s.Service.Code != "" {
		return s.Service.Code
	}
	return s.Name
}

// ServiceName はこのサーバーが属するサービスの表示名を返す。service.name が
// 未設定なら ServiceCode を返す。
func (s Server) ServiceName() string {
	if s.Service != nil && s.Service.Name != "" {
		return s.Service.Name
	}
	return s.ServiceCode()
}

// validateServiceNames は同じサービスコードを持つサーバー同士で service.name が
// 食い違っていないことを検証する。サービス単位で表示名を 1 つに決められないと
// UI 上で同じサービスが別名で並んでしまうため、設定ミスとして拒否する。
// 表示名を省略したエントリは他のエントリの表示名に従うので衝突としない。
func validateServiceNames(servers Servers) error {
	names := map[string]string{}
	owners := map[string]string{}
	for _, key := range slices.Sorted(maps.Keys(servers)) {
		srv := servers[key]
		if srv == nil || srv.Service == nil || srv.Service.Name == "" {
			continue
		}
		code := srv.ServiceCode()
		if prev, ok := names[code]; ok && prev != srv.Service.Name {
			return fmt.Errorf(
				"service %q has conflicting names %q ('%s') and %q ('%s')",
				code, prev, owners[code], srv.Service.Name, key,
			)
		}
		names[code] = srv.Service.Name
		owners[code] = key
	}
	return nil
}

// resolveServiceNames は service.name を省略したサーバーに、同じサービスコードの
// 他のサーバーで設定された表示名を補う。validateServiceNames の後に呼ぶため、
// サービスごとの表示名は高々 1 つに決まっている。
func resolveServiceNames(servers Servers) {
	names := map[string]string{}
	for _, srv := range servers {
		if srv.Service != nil && srv.Service.Name != "" {
			names[srv.ServiceCode()] = srv.Service.Name
		}
	}
	for _, srv := range servers {
		name, ok := names[srv.ServiceCode()]
		if !ok || (srv.Service != nil && srv.Service.Name != "") {
			continue
		}
		svc := Service{Name: name}
		if srv.Service != nil {
			svc.Code = srv.Service.Code
		}
		srv.Service = &svc
	}
}
