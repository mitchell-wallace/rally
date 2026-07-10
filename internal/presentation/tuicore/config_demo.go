package tuicore

func DemoConfigSnapshot() ConfigSnapshot {
	return ConfigSnapshot{
		Path: "/home/demo/.config/rally/config.toml",
		Roles: []RoleConfig{
			{Name: "default", Route: []string{"op:zai", "cx:g55"}, BuiltIn: true},
			{Name: "intern", Route: []string{"op:zai"}, Reasoning: "medium", BuiltIn: true},
			{Name: "junior", Route: []string{"op:zai", "cx:g55"}, Reasoning: "high", BuiltIn: true},
			{Name: "senior", Route: []string{"cl:sonnet"}, Reasoning: "xhigh", BuiltIn: true},
			{Name: "architect", Route: []string{"cl:opus"}, BuiltIn: true},
			{Name: "review", Route: []string{"cx:g55"}, Reasoning: "high", BuiltIn: true},
			{Name: "verify", Route: []string{"cx:g55"}, BuiltIn: true},
			{Name: "qa", Route: []string{"op:zai"}, BuiltIn: true},
			{Name: "recovery", Route: []string{"cl:sonnet"}, BuiltIn: true},
			{Name: "planner", Route: []string{"cl:opus", "cx:g55"}, Reasoning: "high"},
		},
		Providers: []ProviderConfig{
			{Name: "anthropic", MemberCount: 2},
			{Name: "openai", Disabled: true, MemberCount: 3},
		},
		Shorthands: []string{"ag:pro", "cl:opus", "cl:sonnet", "cx:g55", "op:zai"},
	}
}
