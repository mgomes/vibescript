package runtime

import "context"

// CheckedCall statically checks the exact call — the same function, argument
// values, and options a Call would receive — and executes it only when the
// checker reports no diagnostics. The returned warnings are the static gate:
// when non-empty, the script did not run and the error is nil. A nil warning
// slice with a non-nil error is a runtime failure from the executed call.
//
// Both phases receive identical inputs: the static phase resolves the same
// host globals and capability surfaces that the call binds, so a script
// cannot pass the gate under one contract and execute under another.
// Capability adapters are bound in each phase with the same adapters and
// options; adapters are expected to expose the same surface on every bind.
//
// The ordinary Call API stays gradual: CheckedCall is the opt-in gate for
// deployment pipelines and untrusted-script boundaries where a provable
// contradiction should block execution entirely.
func (s *Script) CheckedCall(ctx context.Context, name string, args []Value, opts CallOptions) (Value, []CheckWarning, error) {
	if warnings := s.CheckWarningsForCall(name, args, opts); len(warnings) > 0 {
		return NewNil(), warnings, nil
	}
	val, err := s.Call(ctx, name, args, opts)
	return val, nil, err
}
