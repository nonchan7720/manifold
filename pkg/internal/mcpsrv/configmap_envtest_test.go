package mcpsrv

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nonchan7720/manifold/pkg/config"
	"github.com/nonchan7720/manifold/pkg/infrastructure/storage"
	"github.com/nonchan7720/manifold/pkg/internal/oastomcptool"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// envtestK8sVersion pins the kube-apiserver/etcd binary set fetched by
// `setup-envtest use`. Must match the version the Makefile's test target
// fetches (see Makefile), since both must resolve to the same on-disk
// directory.
const envtestK8sVersion = "1.36.2"

// envtestAdmin holds the shared kube-apiserver started once for this package
// by TestMain.
var envtestAdmin struct {
	env    *envtest.Environment
	config *rest.Config
}

// envtestBinaryAssetsDirectory returns the directory setup-envtest stores
// envtestK8sVersion's binaries in, so Environment.Start can find them
// without KUBEBUILDER_ASSETS being set. KUBEBUILDER_ASSETS, when set, still
// wins: process.BinPathFinder checks it before BinaryAssetsDirectory.
func envtestBinaryAssetsDirectory() (string, error) {
	base, err := envtest.SetupEnvtestDefaultBinaryAssetsDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(
		base,
		fmt.Sprintf("%s-%s-%s", envtestK8sVersion, runtime.GOOS, runtime.GOARCH),
	), nil
}

// TestMain starts a single envtest kube-apiserver/etcd for the whole package
// instead of one per test, since each instance takes seconds to boot.
func TestMain(m *testing.M) {
	assetsDir, err := envtestBinaryAssetsDirectory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve envtest binary assets directory:", err)
		os.Exit(1)
	}

	env := &envtest.Environment{BinaryAssetsDirectory: assetsDir}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"start envtest environment: %v\n"+
				"fetch the envtest binaries first: setup-envtest use %s\n",
			err, envtestK8sVersion)
		os.Exit(1)
	}
	envtestAdmin.env = env
	envtestAdmin.config = cfg

	code := m.Run()

	if err := env.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "stop envtest environment:", err)
	}
	os.Exit(code)
}

// newEnvtestAdminClientset returns a clientset authenticated as the envtest
// cluster-admin. TestMain has already verified the cluster is up (Makefile's
// test target fetches envtestK8sVersion's binaries first) by the time any
// test runs.
func newEnvtestAdminClientset(t *testing.T) kubernetes.Interface {
	t.Helper()
	cs, err := kubernetes.NewForConfig(envtestAdmin.config)
	require.NoError(t, err)
	return cs
}

