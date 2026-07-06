# Migrating to Vibescript 1.0

Vibescript 1.0 aligns the language and the collection standard library with
Ruby semantics. Most scripts written for v0.50.x run unchanged, but a number of
behaviors changed incompatibly. This guide collects every breaking change in
one place: what changed, why, what breaks, and how to fix it.

The full list of 1.0 changes (including the many non-breaking additions) lives
in the [CHANGELOG](../CHANGELOG.md).

## 1. Arrays and hashes are mutable, with Ruby reference semantics

**What changed.** The Ruby-named collection mutators now mutate their receiver
in place instead of returning a modified copy. Arrays and hashes behave as
mutable objects with reference semantics: two variables bound to the same
collection observe each other's mutations.

- Array `push`/`append`, `prepend`/`unshift`, `<<`, `insert`, `fill`, `clear`,
  and `delete_if`/`keep_if` mutate and return the receiver.
- `pop`, `shift`, and `delete` mutate and return the removed value(s) — not the
  former `{ array:, popped: }`-style result hashes.
- `map!`, `sort!`, and `reverse!` transform in place and return the receiver;
  `select!`, `reject!`, `uniq!`, and `compact!` return `nil` when nothing
  changed.
- Hash `update`/`merge!` fold into the receiver, `store` is true index
  assignment returning the stored value, `delete` returns the removed value,
  `clear` empties in place, `delete_if`/`keep_if` prune in place, and `replace`
  adopts the argument's entries and default.

The non-mutating helpers (`map`, `select`, `merge`, `sort`, `+`, ...) are
unchanged, and strings remain immutable values (the string bang helpers keep
their value-or-`nil` contract).

**Why.** Ruby code and Ruby intuition expect `list.pop` to hand back the popped
element and `a << x` to grow `a` for every holder of `a`. The old
copy-returning model made ported Ruby snippets silently wrong.

**What breaks.**

Code written against the old result-hash shape:

```
values = [1, 2, 3]
result = values.pop
result[:array]    # old: [1, 2]
result[:popped]   # old: 3
```

And code that relied on mutators leaving the receiver untouched:

```
base = [1, 2]
extended = base.push(3)   # old: base stayed [1, 2]
```

**Fix.** Read the removed value directly and let the receiver mutate:

```vibe
values = [1, 2, 3]
popped = values.pop    # => 3
values                 # => [1, 2]

removed = { a: 1 }.delete(:a)   # => 1
```

Aliases now see each other's writes; use `dup`/`clone` to detach a copy when
you need the old snapshot behavior:

```vibe
base = [1, 2]
extended = base.dup.push(3)   # base stays [1, 2]
shared = base
shared << 9                   # base is now [1, 2, 9]
```

Two related caveats:

- Iteration helpers walk the elements captured at loop entry, so structural
  mutation inside a block never extends or shortens the in-flight loop.
- Host isolation is unchanged: call arguments and globals are still deep-cloned
  per call, so in-script mutation never leaks into host originals.

## 2. `equal?` is object identity; empty collections are distinct objects

**What changed.** `equal?` reports object identity, matching Ruby. Every
independently constructed collection — including every empty array, empty
hash, and empty object (even `{}` from `JSON.parse("{}")`) — is a distinct
object. Previously, any two empty arrays were `equal?`.

**What breaks.** Code that used `equal?` as a cheap emptiness or content
comparison.

**Fix.** Use `==` for content equality and `empty?` for emptiness; reserve
`equal?` for genuine identity checks:

```vibe
[].equal?([])   # => false: two distinct objects
[] == []        # => true: same contents
a = []
a.equal?(a)     # => true: same object
```

## 3. Hashes iterate in insertion order

**What changed.** `keys`, `values`, `each`/`each_key`/`each_value`, `to_a`,
`flatten`, `for ... in`, and the hash transforms now visit entries in insertion
order, matching Ruby, instead of sorted key order. `JSON.parse` preserves
document order and `JSON.stringify` emits members in insertion order. Hash
rendering (`inspect`, interpolation) uses the same stable order.

