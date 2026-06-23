# String Methods

Vibescript provides several methods for string manipulation.

## Interpolation

Double-quoted strings interpolate single expressions inside `#{...}`:

```vibe
name = "Ada"
"Hello #{name}" # "Hello Ada"
```

Escape the marker with `\#{...}` when literal text is required. Single-quoted
strings do not interpolate.

## Locale Behavior

String transforms are locale-insensitive and deterministic across supported
platforms. Methods like `upcase`, `downcase`, and `capitalize` use Unicode
case mapping rules and do not depend on host locale settings.

## Basic Methods

### `length`

Alias for `size`, returns the number of characters:

```vibe
"héllo".length  # 5
```

### `bytesize`

Returns the number of bytes in UTF-8 encoding:

```vibe
"hé".bytesize  # 3
```

### `ord`

Returns the codepoint of the first character:

```vibe
"hé".ord  # 104
```

### `chr`

Returns the first character, mirroring Ruby's `String#chr`. An empty string
returns an empty string:

```vibe
"hé".chr  # "h"
"".chr    # ""
```

### `hex`

Interprets the leading characters as a hexadecimal integer, mirroring Ruby's
`String#hex`. Leading whitespace and a single optional `+`/`-` sign are skipped,
an optional `0x`/`0X` prefix is consumed, and single underscores between digits
are treated as separators. Parsing stops at the first byte that is not a hex
digit, and a string with no leading hex digit returns `0`:

```vibe
"ff".hex      # 255
"0xff".hex    # 255
"-1A".hex     # -26
"ff zoo".hex  # 255
"garbage".hex # 0
```

Unlike Ruby, which promotes to an arbitrary-precision `Bignum`, Vibescript only
has 64-bit integers, so a value outside the `int64` range raises an
`integer out of range` error rather than silently growing.

### `oct`

Interprets the leading characters as an integer using a base inferred from its
prefix, mirroring Ruby's `String#oct`. The default base is octal, but a
`0x`/`0X`, `0b`/`0B`, `0o`/`0O`, or `0d`/`0D` prefix selects hexadecimal,
binary, octal, or decimal respectively. Leading whitespace, a single optional
sign, and underscore separators are handled like `hex`, parsing stops at the
first byte invalid for the chosen base, and an unparseable string returns `0`:

```vibe
"17".oct     # 15
"0b101".oct  # 5
"0o17".oct   # 15
"0xff".oct   # 255
"0d99".oct   # 99
"-17".oct    # -15
"garbage".oct # 0
```

Like `hex`, a value outside the `int64` range raises an `integer out of range`
error instead of promoting to a `Bignum`.

### `empty?`

Returns true when the string has no characters:

```vibe
"".empty?      # true
"hello".empty? # false
```

### `strip`

Removes leading and trailing whitespace:

```vibe
def clean_input(text)
  text.strip
end

clean_input("  hello  ")  # "hello"
```

Like Ruby, `strip` only removes the ASCII whitespace bytes tab (`\t`), newline
(`\n`), vertical tab (`\v`), form feed (`\f`), carriage return (`\r`), and space
(`" "`), plus trailing NUL (`\0`). Unicode spaces such as NBSP (`U+00A0`), the
Ogham space mark (`U+1680`), em space (`U+2003`), and the byte order mark
(`U+FEFF`) are preserved. A leading NUL is kept while a trailing NUL is removed,
matching Ruby's `lstrip`/`rstrip` asymmetry.

### `squish`

Trims leading/trailing whitespace and collapses internal whitespace runs to a
single space:

```vibe
"  hello \n\t world  ".squish # "hello world"
```

### `lstrip`

Removes leading whitespace:

```vibe
"  hello  ".lstrip  # "hello  "
```

Removes the same ASCII whitespace set as `strip` from the start of the string. A
leading NUL is left in place and Unicode spaces are preserved.

### `rstrip`

Removes trailing whitespace:

```vibe
"  hello  ".rstrip  # "  hello"
```

Removes the same ASCII whitespace set as `strip` from the end of the string,
including a trailing NUL. Unicode spaces are preserved.

### `chomp(separator = "\n")`

Removes a trailing separator (default newline):

```vibe
"line\n".chomp      # "line"
"path///".chomp("/") # "path//"
```

### `chop`

Removes the last character. A trailing `"\r\n"` is treated as a single record
separator and removed together; otherwise one full character (a Unicode code
point, not a single byte) is removed. An empty string is returned unchanged:

```vibe
"abc".chop      # "ab"
"abc\n".chop    # "abc"
"héllo".chop    # "héll"
"".chop         # ""
```

### `upcase`

Converts the string to uppercase:

