package vibes

import "testing"

func TestCoreSyntaxFreezeSnippetsCompile(t *testing.T) {
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
    tags: ["a", "b"]
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
  until i >= limit
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
			name: "require_with_alias_keyword",
			source: `def run(value)
  helpers = require("public/helpers", as: "helpers")
  helpers.normalize(value)
end`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_ = compileScriptWithEngine(t, engine, tc.source)
		})
	}
}
