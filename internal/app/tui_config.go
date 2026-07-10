package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mitchell-wallace/rally/internal/config"
	"github.com/mitchell-wallace/rally/internal/roles"
	"github.com/mitchell-wallace/rally/internal/store"
)

type TUIConfigMutationKind string

const (
	TUIConfigSetRoute            TUIConfigMutationKind = "set_route"
	TUIConfigSetReasoning        TUIConfigMutationKind = "set_reasoning"
	TUIConfigSetProviderDisabled TUIConfigMutationKind = "set_provider_disabled"
)

type TUIConfigMutation struct {
	Kind     TUIConfigMutationKind
	Role     string
	Route    []string
	Value    string
	Provider string
	Disabled bool
}

type TUIRoleConfig struct {
	Name      string
	Route     []string
	Reasoning string
	BuiltIn   bool
}

type TUIProviderConfig struct {
	Name        string
	Disabled    bool
	MemberCount int
}

type TUIConfigSnapshot struct {
	Path       string
	Roles      []TUIRoleConfig
	Providers  []TUIProviderConfig
	Shorthands []string
}

// TUIConfigService owns the application use case for the TUI's machine-scope
// routing editor. Presentation receives only mapped snapshots and mutations.
type TUIConfigService struct {
	path string
}

func NewTUIConfigService() (*TUIConfigService, error) {
	path := store.UserConfigPath()
	if path == "" {
		return nil, errors.New("resolve machine config path")
	}
	return NewTUIConfigServiceAt(path), nil
}

func NewTUIConfigServiceAt(path string) *TUIConfigService {
	return &TUIConfigService{path: path}
}

func (s *TUIConfigService) Snapshot(ctx context.Context) (TUIConfigSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return TUIConfigSnapshot{}, err
	}
	cfg, err := config.LoadV2File(s.path)
	if err != nil {
		return TUIConfigSnapshot{}, fmt.Errorf("load machine config: %w", err)
	}
	counts, err := cfg.ProviderMemberCounts()
	if err != nil {
		return TUIConfigSnapshot{}, fmt.Errorf("resolve providers: %w", err)
	}
	return buildTUIConfigSnapshot(s.path, cfg, counts), nil
}

func (s *TUIConfigService) Apply(ctx context.Context, mutation TUIConfigMutation) (TUIConfigSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return TUIConfigSnapshot{}, err
	}
	var err error
	switch mutation.Kind {
	case TUIConfigSetRoute:
		err = config.SetRouteFile(s.path, mutation.Role, mutation.Route)
	case TUIConfigSetReasoning:
		err = config.SetReasoningFile(s.path, mutation.Role, mutation.Value)
	case TUIConfigSetProviderDisabled:
		err = config.SetProviderDisabledFile(s.path, mutation.Provider, mutation.Disabled)
	default:
		err = fmt.Errorf("unknown TUI config mutation %q", mutation.Kind)
	}
	if err != nil {
		return TUIConfigSnapshot{}, err
	}
	return s.Snapshot(ctx)
}

func buildTUIConfigSnapshot(path string, cfg config.V2Config, counts map[string]int) TUIConfigSnapshot {
	builtinNames := []string{"default"}
	for _, spec := range roles.Builtins() {
		builtinNames = append(builtinNames, spec.Name)
	}
	builtinSet := make(map[string]bool, len(builtinNames))
	for _, name := range builtinNames {
		builtinSet[name] = true
	}
	customSet := map[string]bool{}
	for name := range cfg.Routes {
		if !builtinSet[name] {
			customSet[name] = true
		}
	}
	for name := range cfg.Reasoning {
		if !builtinSet[name] {
			customSet[name] = true
		}
	}
	custom := sortedBoolKeys(customSet)
	names := append(append([]string(nil), builtinNames...), custom...)
	roleConfigs := make([]TUIRoleConfig, 0, len(names))
	for _, name := range names {
		roleConfigs = append(roleConfigs, TUIRoleConfig{
			Name:      name,
			Route:     append([]string(nil), cfg.Routes[name]...),
			Reasoning: cfg.Reasoning[name],
			BuiltIn:   builtinSet[name],
		})
	}

	providerNames := make([]string, 0, len(cfg.Providers))
	for name := range cfg.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	providers := make([]TUIProviderConfig, 0, len(providerNames))
	for _, name := range providerNames {
		providers = append(providers, TUIProviderConfig{
			Name:        name,
			Disabled:    cfg.Providers[name].Disabled,
			MemberCount: counts[name],
		})
	}

	return TUIConfigSnapshot{
		Path:       path,
		Roles:      roleConfigs,
		Providers:  providers,
		Shorthands: routeShorthands(cfg),
	}
}

func routeShorthands(cfg config.V2Config) []string {
	set := map[string]bool{"ag": true, "cl": true, "cx": true, "op": true}
	for harness, hc := range cfg.Harnesses {
		if strings.TrimSpace(harness) == "" {
			continue
		}
		set[harness] = true
		if hc == nil {
			continue
		}
		for alias := range hc.Models {
			set[harness+":"+alias] = true
		}
	}
	for _, route := range cfg.Routes {
		for _, entry := range route {
			if strings.TrimSpace(entry) != "" {
				set[entry] = true
			}
		}
	}
	return sortedBoolKeys(set)
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
