package vibes

import "fmt"

func (c *jobQueueCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	contracts := map[string]CapabilityMethodContract{
		c.name + ".enqueue": {
			ValidateArgs:   c.validateEnqueueContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(c.name + ".enqueue"),
		},
	}
	if c.retry != nil {
		contracts[c.name+".retry"] = CapabilityMethodContract{
			ValidateArgs:   c.validateRetryContractArgs,
			ValidateReturn: capabilityValidateAnyReturn(c.name + ".retry"),
		}
	}
	return contracts
}

func (c *jobQueueCapability) validateEnqueueContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".enqueue"

	if len(args) != 2 {
		return fmt.Errorf("%s expects job name and payload", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}

	jobNameVal := args[0]
	switch jobNameVal.Kind() {
	case KindString, KindSymbol:
		// supported
	default:
		return fmt.Errorf("%s expects job name as string or symbol", method)
	}

	payloadVal := args[1]
	if err := validateCapabilityHashValue(method+" payload", payloadVal); err != nil {
		return err
	}

	return validateCapabilityKwargsDataOnly(method, kwargs)
}

func (c *jobQueueCapability) validateRetryContractArgs(args []Value, kwargs map[string]Value, block Value) error {
	method := c.name + ".retry"

	if len(args) < 1 || len(args) > 2 {
		return fmt.Errorf("%s expects job id and optional options hash", method)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept blocks", method)
	}

	idVal := args[0]
	if idVal.Kind() != KindString {
		return fmt.Errorf("%s expects job id string", method)
	}

	if len(args) == 2 {
		optionsVal := args[1]
		if err := validateCapabilityHashValue(method+" options", optionsVal); err != nil {
			return err
		}
	}

	return validateCapabilityKwargsDataOnly(method, kwargs)
}
