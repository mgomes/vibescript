package main

import (
	"testing"

	"github.com/mgomes/vibescript/vibes"
)

func narrowedLabels(t *testing.T, source string, line, character int) []string {
	t.Helper()
	items := narrowedMemberCompletionItems(source, splitLSPLines(source), line, character)
	if items == nil {
		return nil
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item["label"].(string))
	}
	return labels
}

// Completion after "." offered every builtin member whatever the receiver was,
// so a string receiver was offered money and temporal methods and could not
// rule out a wrong method name.
func TestCompletionNarrowsToTheReceiverKind(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		line      int
		character int
		receiver  string
	}{
		// The cursor sits on a bare dot, which is what the buffer holds when
		// completion fires and which does not parse on its own.
		{name: "annotated string parameter", source: "def f(s: string)\n  s.\nend", line: 1, character: 4, receiver: "string"},
		{name: "partially typed member", source: "def f(s: string)\n  s.up\nend", line: 1, character: 6, receiver: "string"},
		{name: "annotated array parameter", source: "def f(items: array<int>)\n  items.\nend", line: 1, character: 8, receiver: "array"},
		{name: "annotated money parameter", source: "def f(m: money)\n  m.\nend", line: 1, character: 4, receiver: "money"},
		{name: "string literal", source: "x = \"abc\".\n", line: 0, character: 10, receiver: "string"},
		{name: "array literal", source: "x = [1].\n", line: 0, character: 8, receiver: "array"},
		{name: "hash literal", source: "x = ({a: 1}).\n", line: 0, character: 13, receiver: "hash"},
		{name: "integer literal", source: "x = 1.\n", line: 0, character: 6, receiver: "int"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			labels := narrowedLabels(t, tc.source, tc.line, tc.character)
			if labels == nil {
				t.Fatalf("%s: fell back to the full union", tc.name)
			}
			want := vibes.MemberCompletionNames()[tc.receiver]
			if len(labels) != len(want) {
				t.Fatalf("%s: %d items, want %d (%s)", tc.name, len(labels), len(want), tc.receiver)
			}
		})
	}
}

// Narrowing wrongly hides members that apply, with nothing to tell the author
// they were hidden, so every receiver this cannot resolve must fall back to
// the full union rather than guess.
func TestUnresolvedReceiversFallBackToTheFullUnion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		line      int
		character int
	}{
		{name: "unannotated parameter", source: "def f(x)\n  x.\nend", line: 1, character: 4},
		// A nullable receiver dispatches on nil too, so its members are not
		// one kind's members.
		{name: "nullable parameter", source: "def f(s: string?)\n  s.\nend", line: 1, character: 4},
		{name: "union parameter", source: "def f(s: string | int)\n  s.\nend", line: 1, character: 4},
		// A named type resolves user-defined methods, which are not in any
		// builtin table.
		{name: "class-typed parameter", source: "def f(u: User)\n  u.\nend", line: 1, character: 4},
		{name: "local variable", source: "def f()\n  x = 1\n  x.\nend", line: 2, character: 4},
		{name: "call result", source: "def f()\n  build().\nend", line: 1, character: 10},
		{name: "not a member context", source: "def f()\n  x = 1\nend", line: 1, character: 7},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if labels := narrowedLabels(t, tc.source, tc.line, tc.character); labels != nil {
				t.Fatalf("%s: narrowed to %d items, want the full union", tc.name, len(labels))
			}
		})
	}
}

// The narrowed list must be complete for its kind: a member the runtime
// accepts and completion omits is the failure this could introduce.
func TestNarrowedListKeepsEveryMemberOfTheKind(t *testing.T) {
	t.Parallel()

	labels := narrowedLabels(t, "def f(s: string)\n  s.\nend", 1, 4)
	if labels == nil {
		t.Fatalf("fell back to the full union")
	}
	present := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		present[label] = struct{}{}
	}
	// A typed member, and a universal helper that resolves through the
	// Object-level fallback rather than the string table.
	for _, want := range []string{"upcase", "split", "length", "nil?", "respond_to?", "eql?", "tap"} {
		if _, ok := present[want]; !ok {
			t.Fatalf("narrowed string list is missing %s", want)
		}
	}
	// And nothing from an unrelated kind.
	for _, unwanted := range []string{"amount", "cents", "ago", "before"} {
		if _, ok := present[unwanted]; ok {
			t.Fatalf("narrowed string list offers %s, which is not a string member", unwanted)
		}
	}
}

// The items keep the shape the union items have, so a client sees no
// difference beyond the shorter list.
func TestNarrowedItemsKeepTheirShape(t *testing.T) {
	t.Parallel()

	items := narrowedMemberCompletionItems("def f(s: string)\n  s.\nend", splitLSPLines("def f(s: string)\n  s.\nend"), 1, 4)
	if len(items) == 0 {
		t.Fatalf("no narrowed items")
	}
	for _, item := range items {
		if _, ok := item["label"].(string); !ok {
			t.Fatalf("item %v has no label", item)
		}
		if kind, ok := item["kind"].(int); !ok || kind != 2 {
			t.Fatalf("item %v kind = %v, want 2 (Method)", item, item["kind"])
		}
		if detail, ok := item["detail"].(string); !ok || detail != "string" {
			t.Fatalf("item %v detail = %v, want the receiver kind", item, item["detail"])
		}
	}
}