**Why.** Ruby's hash contract is insertion-ordered, and JSON round-trips are
expected to preserve document order.

**What breaks.** Anything that depended on sorted-key iteration or sorted
rendered output — golden-output tests are the usual casualty.

**Fix.** Sort explicitly where order matters:

```vibe
h = { b: 2, a: 1 }
h.keys        # => [:b, :a] (insertion order; previously [:a, :b])
h.keys.sort   # sorted output where you relied on it
```

One exception: hashes that reach the runtime as bare Go maps (host-provided
values and keyword-argument splats) carry no insertion record and keep the
previous sorted-key iteration.

## 4. `Array#fetch` and `Hash#fetch` raise on a miss

**What changed.** A missing index or key with no fallback now raises instead of
returning `nil`, following Ruby's strict `fetch` contract. Both forms accept a
Ruby-style block default, and `Array#fetch` accepts negative indices.

**What breaks.** Code that used `fetch` as a nil-on-miss lookup.

**Fix.** Use `[]`, `at`, `slice`, or `dig` for nil-on-miss reads; keep `fetch`
where a miss is a bug, or give it a default:

```vibe
config = { mode: "fast" }
config[:missing]                      # => nil
config.fetch(:mode)                   # => "fast"
config.fetch(:missing, "default")     # => "default"
config.fetch(:missing) { |key| "no #{key}" }
```

## 5. Hash rocket (`=>`) syntax was removed

**What changed.** Hash literals only accept colon-style keys: shorthand labels
(`name:`) and quoted string keys (`"name":`).

**What breaks.** Any literal written with Ruby's `=>`:

```
{ :name => "Ada" }   # 1.0: parse error
```

**Fix.** Use label keys, quoting when the key needs punctuation:

```vibe
person = { name: "Ada", "full name": "Ada Lovelace" }
```

To key a hash on a value computed at runtime, assign through index access
after building it:

```vibe
h = {}
h[computed_key] = 1
```

## 6. `return` inside a block returns from the enclosing method

**What changed.** An explicit `return` in a normal block now returns from the
method whose body created the block — ending iteration immediately — matching
Ruby's non-local return. The unwind runs `ensure` blocks, cannot be intercepted
by `rescue`, and validates typed returns as usual. A block invoked after its
method has already returned raises `LocalJumpError`.

**What breaks.** Blocks that used `return` as a block-local early exit:

```
def labels(values)
  values.map do |v|
    return "big" if v > 9   # old: value for this element; 1.0: returns from labels
    "small"
  end
end
```

**Fix.** Make the value the block's last expression:

```vibe
def labels(values)
  values.map do |v|
    if v > 9
      "big"
    else
      "small"
    end
  end
end
```

Genuine early exits from the method now work the way Ruby code expects:

```vibe
def first_even(values)
  values.each do |v|
    return v if v % 2 == 0
  end
  nil
end
```

## 7. A bare `private` section covers every following definition

**What changed.** A bare `private` (or `public`/`protected`) in a class body is
now a section directive covering every following definition until another
directive, matching Ruby. Previously it applied only to the next method.

**What breaks.** Classes where a second method after `private` silently stayed
public:

```
class Report
  private

  def helper_a
    1
  end

  def helper_b   # old: public; 1.0: private
    2
  end
end
```

**Fix.** Move public methods above the `private` directive, or reopen a
`public` section:

```vibe
class Report
  def helper_b
    2
  end

  private

  def helper_a
    1
  end
end
```

## 8. `private :name` is retroactive

**What changed.** `private :name` (and `private :a, :b`) in a class body now
retroactively makes the named methods private. Previously the symbol argument
was accepted but inert, so the methods stayed public.

**What breaks.** External callers of methods a script *declared* private with
the symbol form — the declaration now takes effect, so those calls raise.

**Fix.** This is almost always what the script meant. If a method genuinely
must stay public, delete the stale `private :name` line:

```vibe
class Report
  def helper
    1
  end

  private :helper
end
```

