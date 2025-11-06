package vibes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type ScriptFunction struct {
	Name   string
	Params []string
	Body   []Statement
	Pos    Position
	Env    *Env
}

type Script struct {
	engine    *Engine
	functions map[string]*ScriptFunction
}

type CallOptions struct {
	Globals      map[string]Value
	Capabilities []CapabilityAdapter
}

type Execution struct {
	engine        *Engine
	script        *Script
	ctx           context.Context
	quota         int
	steps         int
	callStack     []callFrame
	root          *Env
	modules       map[string]Value
	moduleLoading map[string]bool
}

type callFrame struct {
	Function string
	Pos      Position
}

type StackFrame struct {
	Function string
	Pos      Position
}

type RuntimeError struct {
	Message string
	Frames  []StackFrame
}

func (re *RuntimeError) Error() string {
	var b strings.Builder
	b.WriteString(re.Message)
	for _, frame := range re.Frames {
		// Show position if line number is valid (1-based)
		if frame.Pos.Line > 0 && frame.Pos.Column > 0 {
			fmt.Fprintf(&b, "\n  at %s (%d:%d)", frame.Function, frame.Pos.Line, frame.Pos.Column)
		} else if frame.Pos.Line > 0 {
			// Line valid but column missing
			fmt.Fprintf(&b, "\n  at %s (line %d)", frame.Function, frame.Pos.Line)
		} else {
			// No position information available
			fmt.Fprintf(&b, "\n  at %s", frame.Function)
		}
	}
	return b.String()
}

// Unwrap returns nil to satisfy the error unwrapping interface.
// RuntimeError is a terminal error that wraps the original error message but not the error itself.
func (re *RuntimeError) Unwrap() error {
	return nil
}

func (exec *Execution) step() error {
	exec.steps++
	if exec.quota > 0 && exec.steps > exec.quota {
		return fmt.Errorf("step quota exceeded (%d)", exec.quota)
	}
	if exec.ctx != nil {
		select {
		case <-exec.ctx.Done():
			return exec.ctx.Err()
		default:
		}
	}
	return nil
}

func (exec *Execution) errorAt(pos Position, format string, args ...any) error {
	return exec.newRuntimeError(fmt.Sprintf(format, args...), pos)
}

func (exec *Execution) newRuntimeError(message string, pos Position) error {
	frames := make([]StackFrame, 0, len(exec.callStack)+1)

	if len(exec.callStack) > 0 {
		// First frame: where the error occurred (within the current function)
		current := exec.callStack[len(exec.callStack)-1]
		frames = append(frames, StackFrame{Function: current.Function, Pos: pos})

		// Remaining frames: the call stack (where each function was called from)
		for i := len(exec.callStack) - 1; i >= 0; i-- {
			cf := exec.callStack[i]
			frames = append(frames, StackFrame(cf))
		}
	} else {
		// No call stack means error at script top level
		frames = append(frames, StackFrame{Function: "<script>", Pos: pos})
	}
	return &RuntimeError{Message: message, Frames: frames}
}

func (exec *Execution) wrapError(err error, pos Position) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*RuntimeError); ok {
		return err
	}
	return exec.newRuntimeError(err.Error(), pos)
}

func (exec *Execution) pushFrame(function string, pos Position) {
	exec.callStack = append(exec.callStack, callFrame{Function: function, Pos: pos})
}

func (exec *Execution) popFrame() {
	if len(exec.callStack) == 0 {
		return
	}
	exec.callStack = exec.callStack[:len(exec.callStack)-1]
}

func (exec *Execution) evalStatements(stmts []Statement, env *Env) (Value, bool, error) {
	result := NewNil()
	for _, stmt := range stmts {
		if err := exec.step(); err != nil {
			return NewNil(), false, err
		}
		val, returned, err := exec.evalStatement(stmt, env)
		if err != nil {
			return NewNil(), false, err
		}
		if returned {
			return val, true, nil
		}
		result = val
	}
	return result, false, nil
}

