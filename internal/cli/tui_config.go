package cli

import (
	"context"

	"github.com/mitchell-wallace/rally/internal/app"
	"github.com/mitchell-wallace/rally/internal/presentation/tuicore"
	"github.com/mitchell-wallace/rally/internal/presentation/tuitabs"
)

type tuiConfigBindings struct {
	snapshot tuicore.ConfigSnapshot
	fetch    func(context.Context) (tuicore.ConfigSnapshot, error)
	update   func(context.Context, tuicore.ConfigMutation) (tuicore.ConfigSnapshot, error)
}

func newTUIConfigBindings(ctx context.Context) (tuiConfigBindings, error) {
	service, err := app.NewTUIConfigService()
	if err != nil {
		return tuiConfigBindings{}, err
	}
	fetch := func(ctx context.Context) (tuicore.ConfigSnapshot, error) {
		snapshot, err := service.Snapshot(ctx)
		if err != nil {
			return tuicore.ConfigSnapshot{}, err
		}
		return appConfigToTUI(snapshot), nil
	}
	update := func(ctx context.Context, mutation tuicore.ConfigMutation) (tuicore.ConfigSnapshot, error) {
		snapshot, err := service.Apply(ctx, tuiMutationToApp(mutation))
		if err != nil {
			return tuicore.ConfigSnapshot{}, err
		}
		return appConfigToTUI(snapshot), nil
	}
	snapshot, err := fetch(ctx)
	if err != nil {
		return tuiConfigBindings{}, err
	}
	return tuiConfigBindings{snapshot: snapshot, fetch: fetch, update: update}, nil
}

func (b tuiConfigBindings) apply(opts *tuitabs.Options) {
	opts.Config = tuicore.CloneConfigSnapshot(b.snapshot)
	opts.FetchConfig = b.fetch
	opts.UpdateConfig = b.update
}

func appConfigToTUI(in app.TUIConfigSnapshot) tuicore.ConfigSnapshot {
	out := tuicore.ConfigSnapshot{
		Path:       in.Path,
		Roles:      make([]tuicore.RoleConfig, len(in.Roles)),
		Providers:  make([]tuicore.ProviderConfig, len(in.Providers)),
		Shorthands: append([]string(nil), in.Shorthands...),
	}
	for i, role := range in.Roles {
		out.Roles[i] = tuicore.RoleConfig{Name: role.Name, Route: append([]string(nil), role.Route...), Reasoning: role.Reasoning, BuiltIn: role.BuiltIn}
	}
	for i, provider := range in.Providers {
		out.Providers[i] = tuicore.ProviderConfig{Name: provider.Name, Disabled: provider.Disabled, MemberCount: provider.MemberCount}
	}
	return out
}

func tuiMutationToApp(in tuicore.ConfigMutation) app.TUIConfigMutation {
	return app.TUIConfigMutation{
		Kind:     app.TUIConfigMutationKind(in.Kind),
		Role:     in.Role,
		Route:    append([]string(nil), in.Route...),
		Value:    in.Value,
		Provider: in.Provider,
		Disabled: in.Disabled,
	}
}
