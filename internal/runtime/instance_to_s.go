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
	if rendered, ok := objectStringEntry(val); ok {
		return rendered, true, nil
	}
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

// objectStringEntry returns the string form an attribute-bag value carries
// under to_s, if it has one.
//
// A rescued error is such a bag, and it already stores its message there: the
// content was reachable as e.to_s and e.message but the two shortest ways to
// print it, "#{e}" and puts e, both rendered the placeholder <object>. That is
// the single most common error-reporting idiom there is, so the line written
// precisely to explain a failure explained nothing -- silently, since no error
// is raised.
//
// Only a string entry substitutes. Attribute bags without one, such as match
// data, keep the rendering they have.
func objectStringEntry(val Value) (Value, bool) {
	if val.Kind() != KindObject {
		return NewNil(), false
	}
	entries := val.Hash()
	rendered, ok := entries["to_s"]
	if !ok || rendered.Kind() != KindString {
		return NewNil(), false
	}
	// KindObject also carries ordinary host data, so a to_s entry alone does
	// not mean the bag is declaring its string form: a host object holding a
	// string field of that name would have its payload rendered in place of
	// the established <object> form, exposing it. Only the bags that
	// deliberately publish a string form are rendered.
	if !declaresStringForm(entries) {
		return NewNil(), false
	}
	return rendered, true
}

// declaresStringForm reports whether an attribute bag is one of the two that
// deliberately publish their string form under to_s: a rescued error, whose
// to_s is its message, and match data, whose to_s is the matched text as in
// Ruby's MatchData. A bag that merely happens to carry a field of that name is
// not making that claim.
func declaresStringForm(entries map[string]Value) bool {
	return hasRescuedErrorShape(entries) || hasMatchDataShape(entries)
}

// hasRescuedErrorShape reports the representation a rescued error takes: the
// message under two names, its class, and a backtrace.
func hasRescuedErrorShape(entries map[string]Value) bool {
	for _, field := range []string{"message", "class", "type"} {
		if val, ok := entries[field]; !ok || val.Kind() != KindString {
			return false
		}
	}
	backtrace, ok := entries["backtrace"]
	return ok && backtrace.Kind() == KindArray
}

// hasMatchDataShape reports the representation String#match returns.
func hasMatchDataShape(entries map[string]Value) bool {
	captures, ok := entries["captures"]
	if !ok || captures.Kind() != KindArray {
		return false
	}
	for _, field := range []string{"pre_match", "post_match"} {
		if val, ok := entries[field]; !ok || val.Kind() != KindString {
			return false
		}
	}
	return true
}
