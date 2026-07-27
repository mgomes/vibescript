package runtime

// A space before a parenthesised argument changes how the rest of the
// expression binds, and it binds differently than in Ruby:
//
//	f (x).length    // here: (f(x)).length     Ruby: f((x).length)
//
// Both readings produce a value, neither reports anything, and the results are
// unrelated. The spelling most likely to be written is a debugging line --
// `puts (x).inspect` -- where the effect is that the one construct written to
// see a value accurately renders it inaccurately: `puts` receives the value and
// `.inspect` applies to its nil result, so quotes vanish and ["1"] and [1]
// become indistinguishable at exactly the moment you are telling them apart.
//
// The binding is not changed here, because both readings are defensible and
// changing it would silently alter programs that work today. What is reported
// is the ambiguity itself, which is the part that was invisible.

// checkSpacedParenMemberCalls reports every call written as `f (x)` whose
// result then takes a member access.
func (c *scriptChecker) checkSpacedParenMemberCalls() {
	for _, fn := range c.sortedScriptFunctions() {
		c.reportSpacedParenMemberCalls(fn.Name, fn)
	}
	for _, classDef := range c.sortedClasses() {
		visitCallExprsInStatements(classDef.Body, c.spacedParenReporter(classDef.Name))
		for _, method := range sortedCheckFunctions(classDef.Methods) {
			c.reportSpacedParenMemberCalls(classDef.Name+"#"+method.Name, method)
		}
		for _, method := range sortedCheckFunctions(classDef.ClassMethods) {
			c.reportSpacedParenMemberCalls(classDef.Name+"."+method.Name, method)
		}
	}
}

func (c *scriptChecker) reportSpacedParenMemberCalls(function string, fn *ScriptFunction) {
	visitFunctionCallExprs(fn, c.spacedParenReporter(function))
}

// spacedParenReporter returns the visitor that records the diagnostic. The
// message names both readings, because knowing which one the language picked
// is the whole difficulty: the author cannot tell from the source.
func (c *scriptChecker) spacedParenReporter(function string) func(*CallExpr) {
	return func(call *CallExpr) {
		if call == nil || !call.SpacedParenTakesMember {
			return
		}
		name, ok := call.Callee.(*Identifier)
		callee := "the call"
		if ok {
			callee = name.Name
		}
		c.addOrderIndependent(function, call.Pos(),
			"a space before the parenthesis makes this %s(...) followed by a member access, not %s((...).member) as in Ruby; remove the space, or parenthesize the whole argument, to say which was meant",
			callee, callee)
	}
}
