package tuicore

type ConfigMutationKind string

const (
	ConfigSetRoute            ConfigMutationKind = "set_route"
	ConfigSetReasoning        ConfigMutationKind = "set_reasoning"
	ConfigSetProviderDisabled ConfigMutationKind = "set_provider_disabled"
)

type ConfigMutation struct {
	Kind     ConfigMutationKind
	Role     string
	Route    []string
	Value    string
	Provider string
	Disabled bool
}

type RoleConfig struct {
	Name      string
	Route     []string
	Reasoning string
	BuiltIn   bool
}

type ProviderConfig struct {
	Name        string
	Disabled    bool
	MemberCount int
}

type ConfigSnapshot struct {
	Path       string
	Roles      []RoleConfig
	Providers  []ProviderConfig
	Shorthands []string
}

func CloneConfigSnapshot(in ConfigSnapshot) ConfigSnapshot {
	out := in
	out.Roles = make([]RoleConfig, len(in.Roles))
	for i, role := range in.Roles {
		role.Route = append([]string(nil), role.Route...)
		out.Roles[i] = role
	}
	out.Providers = append([]ProviderConfig(nil), in.Providers...)
	out.Shorthands = append([]string(nil), in.Shorthands...)
	return out
}
