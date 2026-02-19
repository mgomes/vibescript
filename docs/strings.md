# String Methods

VibeScript provides several methods for string manipulation.

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

### `start_with?(prefix)`

Returns true if the string starts with `prefix`:

```vibe
"vibescript".start_with?("vibe") # true
```

### `end_with?(suffix)`

Returns true if the string ends with `suffix`:

```vibe
"vibescript".end_with?("script") # true
```

### `include?(substring)`

Returns true when `substring` appears in the string:

```vibe
"vibescript".include?("script") # true
```

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

VibeScript strings are immutable, so mutating-style Ruby methods return a new string.

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
