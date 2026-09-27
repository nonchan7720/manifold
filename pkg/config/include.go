package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/compose-spec/compose-go/v2/template"
	"go.yaml.in/yaml/v3"
)

// includeKey is the top-level key listing extra config files to merge into
// the main config (similar to LiteLLM's include directive).
const includeKey = "include"

// includableKeys are the only top-level keys an included file may set.
var includableKeys = []string{"gateway"}

// loadWithIncludes reads the YAML config file at path and resolves its
// top-level include list, returning the merged document.
//
// Included files may only set the keys in includableKeys; anything else,
// including a nested include, is an error. They are merged in list order and
// the main file is merged last, so its own values win over anything it
// includes. Maps are merged recursively (keys compared case-insensitively, as
// viper does); any other value, including lists, is replaced as a whole.
// Include paths are resolved relative to the main file, may reference ${VAR}
// environment variables and may be glob patterns (matched files are merged in
// lexical order).
func loadWithIncludes(path string) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	doc, err := readYAMLFile(abs)
	if err != nil {
		return nil, err
	}
	includes, err := includePaths(doc, filepath.Dir(abs))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", abs, err)
	}
	delete(doc, includeKey)

	merged := map[string]any{}
	for _, inc := range includes {
		child, err := readYAMLFile(inc)
		if err != nil {
			return nil, err
		}
		for key := range child {
			if !isIncludableKey(key) {
				return nil, fmt.Errorf(
					"included config file %s: key %q is not allowed (allowed: %s)",
					inc, key, strings.Join(includableKeys, ", "),
				)
			}
		}
		mergeConfigMaps(merged, child)
	}
	mergeConfigMaps(merged, doc)
	return merged, nil
}

func isIncludableKey(key string) bool {
	for _, allowed := range includableKeys {
		if strings.EqualFold(key, allowed) {
			return true
		}
	}
	return false
}

func readYAMLFile(path string) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// The path comes from the operator's own config file, not from requests.
	raw, err := os.ReadFile(filepath.Clean(abs))
	if err != nil {
		return nil, fmt.Errorf("error reading config file %s: %w", abs, err)
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("error parsing config file %s: %w", abs, err)
	}
	if doc == nil {
		doc = map[string]any{}
	}
	return doc, nil
}

// includePaths returns the files referenced by doc's include key, resolved
// against baseDir.
func includePaths(doc map[string]any, baseDir string) ([]string, error) {
	value, ok := doc[includeKey]
	if !ok || value == nil {
		return nil, nil
	}
	var entries []any
	switch typed := value.(type) {
	case string:
		entries = []any{typed}
	case []any:
		entries = typed
	default:
		return nil, fmt.Errorf(
			"%s must be a string or a list of strings, got %T", includeKey, value,
		)
	}

	var paths []string
	for _, entry := range entries {
		pattern, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf("%s entries must be strings, got %T", includeKey, entry)
		}
		pattern, err := template.Substitute(pattern, os.LookupEnv)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(pattern) == "" {
			return nil, fmt.Errorf("%s entries must not be empty", includeKey)
		}
		if !filepath.IsAbs(pattern) {
			pattern = filepath.Join(baseDir, pattern)
		}
		if !hasGlobMeta(pattern) {
			paths = append(paths, pattern)
			continue
		}
		// filepath.Glob returns matches in lexical order.
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid %s pattern %q: %w", includeKey, pattern, err)
		}
		paths = append(paths, matches...)
	}
	return paths, nil
}

func hasGlobMeta(path string) bool {
	return strings.ContainsAny(path, `*?[`)
}

// mergeConfigMaps merges src into dst. Nested maps are merged recursively
// and every other value in src replaces the one in dst. Keys are matched
// case-insensitively because viper treats config keys that way; the key
// already present in dst keeps its spelling.
func mergeConfigMaps(dst, src map[string]any) {
	for key, srcVal := range src {
		dstKey := key
		for existing := range dst {
			if strings.EqualFold(existing, key) {
				dstKey = existing
				break
			}
		}
		srcMap, srcIsMap := srcVal.(map[string]any)
		dstMap, dstIsMap := dst[dstKey].(map[string]any)
		if srcIsMap && dstIsMap {
			mergeConfigMaps(dstMap, srcMap)
			continue
		}
		dst[dstKey] = srcVal
	}
}