func (exec *Execution) evalStatement(stmt Statement, env *Env) (Value, bool, error) {
	switch s := stmt.(type) {
	case *ExprStmt:
		val, err := exec.evalExpression(s.Expr, env)
		return val, false, err
	case *ReturnStmt:
		val, err := exec.evalExpression(s.Value, env)
		return val, true, err
	case *AssignStmt:
		val, err := exec.evalExpression(s.Value, env)
		if err != nil {
			return NewNil(), false, err
		}
		if err := exec.assign(s.Target, val, env); err != nil {
			return NewNil(), false, err
		}
		return val, false, nil
	case *IfStmt:
		val, err := exec.evalExpression(s.Condition, env)
		if err != nil {
			return NewNil(), false, err
		}
		if val.Truthy() {
			return exec.evalStatements(s.Consequent, env)
		}
		for _, clause := range s.ElseIf {
			condVal, err := exec.evalExpression(clause.Condition, env)
			if err != nil {
				return NewNil(), false, err
			}
			if condVal.Truthy() {
				return exec.evalStatements(clause.Consequent, env)
			}
		}
		if len(s.Alternate) > 0 {
			return exec.evalStatements(s.Alternate, env)
		}
		return NewNil(), false, nil
	case *ForStmt:
		return exec.evalForStatement(s, env)
	default:
		return NewNil(), false, exec.errorAt(stmt.Pos(), "unsupported statement")
	}
}

func (exec *Execution) assign(target Expression, value Value, env *Env) error {
	switch t := target.(type) {
	case *Identifier:
		env.Assign(t.Name, value)
		return nil
	case *MemberExpr:
		obj, err := exec.evalExpression(t.Object, env)
		if err != nil {
			return err
		}
		switch obj.Kind() {
		case KindHash, KindObject:
			m := obj.Hash()
			m[t.Property] = value
			return nil
		default:
			return exec.errorAt(target.Pos(), "cannot assign to %s", obj.Kind())
		}
	case *IndexExpr:
		obj, err := exec.evalExpression(t.Object, env)
		if err != nil {
			return err
		}
		idx, err := exec.evalExpression(t.Index, env)
		if err != nil {
			return err
		}
		switch obj.Kind() {
		case KindArray:
			arr := obj.Array()
			i, err := valueToInt(idx)
			if err != nil {
				return exec.errorAt(t.Index.Pos(), "%s", err.Error())
			}
			if i < 0 || i >= len(arr) {
				return exec.errorAt(t.Index.Pos(), "array index out of bounds")
			}
			arr[i] = value
			return nil
		case KindHash, KindObject:
			key, err := valueToHashKey(idx)
			if err != nil {
				return exec.errorAt(t.Index.Pos(), "%s", err.Error())
			}
			obj.Hash()[key] = value
			return nil
		default:
			return exec.errorAt(t.Object.Pos(), "cannot index %s", obj.Kind())
		}
	default:
		return exec.errorAt(target.Pos(), "invalid assignment target")
	}
}

func (exec *Execution) evalExpression(expr Expression, env *Env) (Value, error) {
	if err := exec.step(); err != nil {
		return NewNil(), err
	}
	switch e := expr.(type) {
	case *Identifier:
		if val, ok := env.Get(e.Name); ok {
			return val, nil
		}
		return NewNil(), exec.errorAt(e.Pos(), "undefined variable %s", e.Name)
	case *IntegerLiteral:
		return NewInt(e.Value), nil
	case *FloatLiteral:
		return NewFloat(e.Value), nil
	case *StringLiteral:
		return NewString(e.Value), nil
	case *BoolLiteral:
		return NewBool(e.Value), nil
	case *NilLiteral:
		return NewNil(), nil
	case *SymbolLiteral:
		return NewSymbol(e.Name), nil
	case *ArrayLiteral:
		elems := make([]Value, len(e.Elements))
		for i, el := range e.Elements {
			val, err := exec.evalExpression(el, env)
			if err != nil {
				return NewNil(), err
			}
			elems[i] = val
		}
		return NewArray(elems), nil
	case *HashLiteral:
		entries := make(map[string]Value)
		for _, pair := range e.Pairs {
			keyVal, err := exec.evalExpression(pair.Key, env)
			if err != nil {
				return NewNil(), err
			}
			key, err := valueToHashKey(keyVal)
			if err != nil {
				return NewNil(), exec.errorAt(pair.Key.Pos(), "%s", err.Error())
			}
			val, err := exec.evalExpression(pair.Value, env)
			if err != nil {
				return NewNil(), err
			}
			entries[key] = val
		}
		return NewHash(entries), nil
	case *UnaryExpr:
		right, err := exec.evalExpression(e.Right, env)
		if err != nil {
			return NewNil(), err
		}
		switch e.Operator {
		case tokenMinus:
			switch right.Kind() {
			case KindInt:
				return NewInt(-right.Int()), nil
			case KindFloat:
				return NewFloat(-right.Float()), nil
			default:
				return NewNil(), exec.errorAt(e.Pos(), "unsupported unary - operand")
			}
		case tokenBang:
			return NewBool(!right.Truthy()), nil
		default:
			return NewNil(), exec.errorAt(e.Pos(), "unsupported unary operator")
		}
	case *BinaryExpr:
		return exec.evalBinaryExpr(e, env)
	case *RangeExpr:
		return exec.evalRangeExpr(e, env)
	case *MemberExpr:
		obj, err := exec.evalExpression(e.Object, env)
		if err != nil {
			return NewNil(), err
		}
		return exec.getMember(obj, e.Property, e.Pos())
	case *IndexExpr:
		obj, err := exec.evalExpression(e.Object, env)
		if err != nil {
			return NewNil(), err
		}
		idx, err := exec.evalExpression(e.Index, env)
		if err != nil {
			return NewNil(), err
		}
		switch obj.Kind() {
		case KindArray:
			i, err := valueToInt(idx)
			if err != nil {
				return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
			}
			arr := obj.Array()
			if i < 0 || i >= len(arr) {
				return NewNil(), exec.errorAt(e.Index.Pos(), "array index out of bounds")
			}
			return arr[i], nil
		case KindHash, KindObject:
			key, err := valueToHashKey(idx)
			if err != nil {
				return NewNil(), exec.errorAt(e.Index.Pos(), "%s", err.Error())
			}
			val, ok := obj.Hash()[key]
			if !ok {
				return NewNil(), nil
			}
			return val, nil
		default:
			return NewNil(), exec.errorAt(e.Object.Pos(), "cannot index %s", obj.Kind())
		}
	case *CallExpr:
		return exec.evalCallExpr(e, env)
	case *BlockLiteral:
		return exec.evalBlockLiteral(e, env)
	default:
		return NewNil(), exec.errorAt(expr.Pos(), "unsupported expression")
	}
}

