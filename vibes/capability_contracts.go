package vibes

import (
	"fmt"
)

type capabilityContractScanner struct {
	seenArrays    map[sliceIdentity]struct{}
	seenMaps      map[uintptr]struct{}
	seenClasses   map[*ClassDef]struct{}
	seenInstances map[*Instance]struct{}
	excluded      map[*Builtin]struct{}
}

func newCapabilityContractScanner() *capabilityContractScanner {
	return &capabilityContractScanner{
		seenArrays:    make(map[sliceIdentity]struct{}),
		seenMaps:      make(map[uintptr]struct{}),
		seenClasses:   make(map[*ClassDef]struct{}),
		seenInstances: make(map[*Instance]struct{}),
	}
}

func validateCapabilityDataOnlyValue(label string, val Value) error {
	callableScanner := newCapabilityContractScanner()
	if callableScanner.containsCallable(val) {
		return fmt.Errorf("%s must be data-only", label)
	}
	cycleScanner := newCapabilityCycleScanner()
	if cycleScanner.containsCycle(val) {
		return fmt.Errorf("%s must not contain cyclic references", label)
	}
	return nil
}

func bindCapabilityContracts(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
) {
	bindCapabilityContractsExcluding(val, scope, target, scopes, nil)
}

func bindCapabilityContractsExcluding(
	val Value,
	scope *capabilityContractScope,
	target map[*Builtin]CapabilityMethodContract,
	scopes map[*Builtin]*capabilityContractScope,
	excluded map[*Builtin]struct{},
) {
	if scope == nil {
		return
	}
	scanner := newCapabilityContractScanner()
	scanner.excluded = excluded
	scanner.bindContracts(val, scope, target, scopes)
}

func collectCapabilityBuiltins(val Value, out map[*Builtin]struct{}) {
	if out == nil {
		return
	}
	scanner := newCapabilityContractScanner()
	scanner.collectBuiltins(val, out)
}