// createEnvtestNamespace creates a namespace scoped to a single test so that
// ConfigMap/RBAC objects across tests never collide, and deletes it on
// cleanup.
func createEnvtestNamespace(t *testing.T, cs kubernetes.Interface) string {
	t.Helper()
	name := fmt.Sprintf("mcpsrv-envtest-%d", time.Now().UnixNano())
	_, err := cs.CoreV1().Namespaces().Create(t.Context(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = cs.CoreV1().Namespaces().Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return name
}

func createEnvtestConfigMap(
	t *testing.T, cs kubernetes.Interface, namespace, name string, data map[string]string,
) {
	t.Helper()
	_, err := cs.CoreV1().ConfigMaps(namespace).Create(t.Context(), &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Data:       data,
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

// configMapServers builds a single-server config.Servers pointing at
// configmap://<namespace>/<name>/<key>, matching newRefreshTestMCPServer's
// shape for the http(s) case in spec_refresh_test.go.
func configMapServers(namespace, name, key string) config.Servers {
	return config.Servers{
		"api": &config.Server{
			Name:        "api",
			Description: "test api",
			Spec:        fmt.Sprintf("configmap://%s/%s/%s", namespace, name, key),
			BaseURL:     "https://example.com",
		},
	}
}

func newConfigMapMCPServer(t *testing.T, servers config.Servers) *MCPServer {
	t.Helper()
	u, _ := url.Parse("https://example.com")
	s := NewMCPServer(servers, storage.NewContentManagementService(u, storage.NewNoopUploader()))
	require.NoError(t, s.Init(t.Context()))
	return s
}

func TestMCPServer_Init_ConfigMapSpec_RegistersTools(t *testing.T) {
	cs := newEnvtestAdminClientset(t)
	ns := createEnvtestNamespace(t, cs)
	createEnvtestConfigMap(t, cs, ns, "api-spec", map[string]string{
		"openapi.json": specWithOperations("ping"),
	})
	oastomcptool.SetConfigMapClientset(cs)
	t.Cleanup(func() { oastomcptool.SetConfigMapClientset(nil) })

	s := newConfigMapMCPServer(t, configMapServers(ns, "api-spec", "openapi.json"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
}

func TestMCPServer_Init_ConfigMapSpec_MissingConfigMap_StartsWithNoTools(t *testing.T) {
	cs := newEnvtestAdminClientset(t)
	ns := createEnvtestNamespace(t, cs)
	oastomcptool.SetConfigMapClientset(cs)
	t.Cleanup(func() { oastomcptool.SetConfigMapClientset(nil) })

	s := newConfigMapMCPServer(t, configMapServers(ns, "api-spec", "openapi.json"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.Empty(t, listToolNames(t, srv))

	catalog, err := s.ToolCatalog(t.Context(), "api")
	require.NoError(t, err)
	require.Empty(t, catalog)

	// Creating the ConfigMap after startup lets the regular refresh cycle
	// pick up the spec, the same recovery path as an unreachable HTTP spec.
	createEnvtestConfigMap(t, cs, ns, "api-spec", map[string]string{
		"openapi.json": specWithOperations("ping"),
	})

	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
}

func TestMCPServer_Init_ConfigMapSpec_MissingKey_StartsWithNoTools(t *testing.T) {
	cs := newEnvtestAdminClientset(t)
	ns := createEnvtestNamespace(t, cs)
	createEnvtestConfigMap(t, cs, ns, "api-spec", map[string]string{
		"other-key": specWithOperations("ping"),
	})
	oastomcptool.SetConfigMapClientset(cs)
	t.Cleanup(func() { oastomcptool.SetConfigMapClientset(nil) })

	s := newConfigMapMCPServer(t, configMapServers(ns, "api-spec", "openapi.json"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.Empty(t, listToolNames(t, srv))

	// Adding the missing key lets the regular refresh cycle pick up the spec.
	cm, err := cs.CoreV1().ConfigMaps(ns).Get(t.Context(), "api-spec", metav1.GetOptions{})
	require.NoError(t, err)
	cm.Data["openapi.json"] = specWithOperations("ping")
	_, err = cs.CoreV1().ConfigMaps(ns).Update(t.Context(), cm, metav1.UpdateOptions{})
	require.NoError(t, err)

	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)
	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
}

func TestMCPServer_Init_ConfigMapSpec_RBAC_ForbiddenUntilGranted(t *testing.T) {
	cs := newEnvtestAdminClientset(t)
	ns := createEnvtestNamespace(t, cs)
	createEnvtestConfigMap(t, cs, ns, "api-spec", map[string]string{
		"openapi.json": specWithOperations("ping"),
	})

	authUser, err := envtestAdmin.env.AddUser(
		envtest.User{Name: "restricted-reader"}, envtestAdmin.config,
	)
	require.NoError(t, err)
	restrictedClientset, err := kubernetes.NewForConfig(authUser.Config())
	require.NoError(t, err)

	oastomcptool.SetConfigMapClientset(restrictedClientset)
	t.Cleanup(func() { oastomcptool.SetConfigMapClientset(nil) })

	s := newConfigMapMCPServer(t, configMapServers(ns, "api-spec", "openapi.json"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.Empty(t, listToolNames(t, srv), "no RBAC grant yet; the get must be forbidden")

	_, err = cs.RbacV1().Roles(ns).Create(t.Context(), &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "configmap-reader", Namespace: ns},
		Rules: []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{"configmaps"}, Verbs: []string{"get"}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	_, err = cs.RbacV1().RoleBindings(ns).Create(t.Context(), &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "configmap-reader-binding", Namespace: ns},
		Subjects:   []rbacv1.Subject{{Kind: rbacv1.UserKind, Name: "restricted-reader"}},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName, Kind: "Role", Name: "configmap-reader",
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// The apiserver's RBAC authorizer syncs off a watch, so a RoleBinding can
	// take a moment to take effect; poll instead of a single refreshServer call.
	require.Eventually(t, func() bool {
		changed, err := s.refreshServer(t.Context(), "api")
		return err == nil && changed
	}, 10*time.Second, 100*time.Millisecond, "refreshServer never succeeded after granting RBAC")

	require.ElementsMatch(t, []string{"ping"}, listToolNames(t, srv))
}

func TestMCPServer_RefreshServer_ConfigMapSpec_UpdatedContent_AddsAndRemovesTools(t *testing.T) {
	cs := newEnvtestAdminClientset(t)
	ns := createEnvtestNamespace(t, cs)
	createEnvtestConfigMap(t, cs, ns, "api-spec", map[string]string{
		"openapi.json": specWithOperations("ping", "pong"),
	})
	oastomcptool.SetConfigMapClientset(cs)
	t.Cleanup(func() { oastomcptool.SetConfigMapClientset(nil) })

	s := newConfigMapMCPServer(t, configMapServers(ns, "api-spec", "openapi.json"))

	srv, err := s.Server("api")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"ping", "pong"}, listToolNames(t, srv))

	cm, err := cs.CoreV1().ConfigMaps(ns).Get(t.Context(), "api-spec", metav1.GetOptions{})
	require.NoError(t, err)
	cm.Data["openapi.json"] = specWithOperations("ping", "newop")
	_, err = cs.CoreV1().ConfigMaps(ns).Update(t.Context(), cm, metav1.UpdateOptions{})
	require.NoError(t, err)

	changed, err := s.refreshServer(t.Context(), "api")
	require.NoError(t, err)
	require.True(t, changed)
	require.ElementsMatch(t, []string{"ping", "newop"}, listToolNames(t, srv))
}