## 9. `module`, `public`, `protected`, `include`, and `extend` are contextual keywords

**What changed.** These words are now contextual keywords in declaration and
directive positions. Previously they were plain identifiers everywhere, so a
parenless call to a same-named user function could occupy those positions
(`protected :b` in a class body called `def protected(...)`; `module Config`
called `def module(...)`).

**What breaks.** A bare visibility or mixin directive that collides with a
same-named script function is now a compile error naming the collision, and
reinterpreted `module` shapes fail with targeted parse or resolution errors.

**Fix.** Rename the colliding function, or keep calling it with parentheses —
parenthesized calls (`public(:b)`), assignments (`public = 1`), and bare local
reads keep their old meaning.

## 10. Parenless `f *x` and `f **x` are splat calls

**What changed.** Following Ruby's spacing rule, `f *n` and `f **n` — a space
before the star, none after — where the callee is a zero-arg function or
member call now parse as a call with a splat (or keyword-splat) argument
instead of multiplying or exponentiating the call's value.

**What breaks.** Arithmetic written in exactly that spacing against a function
or member-call callee:

```
def total
  10
end

total *2    # old: 20; 1.0: splat call — raises "splat argument must be an array"
```

Local variables are unaffected: `x *n` is still multiplication.

**Fix.** Keep the arithmetic reading with any of these spellings:

```vibe
def total
  10
end

n = 2
total() * n   # explicit call
(total) * n   # parenthesized callee
total * n     # operator spaced on both sides
```

And use the new form deliberately when forwarding argument lists:

```vibe
def add(a, b)
  a + b
end

args = [1, 2]
add *args   # => 3
```

## 11. The `-> Type` return annotation must sit on the signature line

**What changed.** A `->` opening the line after a `def` signature now parses as
a stabby lambda literal (new in 1.0), not as the previous line's return
annotation.

**What breaks.** Signatures that wrapped the annotation onto its own line:

```
def total(a, b)
  -> int
  a + b
end
```

**Fix.** Keep the annotation on the signature line:

```vibe
def total(a, b) -> int
  a + b
end
```

## 12. Parenless `f /2` can open a regex literal

**What changed.** Following Ruby's spacing rule (space before the slash, none
after, non-local callee), a slash after a zero-arg function or member call now
starts a command-argument regex literal instead of dividing the call's result.
Locals — including the implicit `it` block parameter and the enclosing class's
constants — are unaffected and keep dividing in every spacing, and `f /= 2`
remains a compound assignment.

**What breaks.** Division written with exactly that spacing against a function
or member-call callee. Without a second slash on the line it fails loudly with
"unterminated regex literal"; with one (for example `f /2 + g/i`) the line
parses as `f(/2 + g/i)`, silently changing a former division chain — audit
division written in this spacing. Note that accessor names (`getter total`)
are method calls, not locals, so `total /2` inside a method now reads as a
regex argument.

**Fix.** Keep the division reading with any of these spellings:

```vibe
def rate
  4
end

rate / 2    # spaced on both sides
rate/2      # flush
rate() / 2  # explicit call
```

And use the new form deliberately:

```vibe
text = "ID-42"
text.match /ID-[0-9]+/
```

## 13. Parenless `f [0]` passes an array argument

**What changed.** Following the same spacing rule as the splat and regex
forms, a bracket detached from a non-local callee now opens an array-literal
command argument: `puts [3, 1, 2].sort` is `puts([3, 1, 2].sort)`. A flush
bracket keeps indexing (`puts[1]` still tries to index the callee), and a
known local indexes in every spacing, so `a [0]` and `a [0] = 1` are
unchanged when `a` is a local. `self [0]` also keeps indexing: `self` is
never a command callee.

**What breaks.** Bare accessor names inside method bodies are non-local
callees, so with `getter items` (or `property items`) a method body that
indexed the accessor value with a spaced bracket now passes an argument
instead and fails — the same error Ruby raises for `items [0]` in a method:

