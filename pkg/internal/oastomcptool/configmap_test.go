package oastomcptool

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// resetConfigMapClientset clears the package-level clientset override/cache
// so tests never leak state into one another.
func resetConfigMapClientset(t *testing.T) {
	t.Helper()
	SetConfigMapClientset(nil)
	t.Cleanup(func() { SetConfigMapClientset(nil) })
}

// --- parseConfigMapSpecPath: configmap://<namespace>/<name>/<key> ---

func TestParseConfigMapSpecPath_Valid(t *testing.T) {
	namespace, name, key, err := parseConfigMapSpecPath("configmap://default/my-specs/openapi.json")
	require.NoError(t, err)
	require.Equal(t, "default", namespace)
	require.Equal(t, "my-specs", name)
	require.Equal(t, "openapi.json", key)
}

func TestParseConfigMapSpecPath_InvalidFormat(t *testing.T) {
	tests := []struct {
		name     string
		specPath string
	}{
		{"missing key", "configmap://default/my-specs"},
		{"missing name and key", "configmap://default"},
		{"empty namespace", "configmap:///my-specs/openapi.json"},
		{"too many segments", "configmap://default/my-specs/nested/openapi.json"},
		{"trailing slash", "configmap://default/my-specs/openapi.json/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, _, err := parseConfigMapSpecPath(tt.specPath)
			require.Error(t, err)
		})
	}
}

// --- fetchConfigMapSpecBytes ---

func TestFetchConfigMapSpecBytes_ReturnsDataKey(t *testing.T) {
	resetConfigMapClientset(t)
	SetConfigMapClientset(fake.NewClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-specs", Namespace: "default"},
		Data:       map[string]string{"openapi.json": inlineOpenAPI3Spec},
	}))

	got, err := fetchConfigMapSpecBytes(
		context.Background(),
		"configmap://default/my-specs/openapi.json",
	)
	require.NoError(t, err)
	require.Equal(t, inlineOpenAPI3Spec, string(got))
}

func TestFetchConfigMapSpecBytes_ConfigMapNotFound(t *testing.T) {
	resetConfigMapClientset(t)
	SetConfigMapClientset(fake.NewClientset())

	_, err := fetchConfigMapSpecBytes(
		context.Background(),
		"configmap://default/missing/openapi.json",
	)
	require.Error(t, err)
}

func TestFetchConfigMapSpecBytes_KeyNotFound(t *testing.T) {
	resetConfigMapClientset(t)
	SetConfigMapClientset(fake.NewClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-specs", Namespace: "default"},
		Data:       map[string]string{"other-key": inlineOpenAPI3Spec},
	}))

	_, err := fetchConfigMapSpecBytes(
		context.Background(),
		"configmap://default/my-specs/openapi.json",
	)
	require.Error(t, err)
}

func TestFetchConfigMapSpecBytes_InvalidSpecPath(t *testing.T) {
	resetConfigMapClientset(t)
	SetConfigMapClientset(fake.NewClientset())

	_, err := fetchConfigMapSpecBytes(context.Background(), "configmap://default/my-specs")
	require.Error(t, err)
}

// --- FetchSpecBytes: configmap:// dispatch ---

func TestFetchSpecBytes_ConfigMapPrefix_DelegatesToConfigMapFetch(t *testing.T) {
	resetConfigMapClientset(t)
	SetConfigMapClientset(fake.NewClientset(&corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "my-specs", Namespace: "default"},
		Data:       map[string]string{"openapi.json": inlineOpenAPI3Spec},
	}))

	got, err := FetchSpecBytes(context.Background(), "configmap://default/my-specs/openapi.json")
	require.NoError(t, err)
	require.Equal(t, inlineOpenAPI3Spec, string(got))
}

func TestFetchSpecBytes_ConfigMapPrefix_InvalidFormat(t *testing.T) {
	resetConfigMapClientset(t)
	_, err := FetchSpecBytes(context.Background(), "configmap://default/my-specs")
	require.Error(t, err)
}

// --- getConfigMapClientset ---

func TestGetConfigMapClientset_NoOverride_BuildsFromInClusterConfig(t *testing.T) {
	resetConfigMapClientset(t)
	// Outside a cluster, in-cluster config building fails; this must surface
	// as a plain error, not a panic, and must not silently fall back to any
	// other clientset.
	_, err := getConfigMapClientset()
	require.Error(t, err)
}

func TestGetConfigMapClientset_Override_ReturnsOverride(t *testing.T) {
	resetConfigMapClientset(t)
	cs := fake.NewClientset()
	SetConfigMapClientset(cs)

	got, err := getConfigMapClientset()
	require.NoError(t, err)
	require.Same(t, cs, got)
}

func TestGetConfigMapClientset_CachesBuiltClientset(t *testing.T) {
	resetConfigMapClientset(t)
	cs := fake.NewClientset()
	SetConfigMapClientset(cs)

	got1, err := getConfigMapClientset()
	require.NoError(t, err)
	got2, err := getConfigMapClientset()
	require.NoError(t, err)
	require.Same(t, got1, got2)
}

// --- OpenAPI3 location / base URL derivation must not break for configmap:// ---

func TestLoadOpenAPI3SpecFromData_ConfigMapSpecPath_ParsesWithoutError(t *testing.T) {
	spec, err := LoadOpenAPI3SpecFromData(
		[]byte(inlineOpenAPI3Spec), "configmap://default/my-specs/openapi.json",
	)
	require.NoError(t, err)
	require.NotNil(t, spec)
}

func TestGetBaseUrlFromOpenAPI3_ConfigMapSpecPath_NoServers_ReturnsEmpty(t *testing.T) {
	spec, err := LoadOpenAPI3SpecFromData(
		[]byte(inlineOpenAPI3Spec), "configmap://default/my-specs/openapi.json",
	)
	require.NoError(t, err)

	got := GetBaseUrlFromOpenAPI3(
		context.Background(), spec, "configmap://default/my-specs/openapi.json",
	)
	require.Empty(t, got)
}
