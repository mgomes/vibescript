package db

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/internal/capabilitycontract"
	"github.com/mgomes/vibescript/vibes/value"
)

// Contracts returns the boundary validators the runtime should enforce
// around each db.* builtin. The map keys must match the builtin names
// bound in the vibes-side adapter so the contract scanner can resolve
// them.
func (c *Capability) Contracts() map[string]Contract {
	return map[string]Contract{
		c.name + ".find": {
			ValidateArgs:   c.validateFindContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".find"),
		},
		c.name + ".query": {
			ValidateArgs:   c.validateQueryContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".query"),
		},
		c.name + ".update": {
			ValidateArgs:   c.validateUpdateContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".update"),
		},
		c.name + ".sum": {
			ValidateArgs:   c.validateSumContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".sum"),
		},
		c.name + ".each": {
			ValidateArgs:   c.validateEachContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".each"),
		},
	}
}

func (c *Capability) validateFindContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	method := c.name + ".find"
	if len(args) != 2 {
		return fmt.Errorf("%s expects collection and id", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilitycontract.NameArg(method, "collection", args[0]); err != nil {
		return err
	}
	if err := capabilitycontract.ValidateDataOnlyValue(method+" id", args[1]); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(method, kwargs)
}

func (c *Capability) validateQueryContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	method := c.name + ".query"
	if len(args) != 1 {
		return fmt.Errorf("%s expects collection", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilitycontract.NameArg(method, "collection", args[0]); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(method, kwargs)
}

func (c *Capability) validateUpdateContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	method := c.name + ".update"
	if len(args) != 3 {
		return fmt.Errorf("%s expects collection, id, and attributes", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilitycontract.NameArg(method, "collection", args[0]); err != nil {
		return err
	}
	if err := capabilitycontract.ValidateDataOnlyValue(method+" id", args[1]); err != nil {
		return err
	}
	if err := capabilitycontract.ValidateHashValue(method+" attributes", args[2]); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(method, kwargs)
}

func (c *Capability) validateSumContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	method := c.name + ".sum"
	if len(args) != 2 {
		return fmt.Errorf("%s expects collection and field", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilitycontract.NameArg(method, "collection", args[0]); err != nil {
		return err
	}
	if _, err := capabilitycontract.NameArg(method, "field", args[1]); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(method, kwargs)
}

func (c *Capability) validateEachContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	method := c.name + ".each"
	if len(args) != 1 {
		return fmt.Errorf("%s expects collection", method)
	}
	if err := capabilitycontract.EnsureBlock(block, method); err != nil {
		return err
	}
	if _, err := capabilitycontract.NameArg(method, "collection", args[0]); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(method, kwargs)
}