```
class Box
  property items

  def first_item()
    items [0]    # 1.0: arity error — parses as items([0]), as in Ruby
  end
end
```

**The fix.** Index through a receiver or keep the bracket flush:

```vibe
class Box
  property items

  def initialize()
    @items = [10, 20]
  end

  def first_item()
    self.items[0]
  end
end
```

## 14. A same-line `do` block after a parenless call argument binds to the outer call

**What changed.** `puts arr.map do |x| ... end` now passes the block to `puts`
(which ignores it), matching Ruby, so `map` raises "requires a block".

**What breaks.** Parenless outer calls whose argument was a call that expected
the `do` block.

**Fix.** Parenthesize the receiver call, or use a brace block, which keeps
binding to the nearest call:

```vibe
values = [1, 2]

puts(values.map do |v|
  v * 2
end)

puts values.map { |v| v * 2 }
```

## 15. A newline ends a range at statement level

**What changed.** `x = 1..` at the end of a line is now an endless range (new
in 1.0), and the next line parses as a separate statement. Bounded endpoints
may still continue onto the next line inside parens, brackets, and call
arguments.

**What breaks.** Statement-level ranges that wrapped their end bound onto the
next line:

```
limit = 1..
  10        # old: 1..10; 1.0: endless range, then a stray statement
```

**Fix.** Group the range when it must span lines, or keep it on one line:

```vibe
limit = (1..
  10)
span = 1..10
open_ended = 5..
```

## 16. String behavior alignments

Several string methods changed results to match Ruby exactly.

**`String#scan` returns captures when the pattern has groups.** With one or
more capture groups, `scan` returns a nested array per match holding each
captured substring (`nil` for a non-participating optional group) instead of
the full match strings:

```vibe
"a1 b2".scan(/([a-z])([0-9])/)   # => [["a", "1"], ["b", "2"]]
"a1 b2".scan(/[a-z][0-9]/)       # => ["a1", "b2"]
```

Fix: use non-capturing groups (`(?:...)`) when you want full matches.

**`String#sub`/`gsub` regex replacements use Ruby backreference syntax.** With
`regex: true`, replacements expand `\1`–`\9`, `\&`/`\0`, `` \` ``, `\'`, `\+`,
and `\k<name>`; Go's `$1` and `$&` are now literal text. As in Ruby, once a
pattern defines any named group the numbered refs expand to the empty string —
use `\k<name>`. (`Regex.replace`/`Regex.replace_all` keep the `$1` syntax.)

```vibe
"abc123".sub("([a-z]+)([0-9]+)", "\\2-\\1", regex: true)   # => "123-abc"
```

**`String#split` defaults.** Trailing empty fields are trimmed by default (use
a negative limit to keep them), a separator of exactly `" "` triggers Ruby's
AWK whitespace mode, and the no-separator default splits only on the six ASCII
whitespace bytes (wider Unicode whitespace stays inside fields):

```vibe
"a,b,".split(",")       # => ["a", "b"]
"a,b,".split(",", -1)   # => ["a", "b", ""]
" a  b ".split(" ", 2)  # => ["a", "b "]
```

**`String#strip`/`lstrip`/`rstrip` use Ruby's whitespace set.** Only the ASCII
whitespace bytes (plus NUL) are trimmed; NBSP, em space, and other Unicode
spaces are now preserved.

**Case methods use full Unicode case mapping.** `upcase`, `downcase`,
`capitalize`, and `swapcase` follow Ruby's special mappings (`"Straße".upcase`
is `"STRASSE"`). Pass `:ascii` to restrict mapping to ASCII letters, or
`downcase(:fold)` for case folding:

```vibe
"Straße".upcase           # => "STRASSE"
"Straße".upcase(:ascii)   # => "STRAßE"
"Straße".downcase(:fold)  # => "strasse"
```

## 17. `time - time` returns a `Float` of seconds

**What changed.** Subtracting two times returns a `Float` number of seconds —
preserving sub-second precision and matching Ruby's `Time#-` — instead of a
whole-second `Duration`. (Relatedly, `time + number` / `time - number` now
treat the number as seconds.)

