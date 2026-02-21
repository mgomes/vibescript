package vibes

import "fmt"

func (c *dbCapability) Bind(binding CapabilityBinding) (map[string]Value, error) {
	methods := map[string]Value{
		"find":   NewBuiltin(c.name+".find", c.callFind),
		"query":  NewBuiltin(c.name+".query", c.callQuery),
		"update": NewBuiltin(c.name+".update", c.callUpdate),
		"sum":    NewBuiltin(c.name+".sum", c.callSum),
		"each":   NewBuiltin(c.name+".each", c.callEach),
	}
	return map[string]Value{c.name: NewObject(methods)}, nil
}

func (c *dbCapability) callFind(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if err := c.validateFindContractArgs(args, kwargs, block); err != nil {
		return NewNil(), err
	}
	collection, _ := capabilityNameArg(c.name+".find", "collection", args[0])
	req := DBFindRequest{
		Collection: collection,
		ID:         deepCloneValue(args[1]),
		Options:    cloneCapabilityKwargs(kwargs),
	}
	result, err := c.db.Find(exec.ctx, req)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(c.name+".find", result)
}

func (c *dbCapability) callQuery(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if err := c.validateQueryContractArgs(args, kwargs, block); err != nil {
		return NewNil(), err
	}
	collection, _ := capabilityNameArg(c.name+".query", "collection", args[0])
	req := DBQueryRequest{
		Collection: collection,
		Options:    cloneCapabilityKwargs(kwargs),
	}
	result, err := c.db.Query(exec.ctx, req)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(c.name+".query", result)
}

func (c *dbCapability) callUpdate(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if err := c.validateUpdateContractArgs(args, kwargs, block); err != nil {
		return NewNil(), err
	}
	collection, _ := capabilityNameArg(c.name+".update", "collection", args[0])
	req := DBUpdateRequest{
		Collection: collection,
		ID:         deepCloneValue(args[1]),
		Attributes: cloneHash(args[2].Hash()),
		Options:    cloneCapabilityKwargs(kwargs),
	}
	result, err := c.db.Update(exec.ctx, req)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(c.name+".update", result)
}

func (c *dbCapability) callSum(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if err := c.validateSumContractArgs(args, kwargs, block); err != nil {
		return NewNil(), err
	}
	collection, _ := capabilityNameArg(c.name+".sum", "collection", args[0])
	field, _ := capabilityNameArg(c.name+".sum", "field", args[1])
	req := DBSumRequest{
		Collection: collection,
		Field:      field,
		Options:    cloneCapabilityKwargs(kwargs),
	}
	result, err := c.db.Sum(exec.ctx, req)
	if err != nil {
		return NewNil(), err
	}
	return cloneCapabilityMethodResult(c.name+".sum", result)
}

func (c *dbCapability) callEach(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if err := c.validateEachContractArgs(args, kwargs, block); err != nil {
		return NewNil(), err
	}
	collection, _ := capabilityNameArg(c.name+".each", "collection", args[0])
	req := DBEachRequest{
		Collection: collection,
		Options:    cloneCapabilityKwargs(kwargs),
	}
	rows, err := c.db.Each(exec.ctx, req)
	if err != nil {
		return NewNil(), err
	}
	for idx, row := range rows {
		if err := exec.step(); err != nil {
			return NewNil(), err
		}
		if err := validateCapabilityTypedValue(fmt.Sprintf("%s.each row %d", c.name, idx), row, capabilityTypeAny); err != nil {
			return NewNil(), err
		}
		if _, err := exec.CallBlock(block, []Value{deepCloneValue(row)}); err != nil {
			return NewNil(), err
		}
	}
	return NewNil(), nil
}
