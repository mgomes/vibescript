package runtime

// This file connects a class's to_s to the implicit string conversions:
// interpolation and puts/print. Explicit p.to_s always worked, so the two
// disagreed, and every log line built from a domain object rendered as
// <Class instance> unless the author remembered .to_s at each site.
//
// Only the direct conversions dispatch. A value nested inside a container
// still renders through the container's own rendering, which is Ruby's rule
// too: "#{[p]}" shows the element's inspect form, not its to_s.

// instanceStringMethod returns the to_s a script instance's class defines for
// implicit conversion, if it has one. A to_s that cannot be called with zero
// arguments is not a conversion method and is left alone: dispatching to it
// would turn a program that renders a placeholder today into one that raises.
func instanceStringMethod(val Value) (*ScriptFunction, bool) {
	if val.Kind() != KindInstance {
		return nil, false
	}
	inst := valueInstance(val)
	if inst == nil || inst.Class == nil {
		return nil, false
	}
	fn, ok := inst.Class.Methods["to_s"]
	if !ok || fn == nil || !callableWithoutArguments(fn) {
		return nil, false
	}
	return fn, true
}

// callableWithoutArguments reports whether fn can be invoked with no
// arguments: every positional parameter must be optional, and a rest or
// block parameter absorbs nothing.
func callableWithoutArguments(fn *ScriptFunction) bool {
	for _, param := range fn.Params {
		switch param.Kind {
		case ParamNormal, ParamKeyword:
			if param.DefaultVal == nil {
				return false
			}
		}
	}
	return true
}

// instanceStringValue converts a script instance to the string its class's
// to_s returns, so interpolation and puts agree with an explicit .to_s call.
//
// It reports false when the value needs no substitution: not an instance, no
// zero-argument to_s, or a to_s that returns a non-string. The last case
// falls back to the <Class instance> placeholder rather than raising, which
// is what Ruby does when to_s does not produce a string.
//
// Unlike the operator dispatch, visibility is not checked. Ruby calls a
// private to_s from interpolation, and a class that made to_s private still
// meant it as the string form.
func (exec *Execution) instanceStringValue(val Value, pos Position) (Value, bool, error) {
	fn, ok := instanceStringMethod(val)
	if !ok {
		return val, false, nil
	}
	rendered, err := exec.callOperatorFunction(fn, val, nil, pos)
	if err != nil {
		return val, false, err
	}
	if rendered.Kind() != KindString {
		return val, false, nil
	}
	return rendered, true, nil
}
