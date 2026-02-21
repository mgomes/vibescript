package vibes

import (
	"fmt"
	"strings"
)

func bindCapabilitiesForCall(exec *Execution, root *Env, rebinder *callFunctionRebinder, capabilities []CapabilityAdapter) error {
	if len(capabilities) == 0 {
		return nil
	}

	binding := CapabilityBinding{Context: exec.ctx, Engine: exec.engine}
	for _, adapter := range capabilities {
		if adapter == nil {
			continue
		}
		scope := &capabilityContractScope{
			contracts: map[string]CapabilityMethodContract{},
		}
		if provider, ok := adapter.(CapabilityContractProvider); ok {
			for methodName, contract := range provider.CapabilityContracts() {
				name := strings.TrimSpace(methodName)
				if name == "" {
					return fmt.Errorf("capability contract method name must be non-empty")
				}
				if _, exists := exec.capabilityContractsByName[name]; exists {
					return fmt.Errorf("duplicate capability contract for %s", name)
				}
				exec.capabilityContractsByName[name] = contract
				scope.contracts[name] = contract
			}
		}
		globals, err := adapter.Bind(binding)
		if err != nil {
			return err
		}
		for name, val := range globals {
			rebound := rebinder.rebindValue(val)
			root.Define(name, rebound)
			if len(scope.contracts) > 0 {
				scope.roots = append(scope.roots, rebound)
			}
			bindCapabilityContracts(rebound, scope, exec.capabilityContracts, exec.capabilityContractScopes)
		}
	}

	return nil
}