func (exec *Execution) evalBinaryExpr(expr *BinaryExpr, env *Env) (Value, error) {
	left, err := exec.evalExpression(expr.Left, env)
	if err != nil {
		return NewNil(), err
	}
	right, err := exec.evalExpression(expr.Right, env)
	if err != nil {
		return NewNil(), err
	}

	var result Value
	switch expr.Operator {
	case tokenPlus:
		result, err = addValues(left, right)
	case tokenMinus:
		result, err = subtractValues(left, right)
	case tokenAsterisk:
		result, err = multiplyValues(left, right)
	case tokenSlash:
		result, err = divideValues(left, right)
	case tokenPercent:
		result, err = moduloValues(left, right)
	case tokenEQ:
		return NewBool(left.Equal(right)), nil
	case tokenNotEQ:
		return NewBool(!left.Equal(right)), nil
	case tokenLT:
		return compareValues(expr, left, right, func(c int) bool { return c < 0 })
	case tokenLTE:
		return compareValues(expr, left, right, func(c int) bool { return c <= 0 })
	case tokenGT:
		return compareValues(expr, left, right, func(c int) bool { return c > 0 })
	case tokenGTE:
		return compareValues(expr, left, right, func(c int) bool { return c >= 0 })
	case tokenAnd:
		return NewBool(left.Truthy() && right.Truthy()), nil
	case tokenOr:
		return NewBool(left.Truthy() || right.Truthy()), nil
	default:
		return NewNil(), exec.errorAt(expr.Pos(), "unsupported operator")
	}

	if err != nil {
		return NewNil(), exec.wrapError(err, expr.Pos())
	}
	return result, nil
}

