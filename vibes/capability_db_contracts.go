package vibes

import "fmt"

func (c *dbCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		c.name + ".find": {
			ValidateArgs:   c.validateFindContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(c.name + ".find"),
		},
		c.name + ".query": {
			ValidateArgs:   c.validateQueryContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(c.name + ".query"),
		},
		c.name + ".update": {
			ValidateArgs:   c.validateUpdateContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(c.name + ".update"),
		},
		c.name + ".sum": {
			ValidateArgs:   c.validateSumContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(c.name + ".sum"),
		},
		c.name + ".each": {
			ValidateArgs:   c.validateEachContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(c.name + ".each"),
		},
	}
}

func (c *dbCapability) validateFindContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".find"
	if len(args) != 2 {
		return fmt.Errorf("%s expects collection and id", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilityNameArg(method, "collection", args[0]); err != nil {
		return err
	}
	if err := validateCapabilityTypedValue(method+" id", args[1], capabilityTypeAny); err != nil {
		return err
	}
	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *dbCapability) validateQueryContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".query"
	if len(args) != 1 {
		return fmt.Errorf("%s expects collection", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilityNameArg(method, "collection", args[0]); err != nil {
		return err
	}
	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *dbCapability) validateUpdateContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".update"
	if len(args) != 3 {
		return fmt.Errorf("%s expects collection, id, and attributes", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilityNameArg(method, "collection", args[0]); err != nil {
		return err
	}
	if err := validateCapabilityTypedValue(method+" id", args[1], capabilityTypeAny); err != nil {
		return err
	}
	if err := validateCapabilityHashValue(method+" attributes", args[2]); err != nil {
		return err
	}
	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *dbCapability) validateSumContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".sum"
	if len(args) != 2 {
		return fmt.Errorf("%s expects collection and field", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}
	if _, err := capabilityNameArg(method, "collection", args[0]); err != nil {
		return err
	}
	if _, err := capabilityNameArg(method, "field", args[1]); err != nil {
		return err
	}
	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *dbCapability) validateEachContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".each"
	if len(args) != 1 {
		return fmt.Errorf("%s expects collection", method)
	}
	if err := ensureBlock(block, method); err != nil {
		return err
	}
	if _, err := capabilityNameArg(method, "collection", args[0]); err != nil {
		return err
	}
	return validateCapabilityKwargsDataOnly(method, kwargs)
}
