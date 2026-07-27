package runtime

import (
	"context"
	"strings"
	"testing"
)

const taskRootCauseSource = `
def boom(n)
  raise "REAL CAUSE"
end
def slow(n)
  sleep(0.05)
  n
end
def double(n)
  n * 2
end
`

// When one task fails its siblings are canceled, correctly, but each of those
// reported "context canceled" -- the mechanism rather than the reason. Which
// error an author saw depended only on the order the handles happened to be
// read, which is arbitrary from where they sit, and the cancellation was also
// unrescuable because a bare context error never becomes a *RuntimeError.
func TestCancelledSiblingReportsTheRootCause(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
	}{
		// Reading the canceled sibling first is the case that lost the cause.
		{name: "canceled handle read first", body: "a = t.spawn(:slow, 1)\n      b = t.spawn(:boom, 2)\n      [a.value, b.value]"},
		{name: "failing handle read first", body: "a = t.spawn(:boom, 1)\n      b = t.spawn(:slow, 2)\n      [a.value, b.value]"},
		{name: "several canceled siblings", body: "a = t.spawn(:slow, 1)\n      b = t.spawn(:slow, 2)\n      c = t.spawn(:boom, 3)\n      [a.value, b.value, c.value]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, taskRootCauseSource+`
            def run()
              begin
                Tasks.run(max: 3) do |t|
                  `+tc.body+`
                end
                "not reached"
              rescue => e
                "rescued: #{e.message}"
              end
            end
            `)
			got, err := script.Call(context.Background(), "run", nil, CallOptions{})
			if err != nil {
				t.Fatalf("%s: the failure escaped rescue: %v", tc.name, err)
			}
			if !strings.Contains(got.String(), "REAL CAUSE") {
				t.Fatalf("%s = %q, want the root cause", tc.name, got.String())
			}
			if strings.Contains(got.String(), "context canceled") {
				t.Fatalf("%s = %q, still reports the cancellation", tc.name, got.String())
			}
		})
	}
}

// A group whose tasks all succeed is unaffected.
func TestSuccessfulTaskGroupIsUnchanged(t *testing.T) {
	t.Parallel()
	script := compileScript(t, taskRootCauseSource+`
    def run()
      Tasks.run(max: 3) do |t|
        a = t.spawn(:double, 1)
        b = t.spawn(:double, 2)
        [a.value, b.value]
      end.inspect
    end
    `)
	got, err := script.Call(context.Background(), "run", nil, CallOptions{})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if got.String() != "[2, 4]" {
		t.Fatalf("results = %s, want [2, 4]", got.String())
	}
}

// A group canceled from outside still reports the cancellation: there is no
// other cause to substitute, so the mechanism is the reason.
func TestExternallyCancelledGroupStillReportsCancellation(t *testing.T) {
	t.Parallel()
	script := compileScript(t, taskRootCauseSource+`
    def run()
      Tasks.run(max: 2) do |t|
        a = t.spawn(:slow, 1)
        [a.value]
      end
    end
    `)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := script.Call(ctx, "run", nil, CallOptions{})
	if err == nil {
		t.Fatalf("expected a canceled context to stop the run")
	}
	if strings.Contains(err.Error(), "REAL CAUSE") {
		t.Fatalf("error = %v, want the cancellation rather than an invented cause", err)
	}
}
