package runtime

import (
	"fmt"
	"math"
)

// registerMathBuiltins installs the Ruby-style `Math` namespace: the
// transcendental constants `PI` and `E` plus the pure numeric helpers that
// Ruby exposes as module functions. Constants are read with either accessor
// (`Math::PI` or `Math.PI`); helpers are called like `Math.sqrt(9)`.
//
// The helpers are constant-time CPU work backed by Go's math package, so they
// need no host capability boundary. They still validate argument arity and
// type and raise on domain violations, mirroring Ruby's Math::DomainError for
// inputs outside a function's mathematical domain. Integer arguments are
// promoted to floats and every helper returns a float, matching Ruby where
// Math always yields a Float.
func registerMathBuiltins(engine *Engine) {
	engine.builtins["Math"] = NewObject(map[string]Value{
		"PI":    NewFloat(math.Pi),
		"E":     NewFloat(math.E),
		"sqrt":  mathUnary("Math.sqrt", math.Sqrt),
		"cbrt":  mathUnary("Math.cbrt", math.Cbrt),
		"sin":   mathUnary("Math.sin", math.Sin),
		"cos":   mathUnary("Math.cos", math.Cos),
		"tan":   mathUnary("Math.tan", math.Tan),
		"asin":  mathUnary("Math.asin", math.Asin),
		"acos":  mathUnary("Math.acos", math.Acos),
		"atan":  mathUnary("Math.atan", math.Atan),
		"exp":   mathUnary("Math.exp", math.Exp),
		"log2":  mathUnary("Math.log2", math.Log2),
		"log10": mathUnary("Math.log10", math.Log10),
		"atan2": mathBinary("Math.atan2", math.Atan2),
		"hypot": mathBinary("Math.hypot", math.Hypot),
		"log":   NewBuiltin("Math.log", builtinMathLog),
	})
}

// mathUnary builds a single-argument Math helper from a Go math function. Go's
// math package already returns NaN for arguments outside a function's domain
// (e.g. sqrt(-1), asin(2)), which mathResult reports as a domain error.
func mathUnary(name string, fn func(float64) float64) Value {
	return NewBuiltin(name, func(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := rejectMathKwargsBlock(name, kwargs, block); err != nil {
			return NewNil(), err
		}
		if len(args) != 1 {
			return NewNil(), fmt.Errorf("%s expects 1 argument, got %d", name, len(args))
		}
		x, err := mathFloatArg(name, args[0])
		if err != nil {
			return NewNil(), err
		}
		return mathResult(name, fn(x), x)
	})
}

// mathBinary builds a two-argument Math helper (atan2, hypot). Both are defined
// across the whole real plane, so no domain check is needed.
func mathBinary(name string, fn func(float64, float64) float64) Value {
	return NewBuiltin(name, func(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		if err := rejectMathKwargsBlock(name, kwargs, block); err != nil {
			return NewNil(), err
		}
		if len(args) != 2 {
			return NewNil(), fmt.Errorf("%s expects 2 arguments, got %d", name, len(args))
		}
		x, err := mathFloatArg(name, args[0])
		if err != nil {
			return NewNil(), err
		}
		y, err := mathFloatArg(name, args[1])
		if err != nil {
			return NewNil(), err
		}
		return NewFloat(fn(x, y)), nil
	})
}

// builtinMathLog implements Ruby's `Math.log(x)` and `Math.log(x, base)`.
// With one argument it computes the natural logarithm; with a second it
// computes the logarithm in that base. A negative operand is a domain error,
// while `log(0)` follows Ruby and IEEE 754 by returning -Infinity.
func builtinMathLog(_ *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	const name = "Math.log"
	if err := rejectMathKwargsBlock(name, kwargs, block); err != nil {
		return NewNil(), err
	}
	if len(args) < 1 || len(args) > 2 {
		return NewNil(), fmt.Errorf("%s expects 1 or 2 arguments, got %d", name, len(args))
	}
	x, err := mathFloatArg(name, args[0])
	if err != nil {
		return NewNil(), err
	}
	if len(args) == 1 {
		return mathResult(name, math.Log(x), x)
	}
	base, err := mathFloatArg(name, args[1])
	if err != nil {
		return NewNil(), err
	}
	// Ruby computes log(x)/log(base); a negative operand on either side has no
	// real logarithm and yields NaN, which mathResult reports as a domain
	// error. The "input" guard combines both operands so a NaN that originates
	// from a NaN argument still propagates unchanged.
	input := x
	if math.IsNaN(base) {
		input = base
	}
	return mathResult(name, math.Log(x)/math.Log(base), input)
}

// mathResult turns a helper's raw output into a value, raising a domain error
// when a finite input produced a NaN (e.g. sqrt(-1) or log(-1)). A NaN that
// originates from a NaN input is passed through unchanged so propagation
// matches Ruby and IEEE 754.
func mathResult(name string, out, input float64) (Value, error) {
	if math.IsNaN(out) && !math.IsNaN(input) {
		return NewNil(), fmt.Errorf("%s out of domain", name)
	}
	return NewFloat(out), nil
}

// mathFloatArg coerces a Math argument to a float. Integers are promoted and
// floats pass through (including NaN and Infinity); any other type raises,
// matching Ruby's TypeError for non-numeric Math arguments.
func mathFloatArg(name string, arg Value) (float64, error) {
	switch arg.Kind() {
	case KindInt:
		return float64(arg.Int()), nil
	case KindFloat:
		return arg.Float(), nil
	default:
		return 0, fmt.Errorf("%s expects a numeric argument, got %s", name, arg.Kind())
	}
}

func rejectMathKwargsBlock(name string, kwargs map[string]Value, block Value) error {
	if len(kwargs) > 0 {
		return fmt.Errorf("%s does not accept keyword arguments", name)
	}
	if !block.IsNil() {
		return fmt.Errorf("%s does not accept a block", name)
	}
	return nil
}
