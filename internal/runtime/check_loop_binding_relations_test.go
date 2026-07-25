package runtime

import "testing"

func TestCheckLoopBindingRelationsJoinZeroAndBodyPaths(t *testing.T) {
	t.Parallel()

	entry := checkScopeState{
		containerAlias: checkNameRelations{
			"x": {"a": {}},
			"a": {"x": {}},
			"u": {"v": {}},
			"v": {"u": {}},
		},
		containerIdentity: checkNameRelations{
			"x": {"a": {}},
			"a": {"x": {}},
			"u": {"v": {}},
			"v": {"u": {}},
		},
		staticDependents: checkNameRelations{
			"x": {"a": {}},
			"a": {"x": {}},
			"u": {"v": {}},
			"v": {"u": {}},
		},
		containerSelection: map[string]checkContainerSelection{
			"x": {key: "entry"},
			"u": {key: "stable"},
		},
	}
	body := checkScopeState{
		containerAlias: checkNameRelations{
			"x": {"b": {}},
			"b": {"x": {}},
			"u": {"v": {}},
			"v": {"u": {}},
		},
		containerIdentity: checkNameRelations{
			"x": {"b": {}},
			"b": {"x": {}},
			"u": {"v": {}},
			"v": {"u": {}},
		},
		staticDependents: checkNameRelations{
			"x": {"b": {}},
			"b": {"x": {}},
			"u": {"v": {}},
			"v": {"u": {}},
		},
		containerSelection: map[string]checkContainerSelection{
			"x": {key: "body"},
			"u": {key: "stable"},
		},
	}
	checker := &scriptChecker{
		localBindingGenerations: map[string]uint64{
			"a": 1,
			"b": 1,
			"u": 1,
			"v": 1,
			"x": 2,
		},
	}

	checker.mergeScopeBindingRelations([]checkScopeState{entry, body})

	requireCurrentBindingEdge(t, checker, checker.typeAliases, "x", "a")
	requireCurrentBindingEdge(t, checker, checker.typeAliases, "x", "b")
	requireCurrentBindingEdge(t, checker, checker.staticValueDependents, "x", "a")
	requireCurrentBindingEdge(t, checker, checker.staticValueDependents, "x", "b")
	if identities := checker.containerIdentityNames("x"); len(identities) != 1 {
		t.Fatalf("containerIdentityNames(%q) = %v, want only self", "x", identities)
	}
	requireCurrentBindingEdge(t, checker, checker.containerIdentityAliases, "u", "v")
	if _, exists := checker.containerSelections["x"]; exists {
		t.Fatal("branch-specific container selection survived loop relation join")
	}
	if selection, exists := checker.containerSelections["u"]; !exists || selection.key != "stable" {
		t.Fatalf("stable container selection = %#v, %t, want stable selection", selection, exists)
	}
}

func TestCheckLoopBindingRelationsPreservePossibleContainerProvenance(t *testing.T) {
	t.Parallel()

	const prelude = `
def mutate(values)
  values[0] = "ok"
end

def takes_string(value: string)
  value
end
`
	tests := []struct {
		name   string
		source string
	}{
		{
			name: "while zero or body",
			source: prelude + `
def run(a: array<int>, b: array<int>, flag: bool)
  x = a
  while flag
    x = b
    break
  end
  mutate(x)
  takes_string(a[0])
end
`,
		},
		{
			name: "for zero or body",
			source: prelude + `
def run(a: array<int>, b: array<int>, items: array<int>)
  x = a
  for ignored in items
    x = b
    break
  end
  mutate(x)
  takes_string(a[0])
end
`,
		},
		{
			name: "until zero or body",
			source: prelude + `
def run(a: array<int>, b: array<int>, flag: bool)
  x = a
  until flag
    x = b
    break
  end
  mutate(x)
  takes_string(a[0])
end
`,
		},
		{
			name: "while degraded intermediary",
			source: prelude + `
def run(a: array<int>, b: array<int>, c: array<int>, flag: bool)
  x = a
  y = b
  while flag
    x = y
    y = c
    break
  end
  mutate(x)
  takes_string(b[0])
end
`,
		},
		{
			name: "while multiple iterations",
			source: prelude + `
def run(a: array<int>, b: array<int>, c: array<int>, count: int)
  x = a
  y = b
  while count > 0
    x = y
    y = c
    count = count - 1
  end
  mutate(x)
  takes_string(c[0])
end
`,
		},
		{
			name: "call block zero or many with intermediary",
			source: prelude + `
def run(a: array<int>, b: array<int>, c: array<int>, items: array<int>)
  x = a
  y = b
  items.each do
    x = y
    y = c
  end
  mutate(x)
  takes_string(b[0])
end
`,
		},
		{
			name: "call block multiple iterations",
			source: prelude + `
def run(a: array<int>, b: array<int>, c: array<int>, items: array<int>)
  x = a
  y = b
  items.each do
    x = y
    y = c
  end
  mutate(x)
  takes_string(c[0])
end
`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requireNoCheckWarnings(t, compileScriptDefault(t, tc.source))
		})
	}
}

