package vibes

import "strings"

func moduleCycleFromLoadStack(stack []string, next string) ([]string, bool) {
	for idx, key := range stack {
		if key == next {
			cycle := append(append([]string(nil), stack[idx:]...), next)
			return cycle, true
		}
	}
	return nil, false
}

func moduleExecutionChain(stack []moduleContext) []string {
	chain := make([]string, 0, len(stack))
	for _, ctx := range stack {
		if ctx.key == "" {
			continue
		}
		if len(chain) > 0 && chain[len(chain)-1] == ctx.key {
			continue
		}
		chain = append(chain, ctx.key)
	}
	return chain
}

func moduleCycleFromExecution(stack []moduleContext, next string) ([]string, bool) {
	chain := moduleExecutionChain(stack)
	if len(chain) < 2 {
		return nil, false
	}
	for idx, key := range chain[:len(chain)-1] {
		if key == next {
			cycle := append(append([]string(nil), chain[idx:]...), next)
			return cycle, true
		}
	}
	return nil, false
}

func formatModuleCycle(cycle []string) string {
	if len(cycle) == 0 {
		return ""
	}
	normalized := make([]string, 0, len(cycle))
	for _, key := range cycle {
		if len(normalized) > 0 && normalized[len(normalized)-1] == key {
			continue
		}
		normalized = append(normalized, key)
	}
	parts := make([]string, len(normalized))
	for idx, key := range normalized {
		parts[idx] = moduleDisplayName(key)
	}
	return strings.Join(parts, " -> ")
}
