package oastomcptool

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/nonchan7720/manifold/pkg/internal/client"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// configMapSpecPrefix marks a spec path resolved from a Kubernetes
// ConfigMap's data, in the form configmap://<namespace>/<name>/<key>.
const configMapSpecPrefix = "configmap://"

var (
	configMapClientsetMu sync.Mutex
	configMapClientset   kubernetes.Interface
)

// SetConfigMapClientset installs cs as the clientset used to fetch
// ConfigMap-backed specs, bypassing in-cluster config discovery. Pass nil to
// clear it, so the next fetch builds one from in-cluster config again.
// Intended for tests, which inject k8s.io/client-go/kubernetes/fake.
func SetConfigMapClientset(cs kubernetes.Interface) {
	configMapClientsetMu.Lock()
	defer configMapClientsetMu.Unlock()
	configMapClientset = cs
}

// applyConfigMapClientTimeout bounds cfg's requests to the same timeout as
// the http(s) spec path (client.HTTPClient()), so a stuck apiserver cannot
// block a ConfigMap fetch forever.
func applyConfigMapClientTimeout(cfg *rest.Config) {
	cfg.Timeout = client.HTTPClient().Timeout
}

// getConfigMapClientset returns the clientset used to fetch ConfigMap-backed
// specs, building it from in-cluster config on first use and caching it for
// subsequent calls.
func getConfigMapClientset() (kubernetes.Interface, error) {
	configMapClientsetMu.Lock()
	defer configMapClientsetMu.Unlock()
	if configMapClientset != nil {
		return configMapClientset, nil
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("build in-cluster kubernetes config: %w", err)
	}
	applyConfigMapClientTimeout(cfg)
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build kubernetes clientset: %w", err)
	}
	configMapClientset = cs
	return configMapClientset, nil
}

// parseConfigMapSpecPath splits specPath (configmap://<namespace>/<name>/<key>)
// into its namespace, ConfigMap name, and data key.
func parseConfigMapSpecPath(specPath string) (namespace, name, key string, err error) {
	trimmed := strings.TrimPrefix(specPath, configMapSpecPrefix)
	parts := strings.Split(trimmed, "/")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", fmt.Errorf(
			"invalid configmap spec path %q: expected configmap://<namespace>/<name>/<key>",
			specPath,
		)
	}
	return parts[0], parts[1], parts[2], nil
}

// fetchConfigMapSpecBytes fetches data[key] of the Kubernetes ConfigMap
// referenced by specPath (configmap://<namespace>/<name>/<key>).
func fetchConfigMapSpecBytes(ctx context.Context, specPath string) ([]byte, error) {
	namespace, name, key, err := parseConfigMapSpecPath(specPath)
	if err != nil {
		return nil, err
	}
	cs, err := getConfigMapClientset()
	if err != nil {
		return nil, err
	}
	cm, err := cs.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get configmap %s/%s: %w", namespace, name, err)
	}
	data, ok := cm.Data[key]
	if !ok {
		return nil, fmt.Errorf("key %q not found in configmap %s/%s", key, namespace, name)
	}
	return []byte(data), nil
}