```vibe
def shout(message)
  message.upcase
end

shout("hello")  # "HELLO"
```

### `downcase`

Converts the string to lowercase:

```vibe
def normalize(email)
  email.downcase
end

normalize("USER@EXAMPLE.COM")  # "user@example.com"
```

### `capitalize`

Uppercases the first character and lowercases the rest:

```vibe
"hÉLLo wORLD".capitalize # "Héllo world"
```

### `swapcase`

Flips letter casing for each character:

```vibe
"Hello VIBE".swapcase # "hELLO vibe"
```

### `reverse`

Reverses characters:

```vibe
"héllo".reverse # "olléh"
```

### `start_with?(*prefixes)`

Returns true if the string starts with any of the given prefixes. Requires at
least one prefix, and every prefix must be a string:

```vibe
"vibescript".start_with?("vibe")        # true
"vibescript".start_with?("x", "vibe")   # true
"vibescript".start_with?("x", "script") # false
```

### `end_with?(*suffixes)`

Returns true if the string ends with any of the given suffixes. Requires at
least one suffix, and every suffix must be a string:

```vibe
"vibescript".end_with?("script")        # true
"vibescript".end_with?("x", "script")   # true
"vibescript".end_with?("x", "vibe")     # false
```

### `include?(substring)`

Returns true when `substring` appears in the string:

```vibe
"vibescript".include?("script") # true
```

### `casecmp(other)`

Case-insensitively compares two strings, returning `-1`, `0`, or `1`. Each ASCII
letter `A`-`Z` is folded down to its lowercase form before the byte comparison;
every other byte (including multibyte UTF-8 sequences) is compared ordinally,
matching Ruby's `String#casecmp` (which applies an ASCII `TOLOWER` to each side
in Ruby 2.7 and later). Folding downward keeps the punctuation bytes between `Z`
and `a` (`[`, `\`, `]`, `^`, `_`, and `` ` ``) ordered as Ruby orders them: because
uppercase letters fold into the `a`-`z` range, those punctuation bytes sort below
the folded letters, so `"[".casecmp("A")` is `-1` rather than `1`. Returns `nil`
when `other` is not a string:

```vibe
"abc".casecmp("ABC") # 0
"abc".casecmp("ABD") # -1
"abd".casecmp("ABC") # 1
"[".casecmp("A")     # -1
"z".casecmp("[")     # 1
"abc".casecmp(1)     # nil
```

### `casecmp?(other)`

Returns `true` when two strings are equal under Unicode case folding, `false`
otherwise, and `nil` when `other` is not a string:

```vibe
"abc".casecmp?("ABC")     # true
"héllo".casecmp?("HÉLLO") # true
"abc".casecmp?("ABD")     # false
"abc".casecmp?(1)         # nil
```

Folding uses Unicode simple case mapping, consistent with `upcase` and
`downcase`. Full-fold expansions such as German `ß` matching `SS` are not
applied, so `"ß".casecmp?("SS")` is `false` (Ruby returns `true`).

When either operand contains invalid UTF-8, folding falls back to byte-wise
ASCII case folding so that distinct byte sequences stay distinct. This mirrors
Ruby's binary-string path and preserves byte identity, where the Unicode path
would otherwise treat every invalid byte as the same replacement character.

### `match(pattern)`

Regex match returning `[full, capture1, ...]` or `nil`:

```vibe
"ID-12 ID-34".match("ID-([0-9]+)") # ["ID-12", "12"]
```

### `match?(pattern, offset = 0)`

Allocation-light boolean predicate counterpart to `match`. Returns `true` when
`pattern` has a match at or after the given character offset, otherwise `false`,
without materializing match arrays:

```vibe
"abc".match?("b")           # true
"abc".match?("z")           # false
"abc".match?("ID-[0-9]+")   # false
"abc".match?("b", 1)        # true
"abc".match?("b", 2)        # false
```

The pattern uses the same regex engine and size guards as `match`, so anchors
such as `\A`, `^`, and `\b` keep the full-string context even when an offset is
supplied: the characters preceding the offset still count toward boundary and
anchor checks. The offset is a character (codepoint) position; an offset past
the end of the string yields `false` rather than an error. Unlike Ruby,
negative offsets are rejected, matching `index` and `rindex`.

### `scan(pattern)`

Regex scan returning all full matches:

```vibe
"ID-12 ID-34".scan("ID-[0-9]+") # ["ID-12", "ID-34"]
```

### `index(substring, offset = 0)`

Returns the first character index for `substring`, or `nil` when not found:

```vibe
"héllo hello".index("llo")    # 2
"héllo hello".index("llo", 6) # 8
"héllo hello".index("zzz")    # nil
```

