package runtime

import "testing"

func TestCoreSyntaxFreezeSnippetsCompile(t *testing.T) {
	t.Parallel()
	engine := MustNewEngine(Config{})

	cases := []struct {
		name   string
		source string
	}{
		{
			name: "literals_and_collections",
			source: `def run
  payload = {
    active: true,
    count: 3,
    tags: ["a", "b"],
    :owner => "Ada",
    "first-name" => "Ada"
  }
  payload
end`,
		},
		{
			name: "typed_signature_with_defaults",
			source: `def run(amount: int, tax: int = 2) -> int
  amount + tax
end`,
		},
		{
			name: "assignment_targets",
			source: `def run
  a, *middle, last = [1, 2, 3, 4]
  record = {count: 0}
  record.count = middle[0]
  record[:count] += 1
  [a, record[:count], last]
end`,
		},
		{
			name: "class_definition_and_methods",
			source: `class Counter
  @@n = 0

  def self.bump
    @@n = @@n + 1
  end

  def value
    @@n
  end
end

def run
  Counter.bump
  Counter.new.value
end`,
		},
		{
			name: "for_range_and_blocks",
			source: `def run(values)
  total = 0
  for i in 1..3
    total = total + i
  end
  for i in 1...3 do
    total = total + i
  end

  selected = values.select do |v|
    v > 1
  end

  mapped = selected.map do |v|
    v * 2
  end

  { total: total, mapped: mapped }
end`,
		},
		{
			name: "until_with_if_guard",
			source: `def run(limit)
  total = 0
  i = 0
  until i >= limit do
    total = total + i
    i = i + 1
  end

  if total <= 0
    return 0
  end

  total
end`,
		},
		{
			name: "unless_conditionals",
			source: `def run(blocked)
  unless blocked
    "open"
  else
    "closed"
  end
end`,
		},
		{
			name: "begin_rescue_ensure",
			source: `def run(flag)
  begin
    if flag
      raise("boom")
    end
    "ok"
  rescue(RuntimeError)
    "rescued"
  ensure
    now()
  end
end`,
		},
		{
			name: "begin_rescue_ruby_style_binding",
			source: `def run(flag)
  begin
    if flag
      raise("boom")
    end
    "ok"
  rescue RuntimeError => err
    err.message
  ensure
    now()
  end
end`,
		},
		{
			name: "begin_rescue_else_ensure",
			source: `def run(flag)
  begin
    if flag
      raise("boom")
    end
    "ok"
  rescue(RuntimeError)
    "rescued"
  else
    "clean"
  ensure
    now()
  end
end`,
		},
		{
			name: "require_with_alias_keyword",
			source: `def run(value)
  helpers = require("public/helpers", as: "helpers")
  helpers.normalize(value)
end`,
		},
		{
			name: "spaceship_time_comparison",
			source: `def run
  Time.utc(2024, 1, 1) <=> Time.utc(2024, 1, 2)
end`,
		},
		{
			name: "word_boolean_operators",
			source: `def run
  not false and true or false
end`,
		},
		{
			name: "trailing_commas",
			source: `def passthrough(value, label:)
  value
end

def run
  passthrough([1, 2,], label: "ok",)
end`,
		},
		{
			name: "semicolon_statement_separators",
			source: `def run(flag)
  if flag; 1; else; 2; end
end`,
		},
		{
			name: "ternary_conditionals",
			source: `def run(flag)
  flag ? "enabled" : "disabled"
end`,
		},
		{
			name: "if_expressions",
			source: `def run(flag)
  label = if flag
    "enabled"
  else
    "disabled"
  end
  label
end`,
		},
		{
			name: "parenless_yield_args",
			source: `def caller
  yield 1, 2
end

def run
  caller do |a, b|
    a + b
  end
end`,
		},
		{
			name: "targetless_case_predicates",
			source: `def run(value)
  case
  when value == 1 then
    "one"
  when value == 2 then "two"
  when value == 3
    "three"
  else
    "other"
  end
end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = compileScriptWithEngine(t, engine, tc.source)
		})
	}
}
