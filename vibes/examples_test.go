package vibes

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type builtinAdapter func([]Value, map[string]Value) (Value, error)
type builtinBlockAdapter func(*Execution, []Value, map[string]Value, Value) (Value, error)

type callRecord struct {
	args   []Value
	kwargs map[string]Value
}

type dbMock struct {
	t          *testing.T
	findFunc   builtinAdapter
	queryFunc  builtinAdapter
	updateFunc builtinAdapter
	sumFunc    builtinAdapter
	eachFunc   builtinBlockAdapter

	findCalls   []callRecord
	queryCalls  []callRecord
	updateCalls []callRecord
	sumCalls    []callRecord
	eachCalls   []callRecord

	eachRows []Value
}

func newDBMock(t *testing.T) *dbMock {
	return &dbMock{t: t}
}

func (m *dbMock) Value() Value {
	return NewObject(map[string]Value{
		"find": makeBuiltin("db.find", func(args []Value, kwargs map[string]Value) (Value, error) {
			m.findCalls = append(m.findCalls, callRecord{cloneValues(args), cloneKwargs(kwargs)})
			if m.findFunc == nil {
				m.t.Fatalf("unexpected call to db.find")
			}
			return m.findFunc(args, kwargs)
		}),
		"query": makeBuiltin("db.query", func(args []Value, kwargs map[string]Value) (Value, error) {
			m.queryCalls = append(m.queryCalls, callRecord{cloneValues(args), cloneKwargs(kwargs)})
			if m.queryFunc == nil {
				m.t.Fatalf("unexpected call to db.query")
			}
			return m.queryFunc(args, kwargs)
		}),
		"update": makeBuiltin("db.update", func(args []Value, kwargs map[string]Value) (Value, error) {
			m.updateCalls = append(m.updateCalls, callRecord{cloneValues(args), cloneKwargs(kwargs)})
			if m.updateFunc == nil {
				m.t.Fatalf("unexpected call to db.update")
			}
			return m.updateFunc(args, kwargs)
		}),
		"sum": makeBuiltin("db.sum", func(args []Value, kwargs map[string]Value) (Value, error) {
			m.sumCalls = append(m.sumCalls, callRecord{cloneValues(args), cloneKwargs(kwargs)})
			if m.sumFunc == nil {
				m.t.Fatalf("unexpected call to db.sum")
			}
			return m.sumFunc(args, kwargs)
		}),
		"each": NewBuiltin("db.each", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			m.eachCalls = append(m.eachCalls, callRecord{cloneValues(args), cloneKwargs(kwargs)})
			if m.eachFunc != nil {
				return m.eachFunc(exec, args, kwargs, block)
			}
			if block.Block() == nil {
				m.t.Fatalf("db.each requires a block")
			}
			rows := cloneValues(m.eachRows)
			for _, row := range rows {
				if _, err := exec.CallBlock(block, []Value{row}); err != nil {
					return NewNil(), err
				}
			}
			return NewNil(), nil
		}),
	})
}

type jobsMock struct {
	enqueueFunc  builtinAdapter
	enqueueCalls []callRecord
}

func newJobsMock() *jobsMock {
	return &jobsMock{}
}

func (m *jobsMock) Value() Value {
	return NewObject(map[string]Value{
		"enqueue": makeBuiltin("jobs.enqueue", func(args []Value, kwargs map[string]Value) (Value, error) {
			m.enqueueCalls = append(m.enqueueCalls, callRecord{cloneValues(args), cloneKwargs(kwargs)})
			if m.enqueueFunc != nil {
				return m.enqueueFunc(args, kwargs)
			}
			return NewNil(), nil
		}),
	})
}

type eventsMock struct {
	publishFunc  builtinAdapter
	publishCalls []callRecord
}

func newEventsMock() *eventsMock {
	return &eventsMock{}
}

func (m *eventsMock) Value() Value {
	return NewObject(map[string]Value{
		"publish": makeBuiltin("events.publish", func(args []Value, kwargs map[string]Value) (Value, error) {
			m.publishCalls = append(m.publishCalls, callRecord{cloneValues(args), cloneKwargs(kwargs)})
			if m.publishFunc != nil {
				return m.publishFunc(args, kwargs)
			}
			return NewNil(), nil
		}),
	})
}

type exampleEnv struct {
	Globals map[string]Value
	db      *dbMock
	jobs    *jobsMock
	events  *eventsMock
}

type exampleCase struct {
	name     string
	file     string
	function string
	args     []Value
	prepare  func(*testing.T) *exampleEnv
	want     Value
	wantErr  string
	skip     bool
	after    func(*testing.T, *exampleEnv, Value, error)
}

