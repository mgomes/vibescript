package runtime

import "testing"

func TestBraceBlocksRunDocumentedArrayAndHashExamples(t *testing.T) {
	t.Parallel()

	script := compileScript(t, `
    def summarize(players, record)
      statuses = players.map { |p| p[:status] }.tally
      grouped = players.group_by { |p| p[:status] }
      all_non_negative = players.all? { |p| p[:score] >= 0 }
      public = record
        .slice(:name, :raised, :goal)
        .reject { |key, value| value == nil }

      {
        statuses: statuses,
        grouped: grouped,
        all_non_negative: all_non_negative,
        public: public
      }
    end
    `)

	playerA := NewHash(map[string]Value{"status": NewSymbol("open"), "score": NewInt(3)})
	playerB := NewHash(map[string]Value{"status": NewSymbol("closed"), "score": NewInt(0)})
	playerC := NewHash(map[string]Value{"status": NewSymbol("open"), "score": NewInt(9)})
	players := NewArray([]Value{playerA, playerB, playerC})
	record := NewHash(map[string]Value{
		"name":   NewString("Ada"),
		"raised": NewNil(),
		"goal":   NewInt(10),
		"secret": NewString("hidden"),
	})

	result := callFunc(t, script, "summarize", []Value{players, record})
	if result.Kind() != KindHash {
		t.Fatalf("summarize(...) = %v, want hash", result.Kind())
	}
	got := result.Hash()

	statuses := got["statuses"]
	if statuses.Kind() != KindHash {
		t.Fatalf("summarize(...).statuses = %v, want hash", statuses.Kind())
	}
	compareHash(t, statuses.Hash(), map[string]Value{
		"open":   NewInt(2),
		"closed": NewInt(1),
	})
	groupedValue := got["grouped"]
	if groupedValue.Kind() != KindHash {
		t.Fatalf("summarize(...).grouped = %v, want hash", groupedValue.Kind())
	}
	grouped := groupedValue.Hash()
	compareArrays(t, grouped["open"], []Value{playerA, playerC})
	compareArrays(t, grouped["closed"], []Value{playerB})
	if !got["all_non_negative"].Bool() {
		t.Fatalf("summarize(...).all_non_negative = false, want true")
	}
	public := got["public"]
	if public.Kind() != KindHash {
		t.Fatalf("summarize(...).public = %v, want hash", public.Kind())
	}
	compareHash(t, public.Hash(), map[string]Value{
		"name": NewString("Ada"),
		"goal": NewInt(10),
	})
}