func TestCheckBlockBindingRelationsIsolateShadowingParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		check   string
		warning string
	}{
		{
			name:    "preserves the shadowed outer identity",
			check:   "takes_int(a[0])",
			warning: "call to takes_int argument value expected int, got string",
		},
		{
			name:    "discards the block-local alias",
			check:   "takes_string(b[0])",
			warning: "call to takes_string argument value expected string, got int",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			script := compileScriptDefault(t, `
def takes_int(value: int)
  value
end

def takes_string(value: string)
  value
end

def run()
  a = [1]
  b = [2]
  x = a
  [0].each do |x|
    x = b
  end
  x[0] = "ok"
  `+tc.check+`
end
`)
			requireCheckWarningContains(t, script, tc.warning)
		})
	}

	t.Run("shadow mutation preserves outer type and static facts", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def mutate(values)
  values[0] = "ok"
end

def takes_string(value: string)
  value
end

def run()
  a = [1]
  x = a
  [[2]].each do |x|
    mutate(x)
  end
  takes_string(a[0])
end
`)
		requireCheckWarningContains(
			t,
			script,
			"call to takes_string argument value expected string, got int",
		)
	})

	t.Run("poisoned outer name does not hide the shadow parameter type", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def takes_string(value: string)
  value
end

def run(callback)
  x = [1]
  callback.call(x)
  [1].each do |x: int|
    takes_string(x)
  end
end
`)
		requireCheckWarningContains(
			t,
			script,
			"call to takes_string argument value expected string, got int",
		)
	})

	t.Run("shadow parameter drops outer value correlation", func(t *testing.T) {
		t.Parallel()
		script := compileScriptDefault(t, `
def accept(values: array<int> | array<string>)
  values
end

def run(value: int | string)
  outer = value
  [1].each do |value: int | string|
    accept([value, outer])
  end
end
`)
		requireCheckWarningContains(
			t,
			script,
			"call to accept argument values expected array<int> | array<string>, got array<int | string>",
		)
	})
}

func TestCheckLoopDegradationTracksIsolatedTypedContainers(t *testing.T) {
	t.Parallel()

	checker := &scriptChecker{
		scopes: []map[string]struct{}{{
			"unknown": {},
			"values":  {},
		}},
		localTypes: []checkTypeFrame{{
			"unknown": nil,
			"values":  checkTypeIntArray,
		}},
		localClassValues: []checkClassValueFrame{nil},
	}
	checker.degradeLocalTypesForBindings([]Statement{
		&AssignStmt{Target: &Identifier{Name: "unknown"}},
		&AssignStmt{Target: &Identifier{Name: "values"}},
	})

	if got := checker.localTypeFor("values"); got != nil {
		t.Fatalf("localTypeFor(%q) = %s, want nil after loop degradation", "values", formatTypeExpr(got))
	}
	if name, ok := checker.escapePoisonTarget(&Identifier{Name: "values"}); !ok || name != "values" {
		t.Errorf("escapePoisonTarget(%q) = %q, %t, want tracked container", "values", name, ok)
	}
	if transfer := checker.captureContainerAliasTransfer(&Identifier{Name: "values"}); len(transfer.identities) == 0 {
		t.Errorf("captureContainerAliasTransfer(%q) = %#v, want retained container provenance", "values", transfer)
	}
	if name, ok := checker.escapePoisonTarget(&Identifier{Name: "unknown"}); ok {
		t.Errorf("escapePoisonTarget(%q) = %q, %t, want untracked unknown", "unknown", name, ok)
	}
	if transfer := checker.captureContainerAliasTransfer(&Identifier{Name: "unknown"}); len(transfer.identities) != 0 {
		t.Errorf("captureContainerAliasTransfer(%q) = %#v, want no arbitrary unknown provenance", "unknown", transfer)
	}
}

