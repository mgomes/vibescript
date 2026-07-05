package runtime

import "fmt"

// blockArgumentValue converts the evaluated `&` block argument of a call into
// the block value the call plumbing carries. Blocks (procs and lambdas) pass
// through unchanged, preserving identity so a non-local return through a
// forwarded block still targets its original home method. nil means the call
// has no block, matching Ruby's f(&nil). Symbols convert via symbol-to-proc,
// and functions or bound-method builtins wrap into a forwarding block.
func (exec *Execution) blockArgumentValue(val Value, pos Position) (Value, error) {
	switch val.Kind() {
	case KindNil:
		return NewNil(), nil
	case KindBlock:
		return val, nil
	case KindSymbol:
		return newSymbolToProcBlock(val.String(), pos), nil
	case KindFunction, KindBuiltin:
		return wrapBlock(&Block{forward: val}), nil
	default:
		return NewNil(), exec.errorAt(pos, "block argument must be a block, function, or symbol, got %s", val.Kind())
	}
}

// newSymbolToProcBlock builds the callable behind `&:name`: a forwarding
// block that sends name to its first argument, passing any remaining
// arguments along — Ruby's Symbol#to_proc. The symbol is recorded as a
// captured value so the memory estimator charges each minted proc.
func newSymbolToProcBlock(name string, pos Position) Value {
	fn := NewCapturingBuiltin("symbol.to_proc", func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if len(args) == 0 {
			return NewNil(), fmt.Errorf("no receiver given to &:%s", name)
		}
		return exec.sendSymbolProcMember(args[0], name, args[1:], pos)
	}, NewSymbol(name))
	return wrapBlock(&Block{forward: fn})
}

// sendSymbolProcMember applies one symbol-to-proc invocation:
// receiver.name(rest...). Operator names dispatch through the same arithmetic
// helpers the operators and reduce's symbol shorthand use; other names
// resolve as public members, so a private method raises exactly like an
// external call would. A resolved data member (a getter-less attribute or a
// stored hash entry) is returned as the member access `receiver.name` would
// return it, but only when no extra arguments were supplied.
func (exec *Execution) sendSymbolProcMember(receiver Value, name string, rest []Value, pos Position) (Value, error) {
	if op, ok := reduceArithmeticOps[name]; ok {
		if len(rest) != 1 {
			return NewNil(), fmt.Errorf("&:%s expects exactly one argument, got %d", name, len(rest))
		}
		return op(receiver, rest[0])
	}
	member, err := exec.getPublicMember(receiver, name, pos)
	if err != nil {
		return NewNil(), err
	}
	if !isInvocable(member) {
		if len(rest) > 0 {
			return NewNil(), fmt.Errorf("&:%s does not accept arguments", name)
		}
		return member, nil
	}
	return exec.invokeCallable(member, receiver, rest, nil, NewNil(), pos)
}