### `rindex(substring, offset = size)`

Returns the last character index for `substring`, or `nil` when not found:

```vibe
"héllo hello".rindex("llo")    # 8
"héllo hello".rindex("llo", 4) # 2
```

### `slice(index, length = nil)`

Returns a character or substring; returns `nil` when out of bounds:

```vibe
"héllo".slice(1)    # "é"
"héllo".slice(1, 3) # "éll"
"héllo".slice(99)   # nil
```

### `sub(pattern, replacement, regex: false)`

Replaces the first occurrence of `pattern`:

```vibe
"bananas".sub("na", "NA") # "baNAnas"
"ID-12 ID-34".sub("ID-[0-9]+", "X", regex: true) # "X ID-34"
```

### `gsub(pattern, replacement, regex: false)`

Replaces all occurrences of `pattern`:

```vibe
"bananas".gsub("na", "NA") # "baNANAs"
"ID-12 ID-34".gsub("ID-[0-9]+", "X", regex: true) # "X X"
```

### `delete_prefix(prefix)`

Removes the prefix when present:

```vibe
"unhappy".delete_prefix("un") # "happy"
```

### `delete_suffix(suffix)`

Removes the suffix when present:

```vibe
"report.csv".delete_suffix(".csv") # "report"
```

## Padding

`width` is measured in characters (Unicode code points), not bytes or display
columns, so multibyte characters each count as one. When `width` is less than or
equal to the receiver's length, the receiver is returned unchanged. A `Float`
width is truncated toward zero. The pad string defaults to a single space and
must not be empty; it is repeated and then truncated at a character boundary to
fill the requested span.

### `center(width, pad = " ")`

Centers the string, padding both sides. When the padding cannot be split evenly,
the extra character goes on the right:

```vibe
"hi".center(6, "-")     # "--hi--"
"hi".center(5)          # " hi  "
"hi".center(10, "12345") # "1234hi1234"
```

### `ljust(width, pad = " ")`

Left-justifies the string, padding on the right:

```vibe
"hi".ljust(5, ".") # "hi..."
"hi".ljust(7, "ab") # "hiababa"
```

### `rjust(width, pad = " ")`

Right-justifies the string, padding on the left:

```vibe
"hi".rjust(5, ".") # "...hi"
"hi".rjust(7, "ab") # "ababahi"
```

## Compatibility Methods

Vibescript strings are immutable, so mutating-style Ruby methods return a new string.

### `clear`

Returns an empty string:

```vibe
"hello".clear # ""
```

### `concat(*strings)`

Appends one or more strings:

```vibe
"hello".concat           # "hello"
"he".concat("llo", "!") # "hello!"
```

### `replace(replacement)`

Returns `replacement`:

```vibe
"old".replace("new") # "new"
```

### Bang aliases

The following methods are supported as aliases and return transformed strings.
When there is no change, bang methods return `nil`.

- `strip!`, `lstrip!`, `rstrip!`, `chomp!`, `chop!`
- `squish!`
- `delete_prefix!`, `delete_suffix!`
- `upcase!`, `downcase!`, `capitalize!`, `swapcase!`, `reverse!`
- `sub!`, `gsub!`

## Splitting

### `split(separator = nil)`

Splits a string into an array of strings.

**Without arguments:** Splits on runs of ASCII whitespace and removes empty
entries, mirroring Ruby's default `String#split`. Leading and trailing
whitespace is discarded and a blank string yields an empty array:

```vibe
"one two  three".split  # ["one", "two", "three"]
"  a  b  ".split        # ["a", "b"]
```

Only the ASCII whitespace bytes split fields: space, tab, newline, vertical
tab, form feed, and carriage return. Wider Unicode whitespace such as the
non-breaking space (`U+00A0`) or the em space (`U+2003`) is kept inside the
field rather than acting as a separator, matching Ruby instead of treating the
full Unicode whitespace table as delimiters:

```vibe
"a b".split  # ["a b"] (non-breaking space is preserved)
```

**With separator:** Splits on the specified string:

```vibe
"a,b,c".split(",")        # ["a", "b", "c"]
"path/to/file".split("/") # ["path", "to", "file"]
```

### `partition(separator)`

Splits the string around the **first** occurrence of `separator`, returning a
three-element array of the text before the separator, the separator itself, and
the text after it:

```vibe
"abc=def=ghi".partition("=") # ["abc", "=", "def=ghi"]
```

When the separator is not found, the whole string is returned as the first
element with two empty trailing elements. An empty separator matches at the very
start:

