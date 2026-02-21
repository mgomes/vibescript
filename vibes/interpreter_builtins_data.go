package vibes

func registerDataBuiltins(engine *Engine) {
	engine.builtins["JSON"] = NewObject(map[string]Value{
		"parse":     NewBuiltin("JSON.parse", builtinJSONParse),
		"stringify": NewBuiltin("JSON.stringify", builtinJSONStringify),
	})
	engine.builtins["Regex"] = NewObject(map[string]Value{
		"match":       NewBuiltin("Regex.match", builtinRegexMatch),
		"replace":     NewBuiltin("Regex.replace", builtinRegexReplace),
		"replace_all": NewBuiltin("Regex.replace_all", builtinRegexReplaceAll),
	})
}
