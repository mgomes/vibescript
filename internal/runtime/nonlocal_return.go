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
// It is only emitted while its target invocation is live (callBlock checks
// the live set first), so a signal in flight always has a frame to land on.
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

// pushReturnToken opens a method invocation scope and returns its token. The
// invocation joins both the live set (returnTokens, consulted before a block
// return emits a signal) and the lexical home stack (homeTokens, consulted
// when a block literal is created).
func (exec *Execution) pushReturnToken() uint64 {
	token := nonLocalReturnTokenCounter.Add(1)
	exec.returnTokens = append(exec.returnTokens, token)
	exec.homeTokens = append(exec.homeTokens, token)
	return token
}

func (exec *Execution) popReturnToken() {
	exec.returnTokens = exec.returnTokens[:len(exec.returnTokens)-1]
	exec.homeTokens = exec.homeTokens[:len(exec.homeTokens)-1]
}

// returnTokenLive reports whether the invocation identified by token is still
// on this execution's call stack. A block whose home invocation is not live —
// its method already returned, it was created outside any method, or it ran
// in a different execution (a task worker) — has no frame to return to, so
// its return raises LocalJumpError at the invocation site instead of
// emitting a signal.
func (exec *Execution) returnTokenLive(token uint64) bool {
	for i := len(exec.returnTokens) - 1; i >= 0; i-- {
		if exec.returnTokens[i] == token {
			return true
		}
	}
	return false
}

// currentBlockHomeToken resolves the invocation a block literal returns from:
// the top of the lexical home stack. Method invocations push their own token
// and an executing block body pushes its block's home, so the top always
// names the innermost lexical scope — a literal created by a callee running
// inside a block body homes to that callee, while a literal in the block body
// itself homes to the block's method even when yield executes the body under
// a callee frame. Zero means no home (top-level or host-built blocks): a
// return from such a block reports LocalJumpError.
func (exec *Execution) currentBlockHomeToken() uint64 {
	if n := len(exec.homeTokens); n > 0 {
		return exec.homeTokens[n-1]
	}
	return 0
}

func (exec *Execution) pushBlockHomeToken(token uint64) {
	exec.homeTokens = append(exec.homeTokens, token)
}

func (exec *Execution) popBlockHomeToken() {
	exec.homeTokens = exec.homeTokens[:len(exec.homeTokens)-1]
}