func TestExamples(t *testing.T) {
	cases := []exampleCase{
		{
			name:     "basics/add_numbers",
			file:     "basics/literals_and_operators.vibe",
			function: "add_numbers",
			args:     []Value{intVal(2), intVal(3)},
			want:     intVal(5),
		},
		{
			name:     "basics/combine_strings",
			file:     "basics/literals_and_operators.vibe",
			function: "combine_strings",
			args:     []Value{strVal("hello"), strVal("world")},
			want:     strVal("hello world"),
		},
		{
			name:     "basics/negate",
			file:     "basics/literals_and_operators.vibe",
			function: "negate",
			args:     []Value{intVal(7)},
			want:     intVal(-7),
		},
		{
			name:     "basics/truth_table_true",
			file:     "basics/literals_and_operators.vibe",
			function: "truth_table",
			args:     []Value{boolVal(true)},
			want:     boolVal(true),
		},
		{
			name:     "basics/truth_table_false",
			file:     "basics/literals_and_operators.vibe",
			function: "truth_table",
			args:     []Value{boolVal(false)},
			want:     boolVal(false),
		},
		{
			name:     "basics/mix_literals",
			file:     "basics/literals_and_operators.vibe",
			function: "mix_literals",
			want: hashVal(map[string]Value{
				"answer": intVal(42),
				"ratio":  floatVal(3.75),
				"quote":  strVal("keep going"),
				"flags":  arrayVal(boolVal(true), boolVal(false), nilVal()),
			}),
		},
		{
			name:     "functions/greet",
			file:     "basics/functions_and_calls.vibe",
			function: "greet",
			args:     []Value{strVal("martin")},
			want:     strVal("hello martin"),
		},
		{
			name:     "functions/decorated_greeting",
			file:     "basics/functions_and_calls.vibe",
			function: "decorated_greeting",
			args:     []Value{strVal("team")},
			want:     strVal("[hello team]"),
		},
		{
			name:     "functions/sum_three",
			file:     "basics/functions_and_calls.vibe",
			function: "sum_three",
			args:     []Value{intVal(1), intVal(2), intVal(3)},
			want:     intVal(6),
		},
		{
			name:     "functions/max_value_gt",
			file:     "basics/functions_and_calls.vibe",
			function: "max_value",
			args:     []Value{intVal(9), intVal(4)},
			want:     intVal(9),
		},
		{
			name:     "functions/max_value_lt",
			file:     "basics/functions_and_calls.vibe",
			function: "max_value",
			args:     []Value{intVal(5), intVal(12)},
			want:     intVal(12),
		},
		{
			name:     "collections/head",
			file:     "collections/arrays.vibe",
			function: "head",
			args:     []Value{arrayVal(intVal(1), intVal(2), intVal(3))},
			want:     intVal(1),
		},
		{
			name:     "collections/tail",
			file:     "collections/arrays.vibe",
			function: "tail",
			args:     []Value{arrayVal(intVal(1), intVal(2), intVal(3))},
			want:     intVal(2),
		},
		{
			name:     "collections/build_matrix",
			file:     "collections/arrays.vibe",
			function: "build_matrix",
			args:     []Value{intVal(3), intVal(5)},
			want: arrayVal(
				arrayVal(intVal(3), intVal(5)),
				arrayVal(intVal(5), intVal(3)),
			),
		},
		{
			name:     "collections/replace_first",
			file:     "collections/arrays.vibe",
			function: "replace_first",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3)),
				intVal(10),
			},
			want: arrayVal(
				intVal(10),
				intVal(2),
				intVal(3),
			),
		},
		{
			name:     "arrays/first_two",
			file:     "arrays/extras.vibe",
			function: "first_two",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3), intVal(4)),
			},
			want: arrayVal(intVal(1), intVal(2)),
		},
		{
			name:     "arrays/last_three",
			file:     "arrays/extras.vibe",
			function: "last_three",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3), intVal(4)),
			},
			want: arrayVal(intVal(2), intVal(3), intVal(4)),
		},
		{
			name:     "arrays/numeric_sum",
			file:     "arrays/extras.vibe",
			function: "numeric_sum",
			args: []Value{
				arrayVal(intVal(2), intVal(3), intVal(5)),
			},
			want: intVal(10),
		},
		{
			name:     "arrays/double_sum",
			file:     "arrays/extras.vibe",
			function: "double_sum",
			args: []Value{
				arrayVal(intVal(2), intVal(3)),
			},
			want: intVal(10),
		},
		{
			name:     "arrays/push_and_pop",
			file:     "arrays/extras.vibe",
			function: "push_and_pop",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3)),
				intVal(4),
			},
			want: hashVal(map[string]Value{
				"array":  arrayVal(intVal(1), intVal(2), intVal(3)),
				"popped": intVal(4),
			}),
		},
		{
			name:     "arrays/uniq_values",
			file:     "arrays/extras.vibe",
			function: "uniq_values",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(1), intVal(3)),
			},
			want: arrayVal(intVal(1), intVal(2), intVal(3)),
		},
		{
			name:     "arrays/concat_values",
			file:     "arrays/extras.vibe",
			function: "concat_values",
			args: []Value{
				arrayVal(intVal(1), intVal(2)),
				arrayVal(intVal(3), intVal(4)),
			},
			want: arrayVal(intVal(1), intVal(2), intVal(3), intVal(4)),
		},
		{
			name:     "arrays/subtract_values",
			file:     "arrays/extras.vibe",
			function: "subtract_values",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3), intVal(2)),
				arrayVal(intVal(2)),
			},
			want: arrayVal(intVal(1), intVal(3)),
		},
		{
			name:     "arrays/include_value_true",
			file:     "arrays/extras.vibe",
			function: "include_value",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3)),
				intVal(2),
			},
			want: boolVal(true),
		},
		{
			name:     "arrays/include_value_false",
			file:     "arrays/extras.vibe",
			function: "include_value",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3)),
				intVal(9),
			},
			want: boolVal(false),
		},
		{
			name:     "arrays/first_match",
			file:     "arrays/extras.vibe",
			function: "first_match",
			args: []Value{
				arrayVal(intVal(2), intVal(5), intVal(7)),
				intVal(4),
			},
			want: intVal(5),
		},
		{
			name:     "arrays/first_match_index",
			file:     "arrays/extras.vibe",
			function: "first_match_index",
			args: []Value{
				arrayVal(intVal(2), intVal(5), intVal(7)),
				intVal(4),
			},
			want: intVal(1),
		},
		{
			name:     "arrays/count_value",
			file:     "arrays/extras.vibe",
			function: "count_value",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(1), intVal(3)),
				intVal(1),
			},
			want: intVal(2),
		},
		{
			name:     "arrays/count_large",
			file:     "arrays/extras.vibe",
			function: "count_large",
			args: []Value{
				arrayVal(intVal(1), intVal(5), intVal(6)),
				intVal(4),
			},
			want: intVal(2),
		},
		{
			name:     "arrays/predicate_snapshot",
			file:     "arrays/extras.vibe",
			function: "predicate_snapshot",
			args: []Value{
				arrayVal(intVal(2), intVal(11), intVal(4)),
			},
			want: hashVal(map[string]Value{
				"any_large":     boolVal(true),
				"all_positive":  boolVal(true),
				"none_negative": boolVal(true),
			}),
		},
		{
			name:     "arrays/sort_values",
			file:     "arrays/extras.vibe",
			function: "sort_values",
			args: []Value{
				arrayVal(intVal(3), intVal(1), intVal(2)),
			},
			want: arrayVal(intVal(1), intVal(2), intVal(3)),
		},
		{
			name:     "arrays/sort_desc",
			file:     "arrays/extras.vibe",
			function: "sort_desc",
			args: []Value{
				arrayVal(intVal(3), intVal(1), intVal(2)),
			},
			want: arrayVal(intVal(3), intVal(2), intVal(1)),
		},
		{
			name:     "arrays/sort_words_by_length",
			file:     "arrays/extras.vibe",
			function: "sort_words_by_length",
			args: []Value{
				arrayVal(strVal("bbb"), strVal("a"), strVal("cc")),
			},
			want: arrayVal(strVal("a"), strVal("cc"), strVal("bbb")),
		},
		{
			name:     "arrays/partition_even",
			file:     "arrays/extras.vibe",
			function: "partition_even",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3), intVal(4)),
			},
			want: arrayVal(
				arrayVal(intVal(2), intVal(4)),
				arrayVal(intVal(1), intVal(3)),
			),
		},
		{
			name:     "arrays/group_by_status",
			file:     "arrays/extras.vibe",
			function: "group_by_status",
			args: []Value{
				arrayVal(
					hashVal(map[string]Value{"id": strVal("p1"), "status": strVal("active")}),
					hashVal(map[string]Value{"id": strVal("p2"), "status": strVal("complete")}),
					hashVal(map[string]Value{"id": strVal("p3"), "status": strVal("active")}),
				),
			},
			want: hashVal(map[string]Value{
				"active": arrayVal(
					hashVal(map[string]Value{"id": strVal("p1"), "status": strVal("active")}),
					hashVal(map[string]Value{"id": strVal("p3"), "status": strVal("active")}),
				),
				"complete": arrayVal(
					hashVal(map[string]Value{"id": strVal("p2"), "status": strVal("complete")}),
				),
			}),
		},
		{
			name:     "arrays/tally_statuses",
			file:     "arrays/extras.vibe",
			function: "tally_statuses",
			args: []Value{
				arrayVal(strVal("active"), strVal("active"), strVal("complete")),
			},
			want: hashVal(map[string]Value{
				"active":   intVal(2),
				"complete": intVal(1),
			}),
		},
		{
			name:     "collections/make_player",
			file:     "collections/hashes.vibe",
			function: "make_player",
			args:     []Value{strVal("aria"), intVal(1000)},
			want: hashVal(map[string]Value{
				"name":   strVal("aria"),
				"goal":   intVal(1000),
				"raised": intVal(0),
				"status": strVal("active"),
			}),
		},
		{
			name:     "collections/mark_complete",
			file:     "collections/hashes.vibe",
			function: "mark_complete",
			args: []Value{
				hashVal(map[string]Value{
					"name":   strVal("aria"),
					"status": strVal("active"),
				}),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("aria"),
				"status": strVal("complete"),
			}),
		},
		{
			name:     "collections/total_with_bonus",
			file:     "collections/hashes.vibe",
			function: "total_with_bonus",
			args: []Value{
				hashVal(map[string]Value{
					"raised": intVal(950),
				}),
				intVal(50),
			},
			want: intVal(1000),
		},
		{
			name:     "collections/nested_lookup",
			file:     "collections/hashes.vibe",
			function: "nested_lookup",
			args: []Value{
				hashVal(map[string]Value{
					"meta": hashVal(map[string]Value{
						"tag": strVal("summer"),
					}),
				}),
			},
			want: strVal("summer"),
		},
		{
			name:     "collections/permission_keys",
			file:     "collections/symbols.vibe",
			function: "permission_keys",
			want: arrayVal(
				symbolVal("read"),
				symbolVal("write"),
				symbolVal("delete"),
			),
		},
		{
			name:     "collections/fetch_symbol_key",
			file:     "collections/symbols.vibe",
			function: "fetch_symbol_key",
			args: []Value{
				hashVal(map[string]Value{
					"read":  boolVal(true),
					"write": boolVal(false),
				}),
				symbolVal("write"),
			},
			want: boolVal(false),
		},
		{
			name:     "conditionals/fundraising_badge_legend",
			file:     "control_flow/conditionals.vibe",
			function: "fundraising_badge",
			args:     []Value{intVal(150_000)},
			want:     strVal("legend"),
		},
		{
			name:     "conditionals/fundraising_badge_elite",
			file:     "control_flow/conditionals.vibe",
			function: "fundraising_badge",
			args:     []Value{intVal(12_000)},
			want:     strVal("elite"),
		},
		{
			name:     "conditionals/fundraising_badge_gold",
			file:     "control_flow/conditionals.vibe",
			function: "fundraising_badge",
			args:     []Value{intVal(2_500)},
			want:     strVal("gold"),
		},
		{
			name:     "conditionals/fundraising_badge_silver",
			file:     "control_flow/conditionals.vibe",
			function: "fundraising_badge",
			args:     []Value{intVal(700)},
			want:     strVal("silver"),
		},
		{
			name:     "conditionals/fundraising_badge_bronze",
			file:     "control_flow/conditionals.vibe",
			function: "fundraising_badge",
			args:     []Value{intVal(150)},
			want:     strVal("bronze"),
		},
		{
			name:     "conditionals/choose_label_active",
			file:     "control_flow/conditionals.vibe",
			function: "choose_label",
			args:     []Value{strVal("active")},
			want:     strVal("needs attention"),
		},
		{
			name:     "conditionals/choose_label_complete",
			file:     "control_flow/conditionals.vibe",
			function: "choose_label",
			args:     []Value{strVal("complete")},
			want:     strVal("done"),
		},
		{
			name:     "conditionals/choose_label_other",
			file:     "control_flow/conditionals.vibe",
			function: "choose_label",
			args:     []Value{strVal("blocked")},
			want:     strVal("unknown"),
		},
		{
			name:     "recursion/factorial_five",
			file:     "control_flow/recursion.vibe",
			function: "factorial",
			args:     []Value{intVal(5)},
			want:     intVal(120),
		},
		{
			name:     "recursion/factorial_zero",
			file:     "control_flow/recursion.vibe",
			function: "factorial",
			args:     []Value{intVal(0)},
			want:     intVal(1),
		},
		{
			name:     "recursion/fibonacci_six",
			file:     "control_flow/recursion.vibe",
			function: "fibonacci",
			args:     []Value{intVal(6)},
			want:     intVal(8),
		},
		{
			name:     "recursion/fibonacci_one",
			file:     "control_flow/recursion.vibe",
			function: "fibonacci",
			args:     []Value{intVal(1)},
			want:     intVal(1),
		},
		{
			name:     "loops/sum_range",
			file:     "loops/iteration.vibe",
			function: "sum_range",
			args:     []Value{intVal(5)},
			want:     intVal(15),
		},
		{
			name:     "loops/product_range",
			file:     "loops/iteration.vibe",
			function: "product_range",
			args:     []Value{intVal(4)},
			want:     intVal(24),
		},
		{
			name:     "loops/countdown_total",
			file:     "loops/iteration.vibe",
			function: "countdown_total",
			args:     []Value{intVal(5)},
			want:     intVal(15),
		},
		{
			name:     "loops/total_with_each",
			file:     "loops/iteration.vibe",
			function: "total_with_each",
			args: []Value{
				arrayVal(intVal(1), intVal(3), intVal(5)),
			},
			want: intVal(9),
		},
		{
			name:     "loops/double_values",
			file:     "loops/iteration.vibe",
			function: "double_values",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3)),
			},
			want: arrayVal(intVal(2), intVal(4), intVal(6)),
		},
		{
			name:     "loops/select_above",
			file:     "loops/iteration.vibe",
			function: "select_above",
			args: []Value{
				arrayVal(intVal(8), intVal(11), intVal(15)),
				intVal(10),
			},
			want: arrayVal(intVal(11), intVal(15)),
		},
		{
			name:     "loops/reduce_sum",
			file:     "loops/iteration.vibe",
			function: "reduce_sum",
			args: []Value{
				arrayVal(intVal(4), intVal(6), intVal(11)),
			},
			want: intVal(21),
		},
		{
			name:     "loops/sum_of_products",
			file:     "loops/advanced.vibe",
			function: "sum_of_products",
			args: []Value{
				intVal(2),
				intVal(3),
			},
			want: intVal(18),
		},
		{
			name:     "loops/accumulate_until",
			file:     "loops/advanced.vibe",
			function: "accumulate_until",
			args: []Value{
				intVal(10),
				intVal(12),
			},
			want: intVal(15),
		},
		{
			name:     "loops/find_first_divisible",
			file:     "loops/advanced.vibe",
			function: "find_first_divisible",
			args: []Value{
				intVal(10),
				intVal(4),
			},
			want: intVal(4),
		},
		{
			name:     "loops/find_first_divisible_none",
			file:     "loops/advanced.vibe",
			function: "find_first_divisible",
			args: []Value{
				intVal(5),
				intVal(7),
			},
			want: nilVal(),
		},
		{
			name:     "ranges/inclusive_range_sum",
			file:     "ranges/usage.vibe",
			function: "inclusive_range_sum",
			args: []Value{
				intVal(3),
				intVal(6),
			},
			want: intVal(18),
		},
		{
			name:     "ranges/descending_range_collect",
			file:     "ranges/usage.vibe",
			function: "descending_range_collect",
			args: []Value{
				intVal(5),
				intVal(2),
			},
			want: strVal("5,4,3,2"),
		},
		{
			name:     "ranges/range_even_numbers",
			file:     "ranges/usage.vibe",
			function: "range_even_numbers",
			args: []Value{
				intVal(1),
				intVal(10),
			},
			want: strVal("2,4,6,8,10"),
		},
		{
			name:     "time/after_time",
			file:     "time/duration.vibe",
			function: "after_time",
			args:     []Value{strVal("2024-01-01T00:00:00Z")},
			want:     strVal("2024-01-01T00:05:00Z"),
		},
		{
			name:     "time/ago_time",
			file:     "time/duration.vibe",
			function: "ago_time",
			args:     []Value{strVal("2024-01-01T02:00:00Z")},
			want:     strVal("2024-01-01T00:00:00Z"),
		},
		{
			name:     "time/duration_parts",
			file:     "time/duration.vibe",
			function: "duration_parts",
			want: hashVal(map[string]Value{
				"iso": strVal("P1DT1H1M1S"),
				"parts": hashVal(map[string]Value{
					"days":    intVal(1),
					"hours":   intVal(1),
					"minutes": intVal(1),
					"seconds": intVal(1),
				}),
			}),
		},
		{
			name:     "time/duration_parse_build",
			file:     "time/duration.vibe",
			function: "duration_parse_build",
			want:     strVal("PT1M30S"),
		},
		{
			name:     "time/duration_to_i",
			file:     "time/duration.vibe",
			function: "duration_to_i",
			want:     intVal(88200),
		},
		{
			name:     "time/duration_until",
			file:     "time/duration.vibe",
			function: "duration_until",
			args:     []Value{strVal("2024-01-01T01:30:00Z")},
			want:     strVal("2024-01-01T00:00:00Z"),
		},
		{
			name:     "time/duration_math",
			file:     "time/duration.vibe",
			function: "duration_math",
			want: hashVal(map[string]Value{
				"add":             strVal("PT2H4S"),
				"subtract":        strVal("PT1H59M56S"),
				"multiply":        strVal("PT30S"),
				"multiply_left":   strVal("PT30S"),
				"divide":          strVal("PT5S"),
				"divide_duration": floatVal(2.5),
				"modulo":          strVal("PT2S"),
				"compare": hashVal(map[string]Value{
					"lt": boolVal(true),
					"eq": boolVal(true),
					"gt": boolVal(true),
				}),
			}),
		},
		{
			name:     "time/duration_to_i_math",
			file:     "time/duration.vibe",
			function: "duration_to_i_math",
			want:     intVal(8),
		},
		{
			name:     "loops/sum_matrix",
			file:     "loops/advanced.vibe",
			function: "sum_matrix",
			args: []Value{
				arrayVal(
					arrayVal(intVal(1), intVal(2), intVal(3)),
					arrayVal(intVal(4), intVal(5), intVal(6)),
				),
			},
			want: intVal(21),
		},
		{
			name:     "loops/fizzbuzz",
			file:     "loops/fizzbuzz.vibe",
			function: "fizzbuzz",
			args:     []Value{intVal(5)},
			want:     strVal("1\n2\nFizz\n4\nBuzz\n"),
		},
		{
			name:     "blocks/double_each",
			file:     "blocks/transformations.vibe",
			function: "double_each",
			args: []Value{
				arrayVal(intVal(1), intVal(2), intVal(3)),
			},
			want: arrayVal(intVal(2), intVal(4), intVal(6)),
		},
		{
			name:     "blocks/select_large_donations",
			file:     "blocks/transformations.vibe",
			function: "select_large_donations",
			args: []Value{
				arrayVal(
					hashVal(map[string]Value{"amount": mustMoney("25.00 USD")}),
					hashVal(map[string]Value{"amount": mustMoney("75.00 USD")}),
					hashVal(map[string]Value{"amount": mustMoney("120.00 USD")}),
				),
			},
			want: arrayVal(
				hashVal(map[string]Value{"amount": mustMoney("75.00 USD")}),
				hashVal(map[string]Value{"amount": mustMoney("120.00 USD")}),
			),
		},
		{
			name:     "blocks/total_scores",
			file:     "blocks/transformations.vibe",
			function: "total_scores",
			args: []Value{
				arrayVal(intVal(10), intVal(5), intVal(7)),
			},
			want: intVal(22),
		},
		{
			name:     "blocks/count_active",
			file:     "blocks/advanced.vibe",
			function: "count_active",
			args: []Value{
				arrayVal(
					hashVal(map[string]Value{
						"name":   strVal("alpha"),
						"active": boolVal(true),
					}),
					hashVal(map[string]Value{
						"name":   strVal("beta"),
						"active": boolVal(false),
					}),
					hashVal(map[string]Value{
						"name":   strVal("gamma"),
						"active": boolVal(true),
					}),
				),
			},
			want: intVal(2),
		},
		{
			name:     "blocks/normalize_donations",
			file:     "blocks/advanced.vibe",
			function: "normalize_donations",
			args: []Value{
				arrayVal(
					hashVal(map[string]Value{
						"id":     strVal("p1"),
						"amount": mustMoney("25.00 USD"),
					}),
					hashVal(map[string]Value{
						"id":     strVal("p2"),
						"amount": mustMoney("40.50 USD"),
					}),
				),
			},
			want: arrayVal(
				hashVal(map[string]Value{
					"id":        strVal("p1"),
					"cents":     intVal(2500),
					"formatted": strVal("25.00 USD"),
				}),
				hashVal(map[string]Value{
					"id":        strVal("p2"),
					"cents":     intVal(4050),
					"formatted": strVal("40.50 USD"),
				}),
			),
		},
		{
			name:     "blocks/max_score",
			file:     "blocks/advanced.vibe",
			function: "max_score",
			args: []Value{
				arrayVal(intVal(7), intVal(12), intVal(9)),
			},
			want: intVal(12),
		},
		{
			name:     "blocks/any_large_predicate",
			file:     "blocks/advanced.vibe",
			function: "any_large?",
			args: []Value{
				arrayVal(intVal(3), intVal(15), intVal(8)),
				intVal(10),
			},
			want: boolVal(true),
		},
		{
			name:     "hashes/merge_defaults",
			file:     "hashes/operations.vibe",
			function: "merge_defaults",
			args: []Value{
				hashVal(map[string]Value{
					"name":   strVal("Alex"),
					"raised": mustMoney("25.00 USD"),
				}),
				hashVal(map[string]Value{
					"name":   strVal("Unknown"),
					"goal":   intVal(1000),
					"raised": mustMoney("0.00 USD"),
				}),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("Alex"),
				"goal":   intVal(1000),
				"raised": mustMoney("25.00 USD"),
			}),
		},
		{
			name:     "hashes/merge_with_override",
			file:     "hashes/operations.vibe",
			function: "merge_with_override",
			args: []Value{
				hashVal(map[string]Value{
					"name":   strVal("Alex"),
					"raised": mustMoney("25.00 USD"),
				}),
				hashVal(map[string]Value{
					"raised": mustMoney("40.00 USD"),
				}),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("Alex"),
				"raised": mustMoney("40.00 USD"),
			}),
		},
		{
			name:     "hashes/symbolize_report",
			file:     "hashes/operations.vibe",
			function: "symbolize_report",
			args: []Value{
				arrayVal(
					hashVal(map[string]Value{
						"id":     strVal("p1"),
						"name":   strVal("Alex"),
						"raised": mustMoney("50.00 USD"),
					}),
					hashVal(map[string]Value{
						"id":     strVal("p2"),
						"name":   strVal("Maya"),
						"raised": mustMoney("75.00 USD"),
					}),
				),
			},
			want: hashVal(map[string]Value{
				"p1": hashVal(map[string]Value{
					"name":   strVal("Alex"),
					"raised": mustMoney("50.00 USD"),
				}),
				"p2": hashVal(map[string]Value{
					"name":   strVal("Maya"),
					"raised": mustMoney("75.00 USD"),
				}),
			}),
		},
		{
			name:     "hashes/deep_fetch_or_existing",
			file:     "hashes/operations.vibe",
			function: "deep_fetch_or",
			args: []Value{
				hashVal(map[string]Value{
					"p1": hashVal(map[string]Value{
						"name":   strVal("Alex"),
						"raised": mustMoney("30.00 USD"),
						"meta": hashVal(map[string]Value{
							"display_name": strVal("Alex P."),
						}),
					}),
				}),
				strVal("p1"),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("Alex P."),
				"raised": mustMoney("30.00 USD"),
			}),
		},
		{
			name:     "hashes/deep_fetch_or_missing",
			file:     "hashes/operations.vibe",
			function: "deep_fetch_or",
			args: []Value{
				hashVal(map[string]Value{}),
				strVal("p9"),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("unknown"),
				"raised": mustMoney("0.00 USD"),
			}),
		},
		{
			name:     "hashes/tally_statuses",
			file:     "hashes/operations.vibe",
			function: "tally_statuses",
			args: []Value{
				arrayVal(
					hashVal(map[string]Value{"status": strVal("active")}),
					hashVal(map[string]Value{"status": strVal("active")}),
					hashVal(map[string]Value{"status": strVal("complete")}),
				),
			},
			want: hashVal(map[string]Value{
				"active":   intVal(2),
				"complete": intVal(1),
			}),
		},
		{
			name:     "hashes/hash_flags",
			file:     "hashes/operations.vibe",
			function: "hash_flags",
			args: []Value{
				hashVal(map[string]Value{
					"name":   strVal("Alex"),
					"raised": mustMoney("20.00 USD"),
				}),
			},
			want: hashVal(map[string]Value{
				"size":            intVal(2),
				"length":          intVal(2),
				"empty":           boolVal(false),
				"has_name":        boolVal(true),
				"has_goal":        boolVal(false),
				"includes_raised": boolVal(true),
			}),
		},
		{
			name:     "hashes/rename_keys",
			file:     "hashes/transformations.vibe",
			function: "rename_keys",
			args: []Value{
				hashVal(map[string]Value{
					"name":   strVal("Alex"),
					"raised": mustMoney("20.00 USD"),
				}),
				hashVal(map[string]Value{
					"name": symbolVal("full_name"),
				}),
			},
			want: hashVal(map[string]Value{
				"full_name": strVal("Alex"),
				"raised":    mustMoney("20.00 USD"),
			}),
		},
		{
			name:     "hashes/compact_hash",
			file:     "hashes/transformations.vibe",
			function: "compact_hash",
			args: []Value{
				hashVal(map[string]Value{
					"name":   strVal("Alex"),
					"goal":   nilVal(),
					"raised": mustMoney("20.00 USD"),
				}),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("Alex"),
				"raised": mustMoney("20.00 USD"),
			}),
		},
		{
			name:     "hashes/select_keys",
			file:     "hashes/transformations.vibe",
			function: "select_keys",
			args: []Value{
				hashVal(map[string]Value{
					"name":   strVal("Alex"),
					"goal":   intVal(500),
					"raised": mustMoney("20.00 USD"),
				}),
				arrayVal(symbolVal("name"), symbolVal("raised")),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("Alex"),
				"raised": mustMoney("20.00 USD"),
			}),
		},
		{
			name:     "hashes/public_fields",
			file:     "hashes/transformations.vibe",
			function: "public_fields",
			args: []Value{
				hashVal(map[string]Value{
					"name":     strVal("Alex"),
					"goal":     intVal(500),
					"raised":   mustMoney("20.00 USD"),
					"internal": strVal("sensitive"),
				}),
			},
			want: hashVal(map[string]Value{
				"name":   strVal("Alex"),
				"goal":   intVal(500),
				"raised": mustMoney("20.00 USD"),
			}),
		},
		{
			name:     "hashes/without_private",
			file:     "hashes/transformations.vibe",
			function: "without_private",
			args: []Value{
				hashVal(map[string]Value{
					"name":     strVal("Alex"),
					"token":    strVal("secret"),
					"internal": strVal("sensitive"),
				}),
			},
			want: hashVal(map[string]Value{
				"name": strVal("Alex"),
			}),
		},
		{
			name:     "hashes/normalize_values",
			file:     "hashes/transformations.vibe",
			function: "normalize_values",
			args: []Value{
				hashVal(map[string]Value{
					"active":   intVal(1),
					"complete": nilVal(),
				}),
			},
			want: hashVal(map[string]Value{
				"active":   intVal(1),
				"complete": intVal(0),
			}),
		},
		{
			name:     "hashes/active_only",
			file:     "hashes/transformations.vibe",
			function: "active_only",
			args: []Value{
				hashVal(map[string]Value{
					"p1": strVal("active"),
					"p2": strVal("complete"),
					"p3": strVal("active"),
				}),
			},
			want: hashVal(map[string]Value{
				"p1": strVal("active"),
				"p3": strVal("active"),
			}),
		},
		{
			name:     "hashes/non_zero",
			file:     "hashes/transformations.vibe",
			function: "non_zero",
			args: []Value{
				hashVal(map[string]Value{
					"a": intVal(0),
					"b": intVal(2),
					"c": intVal(0),
					"d": intVal(3),
				}),
			},
			want: hashVal(map[string]Value{
				"b": intVal(2),
				"d": intVal(3),
			}),
		},
		{
			name:     "blocks/total_raised_by_currency",
			file:     "blocks/enumerable_reports.vibe",
			function: "total_raised_by_currency",
			args: []Value{
				arrayVal(
					hashVal(map[string]Value{
						"amount": mustMoney("10.00 USD"),
					}),
					hashVal(map[string]Value{
						"amount": mustMoney("5.50 USD"),
					}),
					hashVal(map[string]Value{
						"amount": mustMoney("7.25 EUR"),
					}),
				),
			},
			want: hashVal(map[string]Value{
				"USD": mustMoney("15.50 USD"),
				"EUR": mustMoney("7.25 EUR"),
			}),
		},
		{
			name:     "durations/reminder_delay_seconds",
			file:     "durations/durations.vibe",
			function: "reminder_delay_seconds",
			want:     intVal(300),
		},
		{
			name:     "durations/event_window",
			file:     "durations/durations.vibe",
			function: "event_window",
			want:     durationVal(7_200),
		},
		{
			name:     "durations/combine_delay_seconds",
			file:     "durations/durations.vibe",
			function: "combine_delay_seconds",
			args: []Value{
				durationVal(90),
				durationVal(45),
			},
			want: intVal(135),
		},
		{
			name:     "errors/ensure_positive",
			file:     "errors/assertions.vibe",
			function: "ensure_positive",
			args:     []Value{intVal(50)},
			want:     intVal(50),
		},
		{
			name:     "errors/ensure_positive_fail",
			file:     "errors/assertions.vibe",
			function: "ensure_positive",
			args:     []Value{intVal(-10)},
			wantErr:  "amount must be positive",
		},
		{
			name:     "errors/ensure_currency",
			file:     "errors/assertions.vibe",
			function: "ensure_currency",
			args: []Value{
				objectVal(map[string]Value{
					"currency": strVal("USD"),
				}),
			},
			want: objectVal(map[string]Value{
				"currency": strVal("USD"),
			}),
		},
		{
			name:     "errors/ensure_currency_fail",
			file:     "errors/assertions.vibe",
			function: "ensure_currency",
			args: []Value{
				objectVal(map[string]Value{
					"currency": strVal("CAD"),
				}),
			},
			wantErr: "only USD pledges supported",
		},
		{
			name:     "errors/guard_present",
			file:     "errors/assertions.vibe",
			function: "guard_present",
			args:     []Value{strVal("ready")},
			want:     strVal("ready"),
		},
		{
			name:     "errors/guard_present_fail",
			file:     "errors/assertions.vibe",
			function: "guard_present",
			args:     []Value{nilVal()},
			wantErr:  "assertion failed",
		},
		{
			name:     "money/add_pledges",
			file:     "money/operations.vibe",
			function: "add_pledges",
			want:     mustMoney("62.50 USD"),
		},
		{
			name:     "money/net_after_fee",
			file:     "money/operations.vibe",
			function: "net_after_fee",
			args:     []Value{intVal(1_000)},
			want:     mustMoney("8.25 USD"),
		},
		{
			name:     "money/exceeds_goal_true",
			file:     "money/operations.vibe",
			function: "exceeds_goal?",
			args: []Value{
				mustMoney("125.00 USD"),
				mustMoney("100.00 USD"),
			},
			want: boolVal(true),
		},
		{
			name:     "money/exceeds_goal_false",
			file:     "money/operations.vibe",
			function: "exceeds_goal?",
			args: []Value{
				mustMoney("80.00 USD"),
				mustMoney("100.00 USD"),
			},
			want: boolVal(false),
		},
		{
			name:     "context/current_user_id",
			file:     "capabilities/context_access.vibe",
			function: "current_user_id",
			prepare: func(t *testing.T) *exampleEnv {
				return &exampleEnv{
					Globals: map[string]Value{
						"ctx": ctxValue("player-1", "coach"),
					},
				}
			},
			want: strVal("player-1"),
		},
		{
			name:     "context/coach_true",
			file:     "capabilities/context_access.vibe",
			function: "coach?",
			prepare: func(t *testing.T) *exampleEnv {
				return &exampleEnv{
					Globals: map[string]Value{
						"ctx": ctxValue("player-2", "coach"),
					},
				}
			},
			want: boolVal(true),
		},
		{
			name:     "context/coach_false",
			file:     "capabilities/context_access.vibe",
			function: "coach?",
			prepare: func(t *testing.T) *exampleEnv {
				return &exampleEnv{
					Globals: map[string]Value{
						"ctx": ctxValue("player-3", "member"),
					},
				}
			},
			want: boolVal(false),
		},
		{
			name:     "policies/can_edit_player_role",
			file:     "policies/access_control.vibe",
			function: "can_edit_player?",
			prepare: func(t *testing.T) *exampleEnv {
				return &exampleEnv{
					Globals: map[string]Value{
						"ctx": ctxValue("user-9", "coach"),
					},
				}
			},
			args: []Value{
				hashVal(map[string]Value{
					"created_by": strVal("other"),
					"status":     strVal("active"),
				}),
			},
			want: boolVal(true),
		},
		{
			name:     "policies/can_edit_player_owner",
			file:     "policies/access_control.vibe",
			function: "can_edit_player?",
			prepare: func(t *testing.T) *exampleEnv {
				return &exampleEnv{
					Globals: map[string]Value{
						"ctx": ctxValue("owner-1", "member"),
					},
				}
			},
			args: []Value{
				hashVal(map[string]Value{
					"created_by": strVal("owner-1"),
					"status":     strVal("active"),
				}),
			},
			want: boolVal(true),
		},
		{
			name:     "policies/can_edit_player_denied",
			file:     "policies/access_control.vibe",
			function: "can_edit_player?",
			prepare: func(t *testing.T) *exampleEnv {
				return &exampleEnv{
					Globals: map[string]Value{
						"ctx": ctxValue("viewer-1", "member"),
					},
				}
			},
			args: []Value{
				hashVal(map[string]Value{
					"created_by": strVal("owner-2"),
					"status":     strVal("active"),
				}),
			},
			want: boolVal(false),
		},
		{
			name:     "policies/can_view_player_active",
			file:     "policies/access_control.vibe",
			function: "can_view_player?",
			args: []Value{
				hashVal(map[string]Value{
					"status": strVal("active"),
				}),
			},
			want: boolVal(true),
		},
		{
			name:     "policies/can_view_player_archived",
			file:     "policies/access_control.vibe",
			function: "can_view_player?",
			args: []Value{
				hashVal(map[string]Value{
					"status": strVal("archived"),
				}),
			},
			want: boolVal(false),
		},
		{
			name:     "database/load_player",
			file:     "capabilities/database_queries.vibe",
			function: "load_player",
			prepare: func(t *testing.T) *exampleEnv {
				db := newDBMock(t)
				db.findFunc = func(args []Value, kwargs map[string]Value) (Value, error) {
					if len(args) != 2 {
						t.Fatalf("expected 2 args for db.find, got %d", len(args))
					}
					if args[0].Kind() != KindString || args[0].String() != "Player" {
						t.Fatalf("unexpected collection %v", args[0])
					}
					if args[1].String() != "player-7" {
						t.Fatalf("unexpected id %s", args[1].String())
					}
					return hashVal(map[string]Value{
						"id":     strVal("player-7"),
						"raised": mustMoney("25.00 USD"),
					}), nil
				}
				return &exampleEnv{
					Globals: map[string]Value{"db": db.Value()},
					db:      db,
				}
			},
			args: []Value{strVal("player-7")},
			want: hashVal(map[string]Value{
				"id":     strVal("player-7"),
				"raised": mustMoney("25.00 USD"),
			}),
		},
		{
			name:     "database/top_players",
			file:     "capabilities/database_queries.vibe",
			function: "top_players",
			prepare: func(t *testing.T) *exampleEnv {
				db := newDBMock(t)
				db.queryFunc = func(args []Value, kwargs map[string]Value) (Value, error) {
					if len(args) != 1 || args[0].String() != "Player" {
						t.Fatalf("unexpected query args: %#v", args)
					}
					limit, ok := kwargs["limit"]
					if !ok || limit.Int() != 3 {
						t.Fatalf("expected limit 3, got %v", limit)
					}
					return arrayVal(
						hashVal(map[string]Value{"id": strVal("p1")}),
						hashVal(map[string]Value{"id": strVal("p2")}),
						hashVal(map[string]Value{"id": strVal("p3")}),
					), nil
				}
				return &exampleEnv{
					Globals: map[string]Value{"db": db.Value()},
					db:      db,
				}
			},
			args: []Value{intVal(3)},
			want: arrayVal(
				hashVal(map[string]Value{"id": strVal("p1")}),
				hashVal(map[string]Value{"id": strVal("p2")}),
				hashVal(map[string]Value{"id": strVal("p3")}),
			),
		},
		{
			name:     "database/increment_total",
			file:     "capabilities/database_queries.vibe",
			function: "increment_total",
			prepare: func(t *testing.T) *exampleEnv {
				db := newDBMock(t)
				db.findFunc = func(args []Value, kwargs map[string]Value) (Value, error) {
					return hashVal(map[string]Value{
						"raised": mustMoney("10.00 USD"),
					}), nil
				}
				db.updateFunc = func(args []Value, kwargs map[string]Value) (Value, error) {
					return NewNil(), nil
				}
				return &exampleEnv{
					Globals: map[string]Value{"db": db.Value()},
					db:      db,
				}
			},
			args: []Value{
				strVal("player-9"),
				mustMoney("5.00 USD"),
			},
			want: nilVal(),
			after: func(t *testing.T, env *exampleEnv, _ Value, err error) {
				if err != nil {
					return
				}
				if len(env.db.updateCalls) != 1 {
					t.Fatalf("expected 1 update call, got %d", len(env.db.updateCalls))
				}
				call := env.db.updateCalls[0]
				if len(call.args) != 3 {
					t.Fatalf("expected 3 update args, got %d", len(call.args))
				}
				if call.args[0].String() != "Player" || call.args[1].String() != "player-9" {
					t.Fatalf("unexpected update target: %v", call.args)
				}
				payload := call.args[2].Hash()
				updated, ok := payload["raised"]
				if !ok {
					t.Fatalf("expected raised in payload")
				}
				assertValueEqual(t, updated, mustMoney("15.00 USD"))
			},
		},
		{
			name:     "background/queue_recalc",
			file:     "background/jobs_and_events.vibe",
			function: "queue_recalc",
			prepare: func(t *testing.T) *exampleEnv {
				jobs := newJobsMock()
				return &exampleEnv{
					Globals: map[string]Value{"jobs": jobs.Value()},
					jobs:    jobs,
				}
			},
			args: []Value{strVal("player-12")},
			want: nilVal(),
			after: func(t *testing.T, env *exampleEnv, _ Value, err error) {
				if err != nil {
					return
				}
				if len(env.jobs.enqueueCalls) != 1 {
					t.Fatalf("expected 1 enqueue call, got %d", len(env.jobs.enqueueCalls))
				}
				call := env.jobs.enqueueCalls[0]
				if len(call.args) != 2 {
					t.Fatalf("expected 2 enqueue args, got %d", len(call.args))
				}
				assertValueEqual(t, call.args[0], strVal("recalc_player_total"))
				payload := call.args[1].Hash()
				playerID, ok := payload["player_id"]
				if !ok {
					t.Fatalf("missing player_id payload")
				}
				assertValueEqual(t, playerID, strVal("player-12"))
				delay, ok := call.kwargs["delay"]
				if !ok {
					t.Fatalf("missing delay kwarg")
				}
				assertValueEqual(t, delay, durationVal(2))
				key, ok := call.kwargs["key"]
				if !ok {
					t.Fatalf("missing key kwarg")
				}
				assertValueEqual(t, key, strVal("recalc:player-12"))
			},
		},
		{
			name:     "background/publish_update",
			file:     "background/jobs_and_events.vibe",
			function: "publish_update",
			prepare: func(t *testing.T) *exampleEnv {
				events := newEventsMock()
				return &exampleEnv{
					Globals: map[string]Value{"events": events.Value()},
					events:  events,
				}
			},
			args: []Value{
				hashVal(map[string]Value{
					"id":     strVal("player-13"),
					"raised": mustMoney("42.00 USD"),
				}),
			},
			want: nilVal(),
			after: func(t *testing.T, env *exampleEnv, _ Value, err error) {
				if err != nil {
					return
				}
				if len(env.events.publishCalls) != 1 {
					t.Fatalf("expected 1 publish call, got %d", len(env.events.publishCalls))
				}
				call := env.events.publishCalls[0]
				if len(call.args) != 2 {
					t.Fatalf("expected 2 publish args, got %d", len(call.args))
				}
				assertValueEqual(t, call.args[0], strVal("players_totals"))
				payload := call.args[1].Hash()
				assertValueEqual(t, payload["id"], strVal("player-13"))
				recorded := payload["total"]
				formatted := valueToString(t, recorded)
				if formatted != "42.00 USD" {
					t.Fatalf("expected formatted total 42.00 USD, got %s", formatted)
				}
			},
		},
		{
			name:     "background/job_recalc_player_total",
			file:     "background/jobs_and_events.vibe",
			function: "job_recalc_player_total",
			prepare: func(t *testing.T) *exampleEnv {
				db := newDBMock(t)
				events := newEventsMock()
				db.sumFunc = func(args []Value, kwargs map[string]Value) (Value, error) {
					return mustMoney("55.00 USD"), nil
				}
				db.updateFunc = func(args []Value, kwargs map[string]Value) (Value, error) {
					return NewNil(), nil
				}
				return &exampleEnv{
					Globals: map[string]Value{
						"db":     db.Value(),
						"events": events.Value(),
					},
					db:     db,
					events: events,
				}
			},
			args: []Value{
				hashVal(map[string]Value{
					"player_id": strVal("player-21"),
				}),
			},
			want: nilVal(),
			after: func(t *testing.T, env *exampleEnv, _ Value, err error) {
				if err != nil {
					return
				}
				if len(env.db.sumCalls) != 1 {
					t.Fatalf("expected 1 sum call, got %d", len(env.db.sumCalls))
				}
				if len(env.db.updateCalls) != 1 {
					t.Fatalf("expected 1 update call, got %d", len(env.db.updateCalls))
				}
				update := env.db.updateCalls[0]
				payload := update.args[2].Hash()
				assertValueEqual(t, payload["raised"], mustMoney("55.00 USD"))
				if len(env.events.publishCalls) != 1 {
					t.Fatalf("expected 1 publish call, got %d", len(env.events.publishCalls))
				}
				payload = env.events.publishCalls[0].args[1].Hash()
				assertValueEqual(t, payload["id"], strVal("player-21"))
				if valueToString(t, payload["total"]) != "55.00 USD" {
					t.Fatalf("unexpected publish total")
				}
			},
		},
		{
			name:     "future/iteration_total_raised_for_player",
			file:     "future/iteration.vibe",
			function: "total_raised_for_player",
			prepare: func(t *testing.T) *exampleEnv {
				db := newDBMock(t)
				db.eachRows = []Value{
					hashVal(map[string]Value{"amount": mustMoney("10.00 USD")}),
					hashVal(map[string]Value{"amount": mustMoney("15.50 USD")}),
					hashVal(map[string]Value{"amount": mustMoney("5.25 USD")}),
				}
				return &exampleEnv{
					Globals: map[string]Value{"db": db.Value()},
					db:      db,
				}
			},
			args: []Value{strVal("player-99")},
			want: mustMoney("30.75 USD"),
			after: func(t *testing.T, env *exampleEnv, _ Value, err error) {
				if err != nil {
					return
				}
				if len(env.db.eachCalls) != 1 {
					t.Fatalf("expected 1 each call, got %d", len(env.db.eachCalls))
				}
			},
		},
		{
			name:     "strings/operations_run",
			file:     "strings/operations.vibe",
			function: "run",
			want: hashVal(map[string]Value{
				"normalized":          strVal("vibescript example"),
				"bytesize":            intVal(3),
				"ord":                 intVal(104),
				"chr":                 strVal("h"),
				"upper":               strVal("VIBESCRIPT EXAMPLE"),
				"lower":               strVal("vibescript example"),
				"capitalize":          strVal("Héllo world"),
				"swapcase":            strVal("hELLO vibe"),
				"reverse":             strVal("olléh"),
				"sub":                 strVal("baNAnas"),
				"gsub":                strVal("baNANAs"),
				"sub_regex":           strVal("X ID-34"),
				"gsub_regex":          strVal("X X"),
				"match":               arrayVal(strVal("ID-12"), strVal("12")),
				"scan":                arrayVal(strVal("ID-12"), strVal("ID-34")),
				"strip_bang":          strVal("hello"),
				"strip_bang_nochange": nilVal(),
				"immutable_after":     strVal("  hello  "),
				"clear":               strVal(""),
				"concat":              strVal("hello!"),
				"concat_noop":         strVal("hello"),
				"replace":             strVal("new"),
				"sub_bang":            strVal("baNAnas"),
				"gsub_bang":           strVal("baNANAs"),
				"lstrip":              strVal("hello\t"),
				"rstrip":              strVal("\thello"),
				"chomp_default":       strVal("line"),
				"chomp_custom":        strVal("path//"),
				"delete_prefix":       strVal("happy"),
				"delete_suffix":       strVal("report"),
				"split_default":       arrayVal(strVal("one"), strVal("two"), strVal("three")),
				"split_custom":        arrayVal(strVal("a"), strVal("b"), strVal("c")),
				"tags":                arrayVal(strVal("ruby"), strVal("go"), strVal("vibescript")),
				"empty_true":          boolVal(true),
				"empty_false":         boolVal(false),
				"starts":              boolVal(true),
				"ends":                boolVal(true),
				"include":             boolVal(true),
				"index":               intVal(2),
				"rindex":              intVal(8),
				"slice_char":          strVal("é"),
				"slice_range":         strVal("éllo"),
				"length":              intVal(5),
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skip {
				t.Skip("example is pending implementation")
			}
			script := compileExample(t, tc.file)
			if _, ok := script.Function(tc.function); !ok {
				t.Fatalf("function %s not found in %s", tc.function, tc.file)
			}
			var env *exampleEnv
			if tc.prepare != nil {
				env = tc.prepare(t)
			}
			if env == nil {
				env = &exampleEnv{}
			}
			opts := CallOptions{}
			if env.Globals != nil {
				opts.Globals = env.Globals
			}
			result, err := script.Call(context.Background(), tc.function, tc.args, opts)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err.Error())
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				assertValueEqual(t, result, tc.want)
			}
			if tc.after != nil {
				tc.after(t, env, result, err)
			}
		})
	}
}

