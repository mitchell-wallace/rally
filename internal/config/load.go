package config

import (
	"errors"
	"fmt"
	"os"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/mitchell-wallace/rally/internal/store"
)

func V2Path(workspaceDir string) string {
	return store.ConfigPath(workspaceDir)
}

// LoadV2 loads the effective config for a workspace by layering the user-level
// config (~/.config/rally/config.toml, the base / main source of truth) under
// the repo-level .rally/config.toml (per-key overrides). A value set in the repo
// file wins over the same key in the user file; keys only the user file sets are
// inherited. When neither file exists, empty defaults are returned.
func LoadV2(workspaceDir string) (V2Config, error) {
	userData, err := readConfigFile(store.UserConfigPath())
	if err != nil {
		return V2Config{}, err
	}
	repoData, err := readConfigFile(V2Path(workspaceDir))
	if err != nil {
		return V2Config{}, err
	}
	if userData == nil && repoData == nil {
		return emptyV2Config(), nil
	}
	merged, err := mergeTOMLDocuments(userData, repoData)
	if err != nil {
		return V2Config{}, fmt.Errorf("merge user and repo config: %w", err)
	}
	return decodeV2(merged)
}

// LoadV2File loads a single config file with no user/repo layering. A missing
// file yields empty defaults. Used by the interactive editor and the init flows
// that read or write one specific file rather than the merged view.
func LoadV2File(path string) (V2Config, error) {
	data, err := readConfigFile(path)
	if err != nil {
		return V2Config{}, err
	}
	if data == nil {
		return emptyV2Config(), nil
	}
	return decodeV2(data)
}

// readConfigFile reads a config file, treating a missing file (or empty path) as
// absent rather than an error.
func readConfigFile(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return data, nil
}

func emptyV2Config() V2Config {
	return V2Config{
		Harnesses: make(map[string]*HarnessConfig),
		Routes:    make(map[string][]string),
		Reasoning: make(map[string]string),
	}
}

// mergeTOMLDocuments deep-merges two TOML documents with override winning over
// base. Sub-tables are merged recursively; scalars and arrays are replaced
// wholesale by override (so a repo [routes].junior replaces the user's junior
// route but leaves other roles inherited).
func mergeTOMLDocuments(base, override []byte) ([]byte, error) {
	baseMap := map[string]interface{}{}
	if len(base) > 0 {
		if err := toml.Unmarshal(base, &baseMap); err != nil {
			return nil, err
		}
	}
	overrideMap := map[string]interface{}{}
	if len(override) > 0 {
		if err := toml.Unmarshal(override, &overrideMap); err != nil {
			return nil, err
		}
	}
	return toml.Marshal(deepMergeMaps(baseMap, overrideMap))
}

func deepMergeMaps(base, override map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(base)+len(override))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range override {
		if bv, ok := out[k]; ok {
			if bm, bok := bv.(map[string]interface{}); bok {
				if om, ook := ov.(map[string]interface{}); ook {
					out[k] = deepMergeMaps(bm, om)
					continue
				}
			}
		}
		out[k] = ov
	}
	return out
}
