package runtime

import (
	"context"
	"errors"

	"github.com/mgomes/vibescript/internal/ast"
)

func isLoopControlSignal(err error) bool {
	return errors.Is(err, errLoopBreak) || errors.Is(err, errLoopNext)
}

func isHostControlSignal(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, errStepQuotaExceeded) ||
		errors.Is(err, errMemoryQuotaExceeded)
}

func runtimeErrorMatchesRescueType(err error, rescueTy *TypeExpr) bool {
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) {
		return false
	}
	if rescueTy == nil {
		return true
	}
	errKind := classifyRuntimeErrorType(err)
	return rescueTypeMatchesErrorKind(rescueTy, errKind)
}

func rescueTypeMatchesErrorKind(ty *TypeExpr, errKind string) bool {
	if ty == nil {
		return false
	}
	if ty.Kind == TypeUnion {
		for _, option := range ty.Union {
			if rescueTypeMatchesErrorKind(option, errKind) {
				return true
			}
		}
		return false
	}
	canonical, ok := ast.CanonicalRuntimeErrorType(ty.Name)
	if !ok {
		return false
	}
	if canonical == runtimeErrorTypeBase {
		return true
	}
	return canonical == errKind
}
