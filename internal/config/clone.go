package config

// Clone returns a deep copy suitable for publication in an immutable runtime
// snapshot. Secret-bearing strings are copied as values and are never logged.
func Clone(source *Config) *Config {
	if source == nil {
		return nil
	}

	cloned := *source
	cloned.Routing.ModelMap = cloneModelMap(source.Routing.ModelMap)
	cloned.Providers.Copilot.AllowedModels = cloneStrings(source.Providers.Copilot.AllowedModels)
	cloned.Providers.Copilot.Accounts = cloneCopilotAccounts(source.Providers.Copilot.Accounts)
	cloned.Providers.Codex.AllowedModels = cloneStrings(source.Providers.Codex.AllowedModels)
	cloned.Providers.Codex.Accounts = cloneCodexAccounts(source.Providers.Codex.Accounts)
	cloned.Providers.Kilo.AllowedModels = cloneStrings(source.Providers.Kilo.AllowedModels)
	return &cloned
}

func cloneModelMap(source map[string]ModelMapEntry) map[string]ModelMapEntry {
	if source == nil {
		return nil
	}
	cloned := make(map[string]ModelMapEntry, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneStrings(source []string) []string {
	if source == nil {
		return nil
	}
	return append([]string(nil), source...)
}

func cloneCopilotAccounts(source []CopilotAccount) []CopilotAccount {
	if source == nil {
		return nil
	}
	return append([]CopilotAccount(nil), source...)
}

func cloneCodexAccounts(source []CodexAccount) []CodexAccount {
	if source == nil {
		return nil
	}
	return append([]CodexAccount(nil), source...)
}
