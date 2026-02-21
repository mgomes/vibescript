package vibes

import "fmt"

func (exec *Execution) evalBlockLiteral(block *BlockLiteral, env *Env) (Value, error) {
	blockValue := NewBlock(block.Params, block.Body, env)
	if ctx := exec.currentModuleContext(); ctx != nil {
		blk := blockValue.Block()
		blk.moduleKey = ctx.key
		blk.modulePath = ctx.path
		blk.moduleRoot = ctx.root
	}
	return blockValue, nil
}

func ensureBlock(block Value, name string) error {
	if block.Block() == nil {
		if name != "" {
			return fmt.Errorf("%s requires a block", name)
		}
		return fmt.Errorf("block required")
	}
	return nil
}

// CallBlock invokes a block value with the provided arguments.
// This is the public entry point for capability adapters that need to
// call user-supplied blocks (e.g. db.each, db.tx).
func (exec *Execution) CallBlock(block Value, args []Value) (Value, error) {
	if err := ensureBlock(block, ""); err != nil {
		return NewNil(), err
	}
	blk := block.Block()
	exec.pushModuleContext(moduleContext{
		key:  blk.moduleKey,
		path: blk.modulePath,
		root: blk.moduleRoot,
	})
	defer exec.popModuleContext()

	blockEnv := newEnv(blk.Env)
	for i, param := range blk.Params {
		var val Value
		if i < len(args) {
			val = args[i]
		} else {
			val = NewNil()
		}
		if param.Type != nil {
			if err := checkValueType(val, param.Type); err != nil {
				return NewNil(), exec.errorAt(param.Type.position, "%s", formatArgumentTypeMismatch(param.Name, err))
			}
		}
		blockEnv.Define(param.Name, val)
	}
	val, returned, err := exec.evalStatements(blk.Body, blockEnv)
	if err != nil {
		return NewNil(), err
	}
	if returned {
		return val, nil
	}
	return val, nil
}

func (exec *Execution) evalYield(expr *YieldExpr, env *Env) (Value, error) {
	block, ok := env.Get("__block__")
	if !ok || block.Kind() == KindNil {
		return NewNil(), exec.errorAt(expr.Pos(), "no block given")
	}
	var args []Value
	for _, arg := range expr.Args {
		val, err := exec.evalExpression(arg, env)
		if err != nil {
			return NewNil(), err
		}
		if err := exec.checkMemoryWith(val); err != nil {
			return NewNil(), err
		}
		args = append(args, val)
	}
	if len(args) > 0 {
		if err := exec.checkMemoryWith(args...); err != nil {
			return NewNil(), err
		}
	}
	return exec.CallBlock(block, args)
}