func (exec *Execution) evalCallExpr(call *CallExpr, env *Env) (Value, error) {
	var callee Value
	var receiver Value
	var err error

	if member, ok := call.Callee.(*MemberExpr); ok {
		receiver, err = exec.evalExpression(member.Object, env)
		if err != nil {
			return NewNil(), err
		}
		callee, err = exec.getMember(receiver, member.Property, member.Pos())
		if err != nil {
			return NewNil(), err
		}
	} else {
		callee, err = exec.evalExpression(call.Callee, env)
		if err != nil {
			return NewNil(), err
		}
	}

	args := make([]Value, len(call.Args))
	for i, arg := range call.Args {
		val, err := exec.evalExpression(arg, env)
		if err != nil {
			return NewNil(), err
		}
		args[i] = val
	}

	kwargs := make(map[string]Value, len(call.KwArgs))
	for _, kw := range call.KwArgs {
		val, err := exec.evalExpression(kw.Value, env)
		if err != nil {
			return NewNil(), err
		}
		kwargs[kw.Name] = val
	}

	block := NewNil()
	if call.Block != nil {
		var err error
		block, err = exec.evalBlockLiteral(call.Block, env)
		if err != nil {
			return NewNil(), err
		}
	}

	switch callee.Kind() {
	case KindFunction:
		fn := callee.Function()
		if len(args) != len(fn.Params) {
			return NewNil(), exec.errorAt(call.Pos(), "expected %d arguments, got %d", len(fn.Params), len(args))
		}
		if call.Block != nil {
			return NewNil(), exec.errorAt(call.Pos(), "blocks are not supported for this function")
		}
		callEnv := newEnv(fn.Env)
		for i, param := range fn.Params {
			callEnv.Define(param, args[i])
		}
		exec.pushFrame(fn.Name, call.Pos())
		val, returned, err := exec.evalStatements(fn.Body, callEnv)
		exec.popFrame()
		if err != nil {
			return NewNil(), err
		}
		if returned {
			return val, nil
		}
		return val, nil
	case KindBuiltin:
		builtin := callee.Builtin()
		result, err := builtin.Fn(exec, receiver, args, kwargs, block)
		if err != nil {
			return NewNil(), exec.wrapError(err, call.Pos())
		}
		return result, nil
	default:
		return NewNil(), exec.errorAt(call.Pos(), "attempted to call non-callable value")
	}
}

func (exec *Execution) evalBlockLiteral(block *BlockLiteral, env *Env) (Value, error) {
	return NewBlock(block.Params, block.Body, env), nil
}

