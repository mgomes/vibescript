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
			CallValidated:  c.callFindValidated,
		},
		c.name + ".query": {
			ValidateArgs:   c.validateQueryContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".query"),
			CallValidated:  c.callQueryValidated,
		},
		c.name + ".update": {
			ValidateArgs:   c.validateUpdateContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".update"),
			CallValidated:  c.callUpdateValidated,
		},
		c.name + ".sum": {
			ValidateArgs:   c.validateSumContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".sum"),
			CallValidated:  c.callSumValidated,
		},
		c.name + ".each": {
			ValidateArgs:   c.validateEachContractArgs,
			ValidateReturn: capabilitycontract.ValidateAnyReturn(c.name + ".each"),
			CallValidated:  c.callEachValidated,
		},
	}
}

func (c *Capability) validateFindContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	if err := c.validateFindCallShapeArgs(args, kwargs, block); err != nil {
		return err
	}
	method := c.name + ".find"
	if err := capabilitycontract.ValidateDataOnlyValue(method+" id", args[1]); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(method, kwargs)
}

func (c *Capability) validateFindCallShapeArgs(args []value.Value, _ map[string]value.Value, block value.Value) error {
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
	return nil
}

func (c *Capability) validateQueryContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	if err := c.validateQueryCallShapeArgs(args, kwargs, block); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(c.name+".query", kwargs)
}

func (c *Capability) validateQueryCallShapeArgs(args []value.Value, _ map[string]value.Value, block value.Value) error {
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
	return nil
}

func (c *Capability) validateUpdateContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	if err := c.validateUpdateCallShapeArgs(args, kwargs, block); err != nil {
		return err
	}
	method := c.name + ".update"
	if err := capabilitycontract.ValidateDataOnlyValue(method+" id", args[1]); err != nil {
		return err
	}
	if err := capabilitycontract.ValidateHashValue(method+" attributes", args[2]); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(method, kwargs)
}

func (c *Capability) validateUpdateCallShapeArgs(args []value.Value, _ map[string]value.Value, block value.Value) error {
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
	return nil
}

func (c *Capability) validateSumContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	if err := c.validateSumCallShapeArgs(args, kwargs, block); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(c.name+".sum", kwargs)
}

func (c *Capability) validateSumCallShapeArgs(args []value.Value, _ map[string]value.Value, block value.Value) error {
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
	return nil
}

func (c *Capability) validateEachContractArgs(args []value.Value, kwargs map[string]value.Value, block value.Value) error {
	if err := c.validateEachCallShapeArgs(args, kwargs, block); err != nil {
		return err
	}
	return capabilitycontract.ValidateKwargsDataOnly(c.name+".each", kwargs)
}

func (c *Capability) validateEachCallShapeArgs(args []value.Value, _ map[string]value.Value, block value.Value) error {
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
	return nil
}