func compileExample(t *testing.T, rel string) *Script {
	return compileExampleWithConfig(t, rel, Config{})
}

func compileExampleWithConfig(t *testing.T, rel string, cfg Config) *Script {
	t.Helper()
	path := filepath.Join("..", "examples", rel)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	engine := MustNewEngine(cfg)
	script, err := engine.Compile(string(source))
	if err != nil {
		t.Fatalf("compile %s: %v", rel, err)
	}
	return script
}

func makeBuiltin(name string, fn builtinAdapter) Value {
	return NewBuiltin(name, func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return fn(args, kwargs)
	})
}

func intVal(n int64) Value { return NewInt(n) }

func floatVal(f float64) Value { return NewFloat(f) }

func strVal(s string) Value { return NewString(s) }

func boolVal(b bool) Value { return NewBool(b) }

func nilVal() Value { return NewNil() }

func arrayVal(elems ...Value) Value {
	cp := append([]Value{}, elems...)
	return NewArray(cp)
}

func hashVal(entries map[string]Value) Value {
	cp := make(map[string]Value, len(entries))
	maps.Copy(cp, entries)
	return NewHash(cp)
}

func objectVal(entries map[string]Value) Value {
	cp := make(map[string]Value, len(entries))
	maps.Copy(cp, entries)
	return NewObject(cp)
}