func (exec *Execution) callBlock(block Value, args []Value) (Value, error) {
	blk := block.Block()
	if blk == nil {
		return NewNil(), fmt.Errorf("block required")
	}
	blockEnv := newEnv(blk.Env)
	for i, param := range blk.Params {
		var val Value
		if i < len(args) {
			val = args[i]
		} else {
			val = NewNil()
		}
		blockEnv.Define(param, val)
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

func (exec *Execution) evalRangeExpr(expr *RangeExpr, env *Env) (Value, error) {
	startVal, err := exec.evalExpression(expr.Start, env)
	if err != nil {
		return NewNil(), err
	}
	endVal, err := exec.evalExpression(expr.End, env)
	if err != nil {
		return NewNil(), err
	}
	start, err := valueToInt64(startVal)
	if err != nil {
		return NewNil(), exec.errorAt(expr.Start.Pos(), "%s", err.Error())
	}
	end, err := valueToInt64(endVal)
	if err != nil {
		return NewNil(), exec.errorAt(expr.End.Pos(), "%s", err.Error())
	}
	return NewRange(Range{Start: start, End: end}), nil
}

func (exec *Execution) evalForStatement(stmt *ForStmt, env *Env) (Value, bool, error) {
	iterable, err := exec.evalExpression(stmt.Iterable, env)
	if err != nil {
		return NewNil(), false, err
	}
	last := NewNil()

	switch iterable.Kind() {
	case KindArray:
		arr := iterable.Array()
		for _, item := range arr {
			env.Assign(stmt.Iterator, item)
			val, returned, err := exec.evalStatements(stmt.Body, env)
			if err != nil {
				return NewNil(), false, err
			}
			if returned {
				return val, true, nil
			}
			last = val
		}
	case KindRange:
		r := iterable.Range()
		if r.Start <= r.End {
			for i := r.Start; i <= r.End; i++ {
				env.Assign(stmt.Iterator, NewInt(i))
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					return NewNil(), false, err
				}
				if returned {
					return val, true, nil
				}
				last = val
			}
		} else {
			for i := r.Start; i >= r.End; i-- {
				env.Assign(stmt.Iterator, NewInt(i))
				val, returned, err := exec.evalStatements(stmt.Body, env)
				if err != nil {
					return NewNil(), false, err
				}
				if returned {
					return val, true, nil
				}
				last = val
			}
		}
	default:
		return NewNil(), false, exec.errorAt(stmt.Pos(), "cannot iterate over %s", iterable.Kind())
	}

	return last, false, nil
}

func (exec *Execution) getMember(obj Value, property string, pos Position) (Value, error) {
	switch obj.Kind() {
	case KindHash, KindObject:
		if val, ok := obj.Hash()[property]; ok {
			return val, nil
		}
		member, err := hashMember(obj, property)
		if err != nil {
			return NewNil(), err
		}
		return member, nil
	case KindMoney:
		return moneyMember(obj.Money(), property)
	case KindDuration:
		switch property {
		case "seconds":
			return NewInt(obj.Duration().Seconds()), nil
		case "minutes":
			// Truncates to whole minutes (e.g., 90 seconds = 1 minute)
			return NewInt(obj.Duration().Seconds() / 60), nil
		case "hours":
			// Truncates to whole hours (e.g., 7200 seconds = 2 hours)
			return NewInt(obj.Duration().Seconds() / 3600), nil
		case "format":
			return NewString(obj.Duration().String()), nil
		default:
			return NewNil(), fmt.Errorf("unknown duration method %s", property)
		}
	case KindArray:
		return arrayMember(obj, property)
	case KindString:
		return stringMember(obj, property)
	case KindInt:
		switch property {
		case "seconds", "second", "minutes", "minute", "hours", "hour", "days", "day":
			return NewDuration(secondsDuration(obj.Int(), property)), nil
		default:
			return NewNil(), exec.errorAt(pos, "unknown int member %s", property)
		}
	default:
		return NewNil(), exec.errorAt(pos, "unsupported member access on %s", obj.Kind())
	}
}

func moneyMember(m Money, property string) (Value, error) {
	switch property {
	case "currency":
		return NewString(m.Currency()), nil
	case "cents":
		return NewInt(m.Cents()), nil
	case "amount":
		return NewString(m.String()), nil
	case "format":
		return NewBuiltin("money.format", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return NewString(m.String()), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown money member %s", property)
	}
}

func hashMember(obj Value, property string) (Value, error) {
	switch property {
	case "keys":
		entries := obj.Hash()
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		values := make([]Value, len(keys))
		for i, k := range keys {
			values[i] = NewSymbol(k)
		}
		return NewArray(values), nil
	case "values":
		entries := obj.Hash()
		values := make([]Value, 0, len(entries))
		for _, v := range entries {
			values = append(values, v)
		}
		return NewArray(values), nil
	case "merge":
		return NewBuiltin("hash.merge", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || args[0].Kind() != KindHash {
				return NewNil(), fmt.Errorf("hash.merge expects a single hash argument")
			}
			base := receiver.Hash()
			addition := args[0].Hash()
			out := make(map[string]Value, len(base)+len(addition))
			for k, v := range base {
				out[k] = v
			}
			for k, v := range addition {
				out[k] = v
			}
			return NewHash(out), nil
		}), nil
	case "compact":
		return NewBuiltin("hash.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("hash.compact does not take arguments")
			}
			entries := receiver.Hash()
			out := make(map[string]Value, len(entries))
			for k, v := range entries {
				if v.Kind() != KindNil {
					out[k] = v
				}
			}
			return NewHash(out), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

func stringMember(str Value, property string) (Value, error) {
	switch property {
	case "strip":
		return NewBuiltin("string.strip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip does not take arguments")
			}
			return NewString(strings.TrimSpace(receiver.String())), nil
		}), nil
	case "upcase":
		return NewBuiltin("string.upcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.upcase does not take arguments")
			}
			return NewString(strings.ToUpper(receiver.String())), nil
		}), nil
	case "downcase":
		return NewBuiltin("string.downcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.downcase does not take arguments")
			}
			return NewString(strings.ToLower(receiver.String())), nil
		}), nil
	case "split":
		return NewBuiltin("string.split", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.split accepts at most one separator")
			}
			text := receiver.String()
			var parts []string
			if len(args) == 0 {
				parts = strings.Fields(text)
			} else {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("string.split separator must be string")
				}
				parts = strings.Split(text, args[0].String())
			}
			values := make([]Value, len(parts))
			for i, part := range parts {
				values[i] = NewString(part)
			}
			return NewArray(values), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}

