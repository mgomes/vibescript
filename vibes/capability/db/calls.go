package db

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/internal/capabilitycontract"
	"github.com/mgomes/vibescript/vibes/value"
)

// CallFind implements the db.find boundary: arg validation, host
// invocation, and cloning of the host-returned Value so script-side
// state cannot alias the host's data.
func (c *Capability) CallFind(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	if err := c.validateFindContractArgs(args, kwargs, block); err != nil {
		return value.NewNil(), err
	}
	return c.callFindValidated(exec, args, kwargs, block)
}

func (c *Capability) callFindValidated(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	req := DBFindRequest{
		Collection: args[0].String(),
		ID:         capabilitycontract.DeepCloneValue(args[1]),
		Options:    capabilitycontract.CloneKwargs(kwargs),
	}
	result, err := c.db.Find(exec.Context(), req)
	if err != nil {
		return value.NewNil(), err
	}
	return capabilitycontract.CloneMethodResult(c.name+".find", result)
}

// CallQuery implements the db.query boundary.
func (c *Capability) CallQuery(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	if err := c.validateQueryContractArgs(args, kwargs, block); err != nil {
		return value.NewNil(), err
	}
	return c.callQueryValidated(exec, args, kwargs, block)
}

func (c *Capability) callQueryValidated(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	req := DBQueryRequest{
		Collection: args[0].String(),
		Options:    capabilitycontract.CloneKwargs(kwargs),
	}
	result, err := c.db.Query(exec.Context(), req)
	if err != nil {
		return value.NewNil(), err
	}
	return capabilitycontract.CloneMethodResult(c.name+".query", result)
}

// CallUpdate implements the db.update boundary.
func (c *Capability) CallUpdate(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	if err := c.validateUpdateContractArgs(args, kwargs, block); err != nil {
		return value.NewNil(), err
	}
	return c.callUpdateValidated(exec, args, kwargs, block)
}

func (c *Capability) callUpdateValidated(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	req := DBUpdateRequest{
		Collection: args[0].String(),
		ID:         capabilitycontract.DeepCloneValue(args[1]),
		Attributes: capabilitycontract.CloneHash(args[2].Hash()),
		Options:    capabilitycontract.CloneKwargs(kwargs),
	}
	result, err := c.db.Update(exec.Context(), req)
	if err != nil {
		return value.NewNil(), err
	}
	return capabilitycontract.CloneMethodResult(c.name+".update", result)
}

// CallSum implements the db.sum boundary.
func (c *Capability) CallSum(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	if err := c.validateSumContractArgs(args, kwargs, block); err != nil {
		return value.NewNil(), err
	}
	return c.callSumValidated(exec, args, kwargs, block)
}

func (c *Capability) callSumValidated(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	req := DBSumRequest{
		Collection: args[0].String(),
		Field:      args[1].String(),
		Options:    capabilitycontract.CloneKwargs(kwargs),
	}
	result, err := c.db.Sum(exec.Context(), req)
	if err != nil {
		return value.NewNil(), err
	}
	return capabilitycontract.CloneMethodResult(c.name+".sum", result)
}

// CallEach implements the db.each boundary. The host returns the row
// set up front; the capability charges one interpreter step per row,
// validates each row is data-only, deep-copies it, and yields it to
// the script-supplied block.
func (c *Capability) CallEach(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	if err := c.validateEachContractArgs(args, kwargs, block); err != nil {
		return value.NewNil(), err
	}
	return c.callEachValidated(exec, args, kwargs, block)
}

func (c *Capability) callEachValidated(exec ExecutionContext, args []value.Value, kwargs map[string]value.Value, block value.Value) (value.Value, error) {
	req := DBEachRequest{
		Collection: args[0].String(),
		Options:    capabilitycontract.CloneKwargs(kwargs),
	}
	rows, err := c.db.Each(exec.Context(), req)
	if err != nil {
		return value.NewNil(), err
	}
	for idx, row := range rows {
		if err := exec.Step(); err != nil {
			return value.NewNil(), err
		}
		if err := capabilitycontract.ValidateDataOnlyValue(fmt.Sprintf("%s.each row %d", c.name, idx), row); err != nil {
			return value.NewNil(), err
		}
		if _, err := exec.CallBlock(block, []value.Value{capabilitycontract.DeepCloneValue(row)}); err != nil {
			return value.NewNil(), err
		}
	}
	return value.NewNil(), nil
}
