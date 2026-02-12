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

### `strip()`

Removes leading and trailing whitespace:

```vibe
def clean_input(text)
  text.strip()
end

clean_input("  hello  ")  # "hello"
```

### `upcase()`

Converts the string to uppercase:

```vibe
def shout(message)
  message.upcase()
end

shout("hello")  # "HELLO"
```

### `downcase()`

Converts the string to lowercase:

```vibe
def normalize(email)
  email.downcase()
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

## Splitting

### `split(separator = nil)`

Splits a string into an array of strings.

**Without arguments:** Splits on whitespace and removes empty entries:

```vibe
"one two  three".split()  # ["one", "two", "three"]
```

**With separator:** Splits on the specified string:

```vibe
"a,b,c".split(",")        # ["a", "b", "c"]
"path/to/file".split("/") # ["path", "to", "file"]
```

## Example: Text Processing

```vibe
def parse_tags(input)
  input.strip()
       .downcase()
       .split(",")
       .map { |tag| tag.strip() }
       .select { |tag| tag != "" }
end

parse_tags("  Ruby, Go,  Python  ")
# ["ruby", "go", "python"]
```
