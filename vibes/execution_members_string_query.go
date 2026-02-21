package vibes

import (
	"fmt"
	"regexp"
	"strings"
)

func stringMemberQuery(property string) (Value, error) {
	switch property {
	case "size":
		return NewAutoBuiltin("string.size", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.size does not take arguments")
			}
			return NewInt(int64(len([]rune(receiver.String())))), nil
		}), nil
	case "length":
		return NewAutoBuiltin("string.length", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.length does not take arguments")
			}
			return NewInt(int64(len([]rune(receiver.String())))), nil
		}), nil
	case "bytesize":
		return NewAutoBuiltin("string.bytesize", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.bytesize does not take arguments")
			}
			return NewInt(int64(len(receiver.String()))), nil
		}), nil
	case "ord":
		return NewAutoBuiltin("string.ord", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.ord does not take arguments")
			}
			runes := []rune(receiver.String())
			if len(runes) == 0 {
				return NewNil(), fmt.Errorf("string.ord requires non-empty string")
			}
			return NewInt(int64(runes[0])), nil
		}), nil
	case "chr":
		return NewAutoBuiltin("string.chr", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.chr does not take arguments")
			}
			runes := []rune(receiver.String())
			if len(runes) == 0 {
				return NewNil(), nil
			}
			return NewString(string(runes[0])), nil
		}), nil
	case "empty?":
		return NewAutoBuiltin("string.empty?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.empty? does not take arguments")
			}
			return NewBool(len(receiver.String()) == 0), nil
		}), nil
	case "clear":
		return NewAutoBuiltin("string.clear", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.clear does not take arguments")
			}
			return NewString(""), nil
		}), nil
	case "concat":
		return NewAutoBuiltin("string.concat", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			var b strings.Builder
			b.WriteString(receiver.String())
			for _, arg := range args {
				if arg.Kind() != KindString {
					return NewNil(), fmt.Errorf("string.concat expects string arguments")
				}
				b.WriteString(arg.String())
			}
			return NewString(b.String()), nil
		}), nil
	case "replace":
		return NewAutoBuiltin("string.replace", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.replace expects exactly one replacement")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.replace replacement must be string")
			}
			return NewString(args[0].String()), nil
		}), nil
	case "start_with?":
		return NewAutoBuiltin("string.start_with?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.start_with? expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.start_with? prefix must be string")
			}
			return NewBool(strings.HasPrefix(receiver.String(), args[0].String())), nil
		}), nil
	case "end_with?":
		return NewAutoBuiltin("string.end_with?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.end_with? expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.end_with? suffix must be string")
			}
			return NewBool(strings.HasSuffix(receiver.String(), args[0].String())), nil
		}), nil
	case "include?":
		return NewAutoBuiltin("string.include?", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.include? expects exactly one substring")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.include? substring must be string")
			}
			return NewBool(strings.Contains(receiver.String(), args[0].String())), nil
		}), nil
	case "match":
		return NewAutoBuiltin("string.match", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.match does not take keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.match expects exactly one pattern")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.match pattern must be string")
			}
			pattern := args[0].String()
			re, err := regexp.Compile(pattern)
			if err != nil {
				return NewNil(), fmt.Errorf("string.match invalid regex: %v", err)
			}
			text := receiver.String()
			indices := re.FindStringSubmatchIndex(text)
			if indices == nil {
				return NewNil(), nil
			}
			values := make([]Value, len(indices)/2)
			for i := range values {
				start := indices[i*2]
				end := indices[i*2+1]
				if start < 0 || end < 0 {
					values[i] = NewNil()
					continue
				}
				values[i] = NewString(text[start:end])
			}
			return NewArray(values), nil
		}), nil
	case "scan":
		return NewAutoBuiltin("string.scan", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("string.scan does not take keyword arguments")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.scan expects exactly one pattern")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.scan pattern must be string")
			}
			pattern := args[0].String()
			re, err := regexp.Compile(pattern)
			if err != nil {
				return NewNil(), fmt.Errorf("string.scan invalid regex: %v", err)
			}
			matches := re.FindAllString(receiver.String(), -1)
			values := make([]Value, len(matches))
			for i, m := range matches {
				values[i] = NewString(m)
			}
			return NewArray(values), nil
		}), nil
	case "index":
		return NewAutoBuiltin("string.index", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.index expects substring and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.index substring must be string")
			}
			offset := 0
			if len(args) == 2 {
				i, err := valueToInt(args[1])
				if err != nil || i < 0 {
					return NewNil(), fmt.Errorf("string.index offset must be non-negative integer")
				}
				offset = i
			}
			index := stringRuneIndex(receiver.String(), args[0].String(), offset)
			if index < 0 {
				return NewNil(), nil
			}
			return NewInt(int64(index)), nil
		}), nil
	case "rindex":
		return NewAutoBuiltin("string.rindex", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.rindex expects substring and optional offset")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.rindex substring must be string")
			}
			offset := len([]rune(receiver.String()))
			if len(args) == 2 {
				i, err := valueToInt(args[1])
				if err != nil || i < 0 {
					return NewNil(), fmt.Errorf("string.rindex offset must be non-negative integer")
				}
				offset = i
			}
			index := stringRuneRIndex(receiver.String(), args[0].String(), offset)
			if index < 0 {
				return NewNil(), nil
			}
			return NewInt(int64(index)), nil
		}), nil
	case "slice":
		return NewAutoBuiltin("string.slice", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 {
				return NewNil(), fmt.Errorf("string.slice expects index and optional length")
			}
			start, err := valueToInt(args[0])
			if err != nil {
				return NewNil(), fmt.Errorf("string.slice index must be integer")
			}
			runes := []rune(receiver.String())
			if len(args) == 1 {
				if start < 0 || start >= len(runes) {
					return NewNil(), nil
				}
				return NewString(string(runes[start])), nil
			}
			length, err := valueToInt(args[1])
			if err != nil {
				return NewNil(), fmt.Errorf("string.slice length must be integer")
			}
			substr, ok := stringRuneSlice(receiver.String(), start, length)
			if !ok {
				return NewNil(), nil
			}
			return NewString(substr), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}
