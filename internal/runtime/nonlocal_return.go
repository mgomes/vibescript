package runtime

import (
	"errors"
	"sync/atomic"
)

// nonLocalReturnTokenCounter issues process-unique method-invocation tokens.
// Uniqueness across executions matters: a block value can outlive the Call
// that created it (host-held globals rebind blocks into later Calls), and a
// per-execution counter could reissue its token to an unrelated invocation,
// letting a stale block's return hijack that method. A process-wide counter
// makes a dead invocation's token unmatchable forever, so such a return
// surfaces as LocalJumpError instead.
var nonLocalReturnTokenCounter atomic.Uint64

// nonLocalReturnSignal carries a Ruby-style non-local return out of a block
// back to the method invocation whose body lexically created the block. It
// travels the error path so enumerable drivers and ensure blocks unwind
// exactly as they do for errors, but it is not rescuable: only the invocation
// whose token matches consumes it, converting it into that method's return.
type nonLocalReturnSignal struct {
	value Value
	token uint64
}

func (s *nonLocalReturnSignal) Error() string {
	return "unexpected return (LocalJumpError)"
}

func isNonLocalReturnSignal(err error) bool {
	var sig *nonLocalReturnSignal
	return errors.As(err, &sig)
}

// matchNonLocalReturn extracts the signal when err is a non-local return
// targeted at the invocation identified by token; any other error (including
// a signal for an outer invocation) returns nil so it keeps propagating.
func matchNonLocalReturn(err error, token uint64) *nonLocalReturnSignal {
	var sig *nonLocalReturnSignal
	if errors.As(err, &sig) && sig.token == token {
		return sig
	}
	return nil
}

// pushReturnToken opens a method invocation scope and returns its token.
func (exec *Execution) pushReturnToken() uint64 {
	token := nonLocalReturnTokenCounter.Add(1)
	exec.returnTokens = append(exec.returnTokens, token)
	return token
}

func (exec *Execution) popReturnToken() {
	exec.returnTokens = exec.returnTokens[:len(exec.returnTokens)-1]
}

// currentBlockHomeToken resolves the invocation a block literal returns from.
// A literal evaluated while a block body is running belongs lexically to that
// block's method (yield may be executing the body inside a callee frame whose
// own token would be wrong), so the innermost executing block's home wins;
// otherwise the literal belongs to the innermost method invocation. Zero means
// no home (top-level or host-built blocks): a return from such a block can
// never match a frame and reports LocalJumpError.
func (exec *Execution) currentBlockHomeToken() uint64 {
	if n := len(exec.blockHomeTokens); n > 0 {
		return exec.blockHomeTokens[n-1]
	}
	if n := len(exec.returnTokens); n > 0 {
		return exec.returnTokens[n-1]
	}
	return 0
}

func (exec *Execution) pushBlockHomeToken(token uint64) {
	exec.blockHomeTokens = append(exec.blockHomeTokens, token)
}

func (exec *Execution) popBlockHomeToken() {
	exec.blockHomeTokens = exec.blockHomeTokens[:len(exec.blockHomeTokens)-1]
}