```vibe
"no-sep".partition("=") # ["no-sep", "", ""]
"abc".partition("")     # ["", "", "abc"]
```

The separator must be a string.

### `rpartition(separator)`

Splits the string around the **last** occurrence of `separator`, returning a
three-element array of the text before the separator, the separator itself, and
the text after it:

```vibe
"abc=def=ghi".rpartition("=") # ["abc=def", "=", "ghi"]
```

When the separator is not found, the whole string is returned as the last
element with two empty leading elements. An empty separator matches at the very
end:

```vibe
"no-sep".rpartition("=") # ["", "", "no-sep"]
"abc".rpartition("")     # ["abc", "", ""]
```

The separator must be a string.

### `chars`

Returns an array of the string's Unicode characters, one entry per code point.
This is rune-aware, matching the behavior of `length` and `slice`:

```vibe
"abc".chars # ["a", "b", "c"]
"héllo".chars # ["h", "é", "l", "l", "o"]
"".chars # []
```

### `lines`

Splits the string into lines using `"\n"` as the separator, keeping the trailing
newline on each line. A trailing newline does not produce a final empty line,
and an empty string yields no lines:

```vibe
"a\nb".lines   # ["a\n", "b"]
"a\nb\n".lines # ["a\n", "b\n"]
"".lines       # []
```

Only `\n` ends a line. Carriage returns are never treated as separators and are
preserved verbatim, so a Windows-style `\r\n` line ending keeps the `\r`
attached to the line that precedes the `\n` (input on the left contains literal
carriage-return bytes):

```text
"a\r\nb".lines # ["a\r\n", "b"]
"a\rb".lines   # ["a\rb"]
```

### `bytes`

Returns an array of the string's bytes as integers in the range `0..255`. This
is byte-level, not rune-aware: a multibyte character expands to one entry per
UTF-8 byte, so `bytes.size` equals [`bytesize`](#bytesize), not
[`length`](#length):

```vibe
"abc".bytes   # [97, 98, 99]
"héllo".bytes # [104, 195, 169, 108, 108, 111]
"".bytes      # []
```

The raw bytes are returned verbatim. Strings carrying invalid UTF-8 (only
reachable through host-provided values, since Vibescript literals cannot encode
them) are not normalized, matching Ruby's `String#bytes`.

### `each_char`

Yields each Unicode character to a block without first materializing an array,
the streaming counterpart to [`chars`](#chars). Characters are whole code points,
matching the rune-aware behavior of `length`, `slice`, and `chars`. The block's
return value is ignored, and `each_char` returns the receiver string:

```vibe
out = []
"héllo".each_char { |c| out = out + [c] }
out # ["h", "é", "l", "l", "o"]
```

A block is required. Vibescript has no `Enumerator`, so calling `each_char`
without a block reports `string.each_char requires a block` rather than returning
an enumerator.

### `each_byte`

Yields each byte as an integer in the range `0..255` without first materializing
an array, the streaming counterpart to [`bytes`](#bytes). Iteration is
byte-level, so a multibyte character is yielded one UTF-8 byte at a time. The
block's return value is ignored, and `each_byte` returns the receiver string:

```vibe
out = []
"é".each_byte { |b| out = out + [b] }
out # [195, 169]
```

As with `each_char`, a block is required; calling `each_byte` without a block
reports `string.each_byte requires a block`.

### `each_line`

Yields each line to a block without first materializing an array, the streaming
counterpart to [`lines`](#lines). Lines retain their trailing `\n` and follow the
same separator rules as `lines`: only `\n` ends a line, a trailing newline does
not produce a final empty line, and a Windows-style `\r\n` keeps the `\r`
attached. The block's return value is ignored, and `each_line` returns the
receiver string:

```vibe
out = []
"a\nb\n".each_line { |l| out = out + [l] }
out # ["a\n", "b\n"]
```

As with `each_char`, a block is required; calling `each_line` without a block
reports `string.each_line requires a block`.

## Templating

### `template(context, strict: false)`

Interpolates `{{name}}` placeholders from a hash context. Dot paths can access
nested hashes (`{{user.name}}`).

```vibe
tpl = "Player {{user.name}} scored {{user.score}}"
ctx = { user: { name: "Alex", score: 42 } }

tpl.template(ctx) # "Player Alex scored 42"
```

When `strict: true`, missing placeholders raise an error instead of being left
unchanged.

## Example: Text Processing

```vibe
def parse_tags(input)
  tags = input.strip.downcase.split(",")
  tags = tags.map do |tag|
    tag.strip
  end
  tags.select do |tag|
    tag != ""
  end
end

parse_tags("  Ruby, Go,  Python  ")
# ["ruby", "go", "python"]
```
