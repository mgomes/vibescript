package vibes

import (
	"context"
	"fmt"
)

// Database exposes data access capability methods to scripts.
type Database interface {
	Find(ctx context.Context, req DBFindRequest) (Value, error)
	Query(ctx context.Context, req DBQueryRequest) (Value, error)
	Update(ctx context.Context, req DBUpdateRequest) (Value, error)
	Sum(ctx context.Context, req DBSumRequest) (Value, error)
	Each(ctx context.Context, req DBEachRequest) ([]Value, error)
}

// DBFindRequest captures db.find calls.
type DBFindRequest struct {
	Collection string
	ID         Value
	Options    map[string]Value
}

// DBQueryRequest captures db.query calls.
type DBQueryRequest struct {
	Collection string
	Options    map[string]Value
}

// DBUpdateRequest captures db.update calls.
type DBUpdateRequest struct {
	Collection string
	ID         Value
	Attributes map[string]Value
	Options    map[string]Value
}

// DBSumRequest captures db.sum calls.
type DBSumRequest struct {
	Collection string
	Field      string
	Options    map[string]Value
}

// DBEachRequest captures db.each calls.
type DBEachRequest struct {
	Collection string
	Options    map[string]Value
}

// NewDBCapability constructs a capability adapter bound to the provided name.
func NewDBCapability(name string, db Database) (CapabilityAdapter, error) {
	if name == "" {
		return nil, fmt.Errorf("vibes: database capability name must be non-empty")
	}
	if isNilCapabilityImplementation(db) {
		return nil, fmt.Errorf("vibes: database capability requires a non-nil implementation")
	}
	return &dbCapability{name: name, db: db}, nil
}

// MustNewDBCapability constructs a capability adapter or panics on invalid arguments.
func MustNewDBCapability(name string, db Database) CapabilityAdapter {
	cap, err := NewDBCapability(name, db)
	if err != nil {
		panic(err)
	}
	return cap
}

type dbCapability struct {
	name string
	db   Database
}

func (c *dbCapability) CapabilityContracts() map[string]CapabilityMethodContract {
	return map[string]CapabilityMethodContract{
		c.name + ".find": {
			ValidateArgs:   c.validateFindContractArgs,
			ValidateReturn: c.validateMethodReturn(c.name + ".find"),
		},
		c.name + ".query": {
			ValidateArgs:   c.validateQueryContractArgs,
			ValidateReturn: c.validateMethodReturn(c.name + ".query"),
		},
		c.name + ".update": {
			ValidateArgs:   c.validateUpdateContractArgs,
			ValidateReturn: c.validateMethodReturn(c.name + ".update"),
		},
		c.name + ".sum": {
			ValidateArgs:   c.validateSumContractArgs,
			ValidateReturn: c.validateMethodReturn(c.name + ".sum"),
		},
		c.name + ".each": {
			ValidateArgs:   c.validateEachContractArgs,
			ValidateReturn: c.validateMethodReturn(c.name + ".each"),
		},
	}
}

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
	return c.cloneMethodResult(c.name+".find", result)
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
	return c.cloneMethodResult(c.name+".query", result)
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
	return c.cloneMethodResult(c.name+".update", result)
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
	return c.cloneMethodResult(c.name+".sum", result)
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
		if err := validateCapabilityDataOnlyValue(fmt.Sprintf("%s.each row %d", c.name, idx), row); err != nil {
			return NewNil(), err
		}
		if _, err := exec.CallBlock(block, []Value{deepCloneValue(row)}); err != nil {
			return NewNil(), err
		}
	}
	return NewNil(), nil
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
	if err := validateCapabilityDataOnlyValue(method+" id", args[1]); err != nil {
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
	if err := validateCapabilityDataOnlyValue(method+" id", args[1]); err != nil {
		return err
	}
	attrs := args[2]
	if attrs.Kind() != KindHash && attrs.Kind() != KindObject {
		return fmt.Errorf("%s expects attributes hash", method)
	}
	if err := validateCapabilityDataOnlyValue(method+" attributes", attrs); err != nil {
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

func (c *dbCapability) validateMethodReturn(method string) func(result Value) error {
	return func(result Value) error {
		return validateCapabilityDataOnlyValue(method+" return value", result)
	}
}

func (c *dbCapability) cloneMethodResult(method string, result Value) (Value, error) {
	if err := validateCapabilityDataOnlyValue(method+" return value", result); err != nil {
		return NewNil(), err
	}
	return deepCloneValue(result), nil
}
