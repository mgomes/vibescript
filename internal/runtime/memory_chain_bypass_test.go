package runtime

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// quotaComparison matches a direct comparison against an execution's own memory
// quota.
var quotaComparison = regexp.MustCompile(`[><]=?\s*(?:\w+\.)*exec\.memoryQuota\b`)

// TestNoHardRefusalBypassesTheMemoryChain pins that the chain-aware check is the
// only way to refuse an allocation.
//
// Every memory check guards on exec.memoryQuota, and for a long time comparing
// against it directly was how a refusal was written. Once a chain ceiling exists
// alongside the local quota, such a comparison is a bypass: it can refuse on the
// local quota while never consulting -- or publishing to -- the chain, so a level
// grows and blocks without its ancestors ever seeing the growth. Twenty such
// sites existed, of which a review found two; the rest were the same defect
// waiting to be reported one at a time.
//
// So this is enforced structurally rather than site by site. A refusal is
// recognized by memoryQuotaExceededError appearing just below the comparison,
// which is exactly the shape of a hard check. Soft probes that answer a capacity
// question and return a number or a bool are untouched, deliberately: they have
// a cheaper fallback and admit nothing.
//
// If this fails, the fix is to call exec.memoryExceeded(used) rather than to add
// the file to an exemption.
func TestNoHardRefusalBypassesTheMemoryChain(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Clean(name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			code := line
			if idx := strings.Index(code, "//"); idx >= 0 {
				code = code[:idx]
			}
			if !quotaComparison.MatchString(code) {
				continue
			}
			// A refusal names the error within the lines it guards.
			window := strings.Join(lines[i:min(i+4, len(lines))], "\n")
			if strings.Contains(window, "memoryQuotaExceededError") {
				offenders = append(offenders, name+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
	}

	if len(offenders) > 0 {
		t.Fatalf("these refusals compare against the execution's own quota instead of exec.memoryExceeded, so they neither enforce nor publish to the chain shared with the call's ancestors:\n%s",
			strings.Join(offenders, "\n"))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