**What breaks.** Code that treated the difference as a `Duration` (calling
duration members on it, or passing it where a `Duration` is required).

**Fix.** Work with the numeric seconds:

```vibe
elapsed = Time.utc(2024, 1, 2) - Time.utc(2024, 1, 1)
elapsed              # => 86400.0
elapsed / 3600.0     # hours as a float
```

## 18. Comparison and float-arithmetic edge cases

**`<=>` returns `nil` for incomparable operands** instead of raising: mixed
kinds (`1 <=> "a"`), money in different currencies, and `Time#<=>` against a
non-`Time` all yield `nil`. The relational operators `<`, `<=`, `>`, `>=`
still raise on incomparable operands. Code that rescued the old error must
check for `nil` instead.

**Float division by zero follows IEEE 754** (matching Ruby) instead of
raising: a finite nonzero numerator yields `Infinity`/`-Infinity` and a zero
numerator yields `NaN`. Integer division by zero still raises. Code that
rescued float division-by-zero errors should test the divisor with `zero?` or
the result with `finite?`/`nan?`:

```vibe
1 <=> "a"     # => nil (previously raised)
1.0 / 0.0     # => Infinity (previously raised)
(0.0 / 0.0).nan?   # => true
```

Coercing a non-finite float to an integer raises rather than yielding a
garbage value.

## 19. Sandbox quota timing

Two quota-accounting changes can alter when (not whether) resource limits
fire; scripts running close to their configured quotas may be affected.

**`Array#flatten` and `Hash#flatten` now charge the step quota.** Both charge
roughly one step per element examined while the result is built (they
previously charged ~0 steps), and charge output growth against the memory
quota before it is allocated. A large flatten under a tight step quota that
previously slipped through now rejects with a quota error. Fix: raise the
host's step quota to cover the real work, or flatten smaller inputs.

**Unread over-quota globals no longer fail at bind time (embedders).** A
`Script.Call` carrying a composite global that would exceed the memory quota
no longer fails at bind time when the script never reads it: the quota is
charged when (and only when) the global is materialized on first read. Hosts
using the memory quota as inbound-payload admission control should size-check
payloads before the call instead of relying on the bind-time rejection.
(`StrictEffects` still validates globals eagerly at bind time.)

More broadly, 1.0 closes several paths where transient allocations
(hash-transform scratch and output maps, interpolation growth, rest
destructuring windows, ephemeral block receivers) escaped the memory quota.
Workloads that only fit because of that under-counting now fail with a quota
error; the fix is to size quotas for the real peak.

## 20. Smaller behavioral alignments

- **`Array#sum` honors its initial value and block** (previously silently
  ignored), and each addition must operate on compatible operands — summing
  string elements needs a string initial value (`["a", "b"].sum("")`).
- **`Array#first`/`Array#last` raise on extra or keyword arguments** instead of
  silently ignoring them.
- **`Array#count(value)` with an attached block ignores the block** (Ruby
  precedence) instead of raising.
- **`Array#reduce` on an empty array folds to `nil`** (or the supplied
  initial) instead of raising.
- **`Hash#each` yields a `[key, value]` pair to single-parameter blocks**
  instead of only the key; two-parameter blocks are unchanged.
- **A positional argument after a keyword label inside parentheses is a parse
  error** (`collect(first: 1, "tail")`), matching Ruby and the parenless form.
- **`array?<int>` / `hash?<string, int>` are parse errors**; write
  `array<int> | nil` for a nullable generic container.
- **`it *2` in a parameterless block multiplies** (it previously parsed as a
  splat call that always failed at runtime).
- **A `+` or `-` written flush against its operand at the start of a line
  begins a new statement**, matching Ruby; a sign spaced from its operand
  still continues the previous line as a binary operator.
- **`String#match` accepts an optional offset** and `String#split(nil)` now
  behaves like the no-argument form — both previously raised, so only code
  asserting those errors is affected.
