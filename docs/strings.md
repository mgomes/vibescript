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

Returns the first character (or `nil` for empty strings):

```vibe
"hé".chr  # "h"
"".chr    # nil
```

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

### `rstrip`

Removes trailing whitespace:

```vibe
"  hello  ".rstrip  # "  hello"
```

### `chomp(separator = "\n")`

Removes a trailing separator (default newline):

```vibe
"line\n".chomp      # "line"
"path///".chomp("/") # "path//"
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

- `strip!`, `lstrip!`, `rstrip!`, `chomp!`
- `squish!`
- `delete_prefix!`, `delete_suffix!`
- `upcase!`, `downcase!`, `capitalize!`, `swapcase!`, `reverse!`
- `sub!`, `gsub!`

## Splitting

### `split(separator = nil)`

Splits a string into an array of strings.

**Without arguments:** Splits on whitespace and removes empty entries:

```vibe
"one two  three".split  # ["one", "two", "three"]
```

**With separator:** Splits on the specified string:

```vibe
"a,b,c".split(",")        # ["a", "b", "c"]
"path/to/file".split("/") # ["path", "to", "file"]
```

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