func symbolVal(name string) Value { return NewSymbol(name) }

func durationVal(seconds int64) Value { return NewDuration(Duration{seconds: seconds}) }

func mustMoney(lit string) Value {
	m, err := parseMoneyLiteral(lit)
	if err != nil {
		panic(err)
	}
	return NewMoney(m)
}

func ctxValue(id, role string) Value {
	return objectVal(map[string]Value{
		"user": objectVal(map[string]Value{
			"id":   strVal(id),
			"role": strVal(role),
		}),
	})
}

func assertValueEqual(t *testing.T, got, want Value) {
	t.Helper()
	if got.Kind() != want.Kind() {
		t.Fatalf("kind mismatch: got %v want %v", got.Kind(), want.Kind())
	}
	switch got.Kind() {
	case KindNil:
		return
	case KindBool:
		if got.Bool() != want.Bool() {
			t.Fatalf("bool mismatch: got %v want %v", got.Bool(), want.Bool())
		}
	case KindInt:
		if got.Int() != want.Int() {
			t.Fatalf("int mismatch: got %d want %d", got.Int(), want.Int())
		}
	case KindFloat:
		if got.Float() != want.Float() {
			t.Fatalf("float mismatch: got %g want %g", got.Float(), want.Float())
		}
	case KindString, KindSymbol:
		if got.String() != want.String() {
			t.Fatalf("string mismatch: got %q want %q", got.String(), want.String())
		}
	case KindMoney:
		gm := got.Money()
		wm := want.Money()
		if gm.cents != wm.cents || gm.currency != wm.currency {
			t.Fatalf("money mismatch: got %s want %s", gm.String(), wm.String())
		}
	case KindDuration:
		if got.Duration().Seconds() != want.Duration().Seconds() {
			t.Fatalf("duration mismatch: got %d want %d", got.Duration().Seconds(), want.Duration().Seconds())
		}
	case KindTime:
		if !got.Time().Equal(want.Time()) {
			t.Fatalf("time mismatch: got %s want %s", got.Time(), want.Time())
		}
	case KindArray:
		gArr := got.Array()
		wArr := want.Array()
		if len(gArr) != len(wArr) {
			t.Fatalf("array length mismatch: got %d want %d", len(gArr), len(wArr))
		}
		for i := range gArr {
			assertValueEqual(t, gArr[i], wArr[i])
		}
	case KindHash, KindObject:
		gMap := got.Hash()
		wMap := want.Hash()
		if len(gMap) != len(wMap) {
			t.Fatalf("hash length mismatch: got %d want %d", len(gMap), len(wMap))
		}
		for key, wantVal := range wMap {
			gotVal, ok := gMap[key]
			if !ok {
				t.Fatalf("missing key %s", key)
			}
			assertValueEqual(t, gotVal, wantVal)
		}
	default:
		t.Fatalf("unsupported kind %v", got.Kind())
	}
}

func cloneValues(in []Value) []Value {
	if in == nil {
		return nil
	}
	out := make([]Value, len(in))
	copy(out, in)
	return out
}

func cloneKwargs(in map[string]Value) map[string]Value {
	if in == nil {
		return nil
	}
	out := make(map[string]Value, len(in))
	maps.Copy(out, in)
	return out
}

func valueToString(t *testing.T, v Value) string {
	t.Helper()
	switch v.Kind() {
	case KindString:
		return v.String()
	case KindBuiltin:
		out, err := v.Builtin().Fn(nil, NewNil(), nil, nil, NewNil())
		if err != nil {
			t.Fatalf("format builtin failed: %v", err)
		}
		return out.String()
	default:
		t.Fatalf("unexpected value kind %v for string", v.Kind())
		return ""
	}
}