func TestCheckScopeStateJoinsDegradedContainerBindings(t *testing.T) {
	t.Parallel()

	t.Run("restores siblings and joins possible provenance", func(t *testing.T) {
		t.Parallel()

		checker := &scriptChecker{
			scopes: []map[string]struct{}{{
				"values": {},
			}},
			localTypes: []checkTypeFrame{{
				"values": checkTypeIntArray,
			}},
			localClassValues: []checkClassValueFrame{nil},
		}
		checker.degradeLocalTypesForBindings([]Statement{
			&AssignStmt{Target: &Identifier{Name: "values"}},
		})
		if !checker.hasDegradedContainerBinding("values") {
			t.Fatal("loop degradation did not retain container provenance")
		}
		base := checker.snapshotScopeState()

		checker.advanceLocalBindingGeneration("values")
		checker.bindLocalType("values", checkTypeInt)
		rebound := checker.snapshotScopeState()
		if checker.hasDegradedContainerBinding("values") {
			t.Fatal("ordinary rebind retained degraded container provenance")
		}

		checker.restoreScopeState(base)
		if !checker.hasDegradedContainerBinding("values") {
			t.Fatal("first branch rebind leaked into sibling scope state")
		}
		untouched := checker.snapshotScopeState()

		checker.mergeScopeStates(base, []checkScopeState{rebound, untouched})
		if got := checker.localTypeFor("values"); got != nil {
			t.Fatalf("localTypeFor(%q) = %s, want unknown branch join", "values", formatTypeExpr(got))
		}
		if name, ok := checker.escapePoisonTarget(&Identifier{Name: "values"}); !ok || name != "values" {
			t.Errorf("escapePoisonTarget(%q) = %q, %t, want joined container provenance", "values", name, ok)
		}
		if transfer := checker.captureContainerAliasTransfer(&Identifier{Name: "values"}); len(transfer.identities) == 0 {
			t.Errorf("captureContainerAliasTransfer(%q) = %#v, want joined container provenance", "values", transfer)
		}
	})

	t.Run("first branch provenance does not leak into sibling", func(t *testing.T) {
		t.Parallel()

		checker := &scriptChecker{
			scopes:           []map[string]struct{}{{"values": {}}},
			localTypes:       []checkTypeFrame{{"values": nil}},
			localClassValues: []checkClassValueFrame{nil},
		}
		base := checker.snapshotScopeState()

		checker.degradedContainerBindings = map[string]struct{}{"values": {}}
		firstBranch := checker.snapshotScopeState()
		if _, tracked := firstBranch.degradedContainers["values"]; !tracked {
			t.Fatal("first branch did not snapshot degraded container provenance")
		}
		checker.restoreScopeState(base)

		if checker.hasDegradedContainerBinding("values") {
			t.Fatal("first branch provenance leaked into sibling scope state")
		}
	})

	t.Run("clears when every fallthrough branch rebinds", func(t *testing.T) {
		t.Parallel()

		checker := &scriptChecker{
			scopes:           []map[string]struct{}{{"values": {}}},
			localTypes:       []checkTypeFrame{{"values": nil}},
			localClassValues: []checkClassValueFrame{nil},
			degradedContainerBindings: map[string]struct{}{
				"values": {},
			},
		}
		base := checker.snapshotScopeState()

		checker.advanceLocalBindingGeneration("values")
		first := checker.snapshotScopeState()
		checker.restoreScopeState(base)
		checker.advanceLocalBindingGeneration("values")
		second := checker.snapshotScopeState()

		checker.mergeScopeStates(base, []checkScopeState{first, second})
		if checker.hasDegradedContainerBinding("values") {
			t.Fatal("all-branch rebind retained degraded container provenance")
		}
		if name, ok := checker.escapePoisonTarget(&Identifier{Name: "values"}); ok {
			t.Errorf("escapePoisonTarget(%q) = %q, %t, want untracked unknown", "values", name, ok)
		}
		if transfer := checker.captureContainerAliasTransfer(&Identifier{Name: "values"}); len(transfer.identities) != 0 {
			t.Errorf("captureContainerAliasTransfer(%q) = %#v, want no stale provenance", "values", transfer)
		}
	})
}

func requireCurrentBindingEdge(
	t *testing.T,
	checker *scriptChecker,
	relations map[string]map[string]checkBindingEdge,
	from,
	to string,
) {
	t.Helper()

	edge, exists := relations[from][to]
	if !exists || !checker.bindingEdgeCurrent(from, to, edge) {
		t.Fatalf("binding edge %q -> %q = %#v, %t, want current", from, to, edge, exists)
	}
}
