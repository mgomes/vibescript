- **Added: host callables can publish static signatures.**
  `Engine.RegisterBuiltinWithSignature` and `vibes.NewTypedBuiltin` accept an
  opt-in `Signature` (positional parameter types, optional parameters, result
  type, block policy) written in the annotation grammar. The checker validates
  known arguments and infers the declared result for engine builtins,
  call-option globals, and capability methods, and the same contract is
  enforced at runtime. Callables without a signature stay fully dynamic.