func arrayMember(array Value, property string) (Value, error) {
	switch property {
	case "each":
		return NewBuiltin("array.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if block.Block() == nil {
				return NewNil(), fmt.Errorf("array.each requires a block")
			}
			for _, item := range receiver.Array() {
				if _, err := exec.callBlock(block, []Value{item}); err != nil {
					return NewNil(), err
				}
			}
			return receiver, nil
		}), nil
	case "map":
		return NewBuiltin("array.map", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if block.Block() == nil {
				return NewNil(), fmt.Errorf("array.map requires a block")
			}
			arr := receiver.Array()
			result := make([]Value, len(arr))
			for i, item := range arr {
				val, err := exec.callBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				result[i] = val
			}
			return NewArray(result), nil
		}), nil
	case "select":
		return NewBuiltin("array.select", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if block.Block() == nil {
				return NewNil(), fmt.Errorf("array.select requires a block")
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			for _, item := range arr {
				val, err := exec.callBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if val.Truthy() {
					out = append(out, item)
				}
			}
			return NewArray(out), nil
		}), nil
	case "reduce":
		return NewBuiltin("array.reduce", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if block.Block() == nil {
				return NewNil(), fmt.Errorf("array.reduce requires a block")
			}
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.reduce accepts at most one initial value")
			}
			arr := receiver.Array()
			if len(arr) == 0 && len(args) == 0 {
				return NewNil(), fmt.Errorf("array.reduce on empty array requires an initial value")
			}
			var acc Value
			start := 0
			if len(args) == 1 {
				acc = args[0]
			} else {
				acc = arr[0]
				start = 1
			}
			for i := start; i < len(arr); i++ {
				next, err := exec.callBlock(block, []Value{acc, arr[i]})
				if err != nil {
					return NewNil(), err
				}
				acc = next
			}
			return acc, nil
		}), nil
	case "push":
		return NewBuiltin("array.push", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 0 {
				return NewNil(), fmt.Errorf("array.push expects at least one argument")
			}
			base := receiver.Array()
			out := make([]Value, len(base)+len(args))
			copy(out, base)
			copy(out[len(base):], args)
			return NewArray(out), nil
		}), nil
	case "pop":
		return NewBuiltin("array.pop", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.pop accepts at most one argument")
			}
			count := 1
			if len(args) == 1 {
				n, err := valueToInt(args[0])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.pop expects non-negative integer")
				}
				count = n
			}
			arr := receiver.Array()
			if count == 0 {
				return NewHash(map[string]Value{
					"array":  NewArray(arr),
					"popped": NewNil(),
				}), nil
			}
			if len(arr) == 0 {
				popped := NewNil()
				if len(args) == 1 {
					popped = NewArray([]Value{})
				}
				return NewHash(map[string]Value{
					"array":  NewArray([]Value{}),
					"popped": popped,
				}), nil
			}
			if count > len(arr) {
				count = len(arr)
			}
			remaining := make([]Value, len(arr)-count)
			copy(remaining, arr[:len(arr)-count])
			removed := make([]Value, count)
			copy(removed, arr[len(arr)-count:])
			result := map[string]Value{
				"array": NewArray(remaining),
			}
			if count == 1 && len(args) == 0 {
				result["popped"] = removed[0]
			} else {
				result["popped"] = NewArray(removed)
			}
			return NewHash(result), nil
		}), nil
	case "uniq":
		return NewBuiltin("array.uniq", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.uniq does not take arguments")
			}
			arr := receiver.Array()
			unique := make([]Value, 0, len(arr))
			for _, item := range arr {
				found := false
				for _, existing := range unique {
					if item.Equal(existing) {
						found = true
						break
					}
				}
				if !found {
					unique = append(unique, item)
				}
			}
			return NewArray(unique), nil
		}), nil
	case "first":
		return NewBuiltin("array.first", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewNil(), nil
			}
			if len(args) == 0 {
				return arr[0], nil
			}
			n, err := valueToInt(args[0])
			if err != nil || n < 0 {
				return NewNil(), fmt.Errorf("array.first expects non-negative integer")
			}
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, n)
			copy(out, arr[:n])
			return NewArray(out), nil
		}), nil
	case "last":
		return NewBuiltin("array.last", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewNil(), nil
			}
			if len(args) == 0 {
				return arr[len(arr)-1], nil
			}
			n, err := valueToInt(args[0])
			if err != nil || n < 0 {
				return NewNil(), fmt.Errorf("array.last expects non-negative integer")
			}
			if n > len(arr) {
				n = len(arr)
			}
			out := make([]Value, n)
			copy(out, arr[len(arr)-n:])
			return NewArray(out), nil
		}), nil
	case "sum":
		return NewBuiltin("array.sum", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			arr := receiver.Array()
			total := NewInt(0)
			for _, item := range arr {
				switch item.Kind() {
				case KindInt, KindFloat:
				default:
					return NewNil(), fmt.Errorf("array.sum supports numeric values")
				}
				sum, err := addValues(total, item)
				if err != nil {
					return NewNil(), fmt.Errorf("array.sum supports numeric values")
				}
				total = sum
			}
			return total, nil
		}), nil
	case "compact":
		return NewBuiltin("array.compact", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.compact does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, 0, len(arr))
			for _, item := range arr {
				if item.Kind() != KindNil {
					out = append(out, item)
				}
			}
			return NewArray(out), nil
		}), nil
	case "flatten":
		return NewBuiltin("array.flatten", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			// depth=-1 is a sentinel value meaning "flatten fully" (no depth limit)
			depth := -1
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.flatten accepts at most one depth argument")
			}
			if len(args) == 1 {
				n, err := valueToInt(args[0])
				if err != nil || n < 0 {
					return NewNil(), fmt.Errorf("array.flatten depth must be non-negative integer")
				}
				depth = n
			}
			arr := receiver.Array()
			out := flattenValues(arr, depth)
			return NewArray(out), nil
		}), nil
	case "join":
		return NewBuiltin("array.join", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("array.join accepts at most one separator")
			}
			sep := ""
			if len(args) == 1 {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("array.join separator must be string")
				}
				sep = args[0].String()
			}
			arr := receiver.Array()
			if len(arr) == 0 {
				return NewString(""), nil
			}
			// Use strings.Builder for efficient concatenation
			var b strings.Builder
			for i, item := range arr {
				if i > 0 {
					b.WriteString(sep)
				}
				b.WriteString(item.String())
			}
			return NewString(b.String()), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}

