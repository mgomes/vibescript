# String Methods

VibeScript provides several methods for string manipulation.

## Basic Methods

### `length`

Alias for `size`, returns the number of characters:

```vibe
"héllo".length  # 5
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
