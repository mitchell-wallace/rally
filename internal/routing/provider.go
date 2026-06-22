package routing

// providerRunner is the map key for the runner -> provider lookup: the resolved
// harness+model pair a route entry executes as.
type providerRunner struct {
	harness string
	model   string
}

// ProviderIndex maps resolved runners to user-defined providers — quota groups
// whose members share a single usage-limit budget. Members of the same provider
// are benched together when any one of them hits a usage limit, and an operator
// can disable a whole provider to sideline every member for the relay.
//
// A nil *ProviderIndex behaves as "no providers configured": QuotaScope falls
// back to the harness-default bucket and Disabled is always false, so callers
// can hold a possibly-nil index without nil-guarding every call.
type ProviderIndex struct {
	byRunner map[providerRunner]string
	disabled map[string]bool
}

// NewProviderIndex returns an empty, ready-to-populate index.
func NewProviderIndex() *ProviderIndex {
	return &ProviderIndex{
		byRunner: map[providerRunner]string{},
		disabled: map[string]bool{},
	}
}

// Add registers a runner (harness+model) as a member of the named provider and
// records whether that provider is disabled. Calling Add for several runners of
// the same provider with a consistent disabled flag is expected; once any member
// marks the provider disabled it stays disabled.
func (p *ProviderIndex) Add(name, harness, model string, disabled bool) {
	if p == nil {
		return
	}
	p.byRunner[providerRunner{harness: harness, model: model}] = name
	if disabled {
		p.disabled[name] = true
	} else if _, ok := p.disabled[name]; !ok {
		p.disabled[name] = false
	}
}

// ProviderFor returns the provider a runner belongs to, or ("", false) when the
// runner is not a member of any configured provider.
func (p *ProviderIndex) ProviderFor(harness, model string) (string, bool) {
	if p == nil {
		return "", false
	}
	name, ok := p.byRunner[providerRunner{harness: harness, model: model}]
	return name, ok
}

// QuotaScope returns the quota bucket a runner draws from. When the runner is a
// member of a provider the scope is "provider:<name>" so a usage limit benches
// every sibling sharing that account front, regardless of harness. Non-members
// fall back to the harness-default QuotaScope.
func (p *ProviderIndex) QuotaScope(harness, model string) string {
	if name, ok := p.ProviderFor(harness, model); ok {
		return "provider:" + name
	}
	return QuotaScope(harness, model)
}

// Disabled reports whether the runner belongs to a provider an operator has
// switched off. Disabled runners are sidelined for the whole relay.
func (p *ProviderIndex) Disabled(harness, model string) bool {
	name, ok := p.ProviderFor(harness, model)
	if !ok {
		return false
	}
	return p.disabled[name]
}