func (e *Engine) Compile(source string) (*Script, error) {
	p := newParser(source)
	program, parseErrors := p.ParseProgram()
	if len(parseErrors) > 0 {
		return nil, combineErrors(parseErrors)
	}

	functions := make(map[string]*ScriptFunction)
	for _, stmt := range program.Statements {
		fn, ok := stmt.(*FunctionStmt)
		if !ok {
			return nil, fmt.Errorf("only function definitions are allowed at top level (found %T)", stmt)
		}
		if _, exists := functions[fn.Name]; exists {
			return nil, fmt.Errorf("duplicate function %s", fn.Name)
		}
		functions[fn.Name] = &ScriptFunction{Name: fn.Name, Params: fn.Params, Body: fn.Body, Pos: fn.Pos()}
	}

	return &Script{engine: e, functions: functions}, nil
}

func combineErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}
	msg := ""
	for _, err := range errs {
		if msg != "" {
			msg += "; "
		}
		msg += err.Error()
	}
	return errors.New(msg)
}

// Function looks up a compiled function by name.
func (s *Script) Function(name string) (*ScriptFunction, bool) {
	fn, ok := s.functions[name]
	return fn, ok
}

func (s *Script) Call(ctx context.Context, name string, args []Value, opts CallOptions) (Value, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	fn, ok := s.functions[name]
	if !ok {
		return NewNil(), fmt.Errorf("function %s not found", name)
	}

	root := newEnv(nil)
	for n, builtin := range s.engine.builtins {
		root.Define(n, builtin)
	}
	for _, fnDecl := range s.functions {
		fnDecl.Env = root
	}
	for n, fnDecl := range s.functions {
		root.Define(n, NewFunction(fnDecl))
	}

	fn.Env = root

	exec := &Execution{
		engine:        s.engine,
		script:        s,
		ctx:           ctx,
		quota:         s.engine.config.StepQuota,
		callStack:     make([]callFrame, 0, 8),
		root:          root,
		modules:       make(map[string]Value),
		moduleLoading: make(map[string]bool),
	}

	if len(opts.Capabilities) > 0 {
		binding := CapabilityBinding{Context: exec.ctx, Engine: s.engine}
		for _, adapter := range opts.Capabilities {
			if adapter == nil {
				continue
			}
			globals, err := adapter.Bind(binding)
			if err != nil {
				return NewNil(), err
			}
			for name, val := range globals {
				root.Define(name, val)
			}
		}
	}

	for n, val := range opts.Globals {
		root.Define(n, val)
	}

	callEnv := newEnv(root)
	if len(args) != len(fn.Params) {
		return NewNil(), fmt.Errorf("function %s expects %d arguments, got %d", name, len(fn.Params), len(args))
	}
	for i, param := range fn.Params {
		callEnv.Define(param, args[i])
	}

	exec.pushFrame(fn.Name, fn.Pos)
	val, returned, err := exec.evalStatements(fn.Body, callEnv)
	exec.popFrame()
	if err != nil {
		return NewNil(), err
	}
	if returned {
		return val, nil
	}
	return val, nil
}

func valueToHashKey(val Value) (string, error) {
	switch val.Kind() {
	case KindSymbol:
		return val.String(), nil
	case KindString:
		return val.String(), nil
	default:
		return "", fmt.Errorf("unsupported hash key type %v", val.Kind())
	}
}

func valueToInt64(val Value) (int64, error) {
	switch val.Kind() {
	case KindInt:
		return val.Int(), nil
	case KindFloat:
		return int64(val.Float()), nil
	default:
		return 0, fmt.Errorf("expected integer value")
	}
}

