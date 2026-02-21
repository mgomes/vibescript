package vibes

type moduleEntry struct {
	key    string
	name   string
	path   string
	script *Script
}

type moduleRequest struct {
	raw              string
	normalized       string
	explicitRelative bool
}

const moduleKeySeparator = "::"

func (e *Engine) getCachedModule(key string) (moduleEntry, bool) {
	e.modMu.RLock()
	entry, ok := e.modules[key]
	e.modMu.RUnlock()
	return entry, ok
}
