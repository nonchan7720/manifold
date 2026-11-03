// Package storetest は store.Client の実装が共通で満たすべき契約のテストスイートを提供する。
// 各実装（memory / redis / sqlite）のテストは Run を呼び、実装固有の振る舞いだけを
// 自分のパッケージでテストする。
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/infrastructure/store"
	"github.com/stretchr/testify/require"
)

// Options は実装ごとに異なるケースの有無と条件を指定する。ゼロ値は、どのオプションケースも
// 走らせない最小の契約（全実装が共通に検証しているケース）を意味する。
type Options struct {
	// NegativeTTLExpires は、負の TTL で Set したキーを期限切れとして扱う実装で true にする。
	// true のとき Get_Expired / Expire_Expired を走らせる。go-redis は負の TTL を
	// 「期限なし」の SET として送るため、redis では false のままにする。
	NegativeTTLExpires bool

	// ValueTypes が true のとき、string / []byte の値が文字列として往復することを確かめる
	// Set_ValueTypes を走らせる。
	ValueTypes bool

	// NotFoundMessage が true のとき、Get_NotFound でエラー文に "key not found" が
	// 含まれることも確かめる。
	NotFoundMessage bool

	// ExpireTTL は Expire_UpdatesTTLWithoutChangingValue で使う TTL を指定する。
	// ゼロ値のときは defaultExpireTTL を使う。
	ExpireTTL ExpireTTL
}

// ExpireTTL は Expire_UpdatesTTLWithoutChangingValue の TTL と待ち時間。
type ExpireTTL struct {
	// Initial は最初の Set に渡す TTL。
	Initial time.Duration
	// Extended は Expire で更新する TTL。
	Extended time.Duration
	// Wait は Expire の後、Get の前に待つ時間。0 なら待たない。
	// Initial が切れた後も値が残ることまで確かめたい実装は、Initial より長い値を指定する。
	Wait time.Duration
}

// defaultExpireTTL は Options.ExpireTTL がゼロ値のときに使う TTL。待たずに Get するので、
// Expire が値を変えないことだけを確かめる（TTL の精度が秒単位の実装でも成り立つ）。
var defaultExpireTTL = ExpireTTL{Initial: time.Minute, Extended: time.Hour}

// Run は store.Client の共通契約をサブテストとして実行する。newClient はサブテストごとに
// 呼ばれ、他のケースと状態を共有しない新しいクライアントを返す（Close は newClient 側で
// t.Cleanup に登録する）。
func Run(t *testing.T, newClient func(t *testing.T) store.Client, opts Options) {
	t.Helper()
	expireTTL := opts.ExpireTTL
	if expireTTL == (ExpireTTL{}) {
		expireTTL = defaultExpireTTL
	}

	t.Run("SetAndGet", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)

		err := c.Set(ctx, "key1", "value1", time.Minute)
		require.NoError(t, err)

		got, err := c.Get(ctx, "key1")
		require.NoError(t, err)
		require.Equal(t, "value1", got)
	})

	t.Run("Get_NotFound", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)

		_, err := c.Get(ctx, "nonexistent")
		require.Error(t, err)
		if opts.NotFoundMessage {
			require.Contains(t, err.Error(), "key not found")
		}
		require.True(t, errors.Is(err, store.ErrNotFound),
			"a missing key must be distinguishable from an unexpected backend failure")
	})

	if opts.NegativeTTLExpires {
		t.Run("Get_Expired", func(t *testing.T) {
			ctx := t.Context()
			c := newClient(t)

			// 過去に期限切れになるTTLで保存
			err := c.Set(ctx, "expiredkey", "val", -time.Second)
			require.NoError(t, err)

			_, err = c.Get(ctx, "expiredkey")
			require.Error(t, err)
			require.Contains(t, err.Error(), "key not found")
			require.True(t, errors.Is(err, store.ErrNotFound))
		})
	}

	t.Run("Set_Overwrite", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)

		require.NoError(t, c.Set(ctx, "k", "first", time.Minute))
		require.NoError(t, c.Set(ctx, "k", "second", time.Minute))

		got, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, "second", got)
	})

	t.Run("Del", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)

		require.NoError(t, c.Set(ctx, "delkey", "val", time.Minute))
		require.NoError(t, c.Del(ctx, "delkey"))

		_, err := c.Get(ctx, "delkey")
		require.Error(t, err)
	})

	t.Run("Del_NotExisting", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)
		// 存在しないキーの削除はエラーにならない
		require.NoError(t, c.Del(ctx, "ghost"))
	})

	t.Run("Expire_UpdatesTTLWithoutChangingValue", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)

		require.NoError(t, c.Set(ctx, "k", "value1", expireTTL.Initial))
		require.NoError(t, c.Expire(ctx, "k", expireTTL.Extended))

		if expireTTL.Wait > 0 {
			time.Sleep(expireTTL.Wait)
		}
		got, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, "value1", got, "Expire must not change the stored value")
	})

	t.Run("Expire_NotFound", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)

		err := c.Expire(ctx, "nonexistent", time.Minute)
		require.Error(t, err)
		require.Contains(t, err.Error(), "key not found")
	})

	if opts.NegativeTTLExpires {
		t.Run("Expire_Expired", func(t *testing.T) {
			ctx := t.Context()
			c := newClient(t)

			require.NoError(t, c.Set(ctx, "k", "val", -time.Second))
			err := c.Expire(ctx, "k", time.Minute)
			require.Error(t, err)
			require.Contains(t, err.Error(), "key not found")
		})
	}

	if opts.ValueTypes {
		t.Run("Set_ValueTypes", func(t *testing.T) {
			runValueTypes(t, newClient)
		})
	}

	t.Run("ImplementsStoreClient", func(t *testing.T) {
		c := newClient(t)
		// newClient の戻り値型が store.Client なので、各実装がインターフェースを満たすことは
		// 呼び出し側のコンパイル時に保証される。ここでは返った値が使えることを確かめる。
		var _ interface {
			Set(ctx context.Context, key string, value any, expiration time.Duration) error
			Get(ctx context.Context, key string) (string, error)
			Del(ctx context.Context, key string) error
			Expire(ctx context.Context, key string, expiration time.Duration) error
			Close() error
		} = c
	})
}

func runValueTypes(t *testing.T, newClient func(t *testing.T) store.Client) {
	t.Helper()

	t.Run("string", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)
		require.NoError(t, c.Set(ctx, "k", "hello", time.Minute))
		got, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, "hello", got)
	})

	t.Run("[]byte JSON", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)
		// []byte を渡した場合、数値列([123 34...])ではなく文字列として保存される
		raw := []byte(`{"token":"abc","expires":3600}`)
		require.NoError(t, c.Set(ctx, "k", raw, time.Minute))
		got, err := c.Get(ctx, "k")
		require.NoError(t, err)
		require.Equal(t, string(raw), got)
	})

	t.Run("[]byte roundtrip via json.Unmarshal", func(t *testing.T) {
		ctx := t.Context()
		c := newClient(t)
		type payload struct {
			Token   string `json:"token"`
			Expires int    `json:"expires"`
		}
		in := payload{Token: "abc", Expires: 3600}
		raw, err := json.Marshal(in)
		require.NoError(t, err)

		require.NoError(t, c.Set(ctx, "k", raw, time.Minute))

		got, err := c.Get(ctx, "k")
		require.NoError(t, err)

		var out payload
		require.NoError(t, json.Unmarshal([]byte(got), &out))
		require.Equal(t, in, out)
	})
}