func valueToInt(val Value) (int, error) {
	switch val.Kind() {
	case KindInt:
		return int(val.Int()), nil
	case KindFloat:
		return int(val.Float()), nil
	default:
		return 0, fmt.Errorf("expected integer index")
	}
}

// flattenValues recursively flattens nested arrays up to the specified depth.
// depth=-1 means flatten completely (no limit).
// depth=0 means don't flatten at all.
// depth=1 means flatten one level, etc.
func flattenValues(values []Value, depth int) []Value {
	out := make([]Value, 0, len(values))
	for _, v := range values {
		if v.Kind() == KindArray && depth != 0 {
			nextDepth := depth
			if nextDepth > 0 {
				nextDepth--
			}
			out = append(out, flattenValues(v.Array(), nextDepth)...)
		} else {
			out = append(out, v)
		}
	}
	return out
}

func addValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		return NewInt(left.Int() + right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() + right.Float()), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		out := make([]Value, len(lArr)+len(rArr))
		copy(out, lArr)
		copy(out[len(lArr):], rArr)
		return NewArray(out), nil
	case left.Kind() == KindString || right.Kind() == KindString:
		return NewString(left.String() + right.String()), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		sum, err := left.Money().add(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(sum), nil
	default:
		return NewNil(), fmt.Errorf("unsupported addition operands")
	}
}

func subtractValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		return NewInt(left.Int() - right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() - right.Float()), nil
	case left.Kind() == KindArray && right.Kind() == KindArray:
		lArr := left.Array()
		rArr := right.Array()
		out := make([]Value, 0, len(lArr))
		for _, item := range lArr {
			found := false
			for _, remove := range rArr {
				if item.Equal(remove) {
					found = true
					break
				}
			}
			if !found {
				out = append(out, item)
			}
		}
		return NewArray(out), nil
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		diff, err := left.Money().sub(right.Money())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(diff), nil
	default:
		return NewNil(), fmt.Errorf("unsupported subtraction operands")
	}
}

func multiplyValues(left, right Value) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		return NewInt(left.Int() * right.Int()), nil
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		return NewFloat(left.Float() * right.Float()), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		return NewMoney(left.Money().mulInt(right.Int())), nil
	case left.Kind() == KindInt && right.Kind() == KindMoney:
		return NewMoney(right.Money().mulInt(left.Int())), nil
	default:
		return NewNil(), fmt.Errorf("unsupported multiplication operands")
	}
}

func divideValues(left, right Value) (Value, error) {
	switch {
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		if right.Float() == 0 {
			return NewNil(), errors.New("division by zero")
		}
		return NewFloat(left.Float() / right.Float()), nil
	case left.Kind() == KindMoney && right.Kind() == KindInt:
		res, err := left.Money().divInt(right.Int())
		if err != nil {
			return NewNil(), err
		}
		return NewMoney(res), nil
	default:
		return NewNil(), fmt.Errorf("unsupported division operands")
	}
}

func moduloValues(left, right Value) (Value, error) {
	if left.Kind() == KindInt && right.Kind() == KindInt {
		if right.Int() == 0 {
			return NewNil(), errors.New("modulo by zero")
		}
		return NewInt(left.Int() % right.Int()), nil
	}
	return NewNil(), fmt.Errorf("unsupported modulo operands")
}

func compareValues(expr *BinaryExpr, left, right Value, cmp func(int) bool) (Value, error) {
	switch {
	case left.Kind() == KindInt && right.Kind() == KindInt:
		diff := left.Int() - right.Int()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case (left.Kind() == KindInt || left.Kind() == KindFloat) && (right.Kind() == KindInt || right.Kind() == KindFloat):
		lf, rf := left.Float(), right.Float()
		switch {
		case lf < rf:
			return NewBool(cmp(-1)), nil
		case lf > rf:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindString && right.Kind() == KindString:
		switch {
		case left.String() < right.String():
			return NewBool(cmp(-1)), nil
		case left.String() > right.String():
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	case left.Kind() == KindMoney && right.Kind() == KindMoney:
		if left.Money().Currency() != right.Money().Currency() {
			return NewNil(), fmt.Errorf("money currency mismatch for comparison")
		}
		diff := left.Money().Cents() - right.Money().Cents()
		switch {
		case diff < 0:
			return NewBool(cmp(-1)), nil
		case diff > 0:
			return NewBool(cmp(1)), nil
		default:
			return NewBool(cmp(0)), nil
		}
	default:
		return NewNil(), fmt.Errorf("unsupported comparison operands")
	}
}
