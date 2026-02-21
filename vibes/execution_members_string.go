package vibes

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

func stringMember(str Value, property string) (Value, error) {
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
	case "strip":
		return NewAutoBuiltin("string.strip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip does not take arguments")
			}
			return NewString(strings.TrimSpace(receiver.String())), nil
		}), nil
	case "strip!":
		return NewAutoBuiltin("string.strip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.strip! does not take arguments")
			}
			updated := strings.TrimSpace(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "squish":
		return NewAutoBuiltin("string.squish", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.squish does not take arguments")
			}
			return NewString(stringSquish(receiver.String())), nil
		}), nil
	case "squish!":
		return NewAutoBuiltin("string.squish!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.squish! does not take arguments")
			}
			updated := stringSquish(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "lstrip":
		return NewAutoBuiltin("string.lstrip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.lstrip does not take arguments")
			}
			return NewString(strings.TrimLeftFunc(receiver.String(), unicode.IsSpace)), nil
		}), nil
	case "lstrip!":
		return NewAutoBuiltin("string.lstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.lstrip! does not take arguments")
			}
			updated := strings.TrimLeftFunc(receiver.String(), unicode.IsSpace)
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "rstrip":
		return NewAutoBuiltin("string.rstrip", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip does not take arguments")
			}
			return NewString(strings.TrimRightFunc(receiver.String(), unicode.IsSpace)), nil
		}), nil
	case "rstrip!":
		return NewAutoBuiltin("string.rstrip!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.rstrip! does not take arguments")
			}
			updated := strings.TrimRightFunc(receiver.String(), unicode.IsSpace)
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "chomp":
		return NewAutoBuiltin("string.chomp", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.chomp accepts at most one separator")
			}
			text := receiver.String()
			if len(args) == 0 {
				return NewString(chompDefault(text)), nil
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.chomp separator must be string")
			}
			sep := args[0].String()
			if sep == "" {
				return NewString(strings.TrimRight(text, "\r\n")), nil
			}
			if strings.HasSuffix(text, sep) {
				return NewString(text[:len(text)-len(sep)]), nil
			}
			return NewString(text), nil
		}), nil
	case "chomp!":
		return NewAutoBuiltin("string.chomp!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.chomp! accepts at most one separator")
			}
			original := receiver.String()
			if len(args) == 0 {
				return stringBangResult(original, chompDefault(original)), nil
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.chomp! separator must be string")
			}
			sep := args[0].String()
			if sep == "" {
				return stringBangResult(original, strings.TrimRight(original, "\r\n")), nil
			}
			if strings.HasSuffix(original, sep) {
				return stringBangResult(original, original[:len(original)-len(sep)]), nil
			}
			return NewNil(), nil
		}), nil
	case "delete_prefix":
		return NewAutoBuiltin("string.delete_prefix", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_prefix expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_prefix prefix must be string")
			}
			return NewString(strings.TrimPrefix(receiver.String(), args[0].String())), nil
		}), nil
	case "delete_prefix!":
		return NewAutoBuiltin("string.delete_prefix!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_prefix! expects exactly one prefix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_prefix! prefix must be string")
			}
			updated := strings.TrimPrefix(receiver.String(), args[0].String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "delete_suffix":
		return NewAutoBuiltin("string.delete_suffix", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_suffix expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_suffix suffix must be string")
			}
			return NewString(strings.TrimSuffix(receiver.String(), args[0].String())), nil
		}), nil
	case "delete_suffix!":
		return NewAutoBuiltin("string.delete_suffix!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.delete_suffix! expects exactly one suffix")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.delete_suffix! suffix must be string")
			}
			updated := strings.TrimSuffix(receiver.String(), args[0].String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "upcase":
		return NewAutoBuiltin("string.upcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.upcase does not take arguments")
			}
			return NewString(strings.ToUpper(receiver.String())), nil
		}), nil
	case "upcase!":
		return NewAutoBuiltin("string.upcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.upcase! does not take arguments")
			}
			updated := strings.ToUpper(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "downcase":
		return NewAutoBuiltin("string.downcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.downcase does not take arguments")
			}
			return NewString(strings.ToLower(receiver.String())), nil
		}), nil
	case "downcase!":
		return NewAutoBuiltin("string.downcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.downcase! does not take arguments")
			}
			updated := strings.ToLower(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "capitalize":
		return NewAutoBuiltin("string.capitalize", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.capitalize does not take arguments")
			}
			return NewString(stringCapitalize(receiver.String())), nil
		}), nil
	case "capitalize!":
		return NewAutoBuiltin("string.capitalize!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.capitalize! does not take arguments")
			}
			updated := stringCapitalize(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "swapcase":
		return NewAutoBuiltin("string.swapcase", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.swapcase does not take arguments")
			}
			return NewString(stringSwapCase(receiver.String())), nil
		}), nil
	case "swapcase!":
		return NewAutoBuiltin("string.swapcase!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.swapcase! does not take arguments")
			}
			updated := stringSwapCase(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "reverse":
		return NewAutoBuiltin("string.reverse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.reverse does not take arguments")
			}
			return NewString(stringReverse(receiver.String())), nil
		}), nil
	case "reverse!":
		return NewAutoBuiltin("string.reverse!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("string.reverse! does not take arguments")
			}
			updated := stringReverse(receiver.String())
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "sub":
		return NewAutoBuiltin("string.sub", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.sub expects pattern and replacement")
			}
			regex, err := stringRegexOption("sub", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub replacement must be string")
			}
			updated, err := stringSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.sub invalid regex: %v", err)
			}
			return NewString(updated), nil
		}), nil
	case "sub!":
		return NewAutoBuiltin("string.sub!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.sub! expects pattern and replacement")
			}
			regex, err := stringRegexOption("sub!", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub! pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.sub! replacement must be string")
			}
			updated, err := stringSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.sub! invalid regex: %v", err)
			}
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "gsub":
		return NewAutoBuiltin("string.gsub", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.gsub expects pattern and replacement")
			}
			regex, err := stringRegexOption("gsub", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub replacement must be string")
			}
			updated, err := stringGSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.gsub invalid regex: %v", err)
			}
			return NewString(updated), nil
		}), nil
	case "gsub!":
		return NewAutoBuiltin("string.gsub!", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 2 {
				return NewNil(), fmt.Errorf("string.gsub! expects pattern and replacement")
			}
			regex, err := stringRegexOption("gsub!", kwargs)
			if err != nil {
				return NewNil(), err
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub! pattern must be string")
			}
			if args[1].Kind() != KindString {
				return NewNil(), fmt.Errorf("string.gsub! replacement must be string")
			}
			updated, err := stringGSub(receiver.String(), args[0].String(), args[1].String(), regex)
			if err != nil {
				return NewNil(), fmt.Errorf("string.gsub! invalid regex: %v", err)
			}
			return stringBangResult(receiver.String(), updated), nil
		}), nil
	case "split":
		return NewAutoBuiltin("string.split", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 1 {
				return NewNil(), fmt.Errorf("string.split accepts at most one separator")
			}
			text := receiver.String()
			var parts []string
			if len(args) == 0 {
				parts = strings.Fields(text)
			} else {
				if args[0].Kind() != KindString {
					return NewNil(), fmt.Errorf("string.split separator must be string")
				}
				parts = strings.Split(text, args[0].String())
			}
			values := make([]Value, len(parts))
			for i, part := range parts {
				values[i] = NewString(part)
			}
			return NewArray(values), nil
		}), nil
	case "template":
		return NewAutoBuiltin("string.template", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("string.template expects exactly one context hash")
			}
			if args[0].Kind() != KindHash && args[0].Kind() != KindObject {
				return NewNil(), fmt.Errorf("string.template context must be hash")
			}
			strict, err := stringTemplateOption(kwargs)
			if err != nil {
				return NewNil(), err
			}
			rendered, err := stringTemplate(receiver.String(), args[0], strict)
			if err != nil {
				return NewNil(), err
			}
			return NewString(rendered), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}
