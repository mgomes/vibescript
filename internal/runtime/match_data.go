package runtime

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const matchDataValuesKey = "\x00matchData.values"

func newMatchData(text string, indices []int) Value {
	values := make([]Value, len(indices)/2)
	starts := make([]Value, len(values))
	ends := make([]Value, len(values))
	for i := range values {
		start := indices[i*2]
		end := indices[i*2+1]
		if start < 0 || end < 0 {
			values[i] = NewNil()
			starts[i] = NewNil()
			ends[i] = NewNil()
			continue
		}
		values[i] = NewString(text[start:end])
		starts[i] = NewInt(int64(utf8.RuneCountInString(text[:start])))
		ends[i] = NewInt(int64(utf8.RuneCountInString(text[:end])))
	}

	captures := make([]Value, 0, max(0, len(values)-1))
	if len(values) > 1 {
		captures = append(captures, values[1:]...)
	}

	preMatch := NewNil()
	postMatch := NewNil()
	if len(indices) >= 2 && indices[0] >= 0 && indices[1] >= 0 {
		preMatch = NewString(text[:indices[0]])
		postMatch = NewString(text[indices[1]:])
	}

	valuesVal := NewArray(values)
	startsVal := NewArray(starts)
	endsVal := NewArray(ends)

	return NewObject(map[string]Value{
		matchDataValuesKey: valuesVal,
		"captures":         NewArray(captures),
		"pre_match":        preMatch,
		"post_match":       postMatch,
		"begin": NewCapturingBuiltin("match_data.begin", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return matchDataOffset("match_data.begin", starts, args, kwargs, block)
		}, startsVal),
		"end": NewCapturingBuiltin("match_data.end", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			return matchDataOffset("match_data.end", ends, args, kwargs, block)
		}, endsVal),
	})
}

func matchDataOffset(name string, offsets, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not accept keyword arguments", name)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s does not accept blocks", name)
	}
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("%s expects a capture index", name)
	}
	index, err := valueToInt(args[0])
	if err != nil {
		return NewNil(), fmt.Errorf("%s capture index must be integer", name)
	}
	if index < 0 {
		index += len(offsets)
	}
	if index < 0 || index >= len(offsets) {
		return NewNil(), fmt.Errorf("%s capture index out of bounds", name)
	}
	return offsets[index], nil
}

func matchDataIndex(obj, index Value) (Value, bool, error) {
	values, ok := obj.Hash()[matchDataValuesKey]
	if !ok || values.Kind() != KindArray {
		return NewNil(), false, nil
	}
	if index.Kind() != KindInt && index.Kind() != KindFloat {
		return NewNil(), false, nil
	}
	i, err := valueToInt(index)
	if err != nil {
		return NewNil(), true, fmt.Errorf("match data index must be integer")
	}
	captures := values.Array()
	if i < 0 {
		i += len(captures)
	}
	if i < 0 || i >= len(captures) {
		return NewNil(), true, nil
	}
	return captures[i], true, nil
}

func newRegexpObject(pattern string) (Value, error) {
	if len(pattern) > maxRegexPatternSize {
		return NewNil(), guardLimitErrorf("Regexp.new pattern exceeds limit %d bytes", maxRegexPatternSize)
	}
	if _, err := compileCachedRegex(pattern); err != nil {
		return NewNil(), fmt.Errorf("Regexp.new invalid regex: %w", err)
	}
	patternValue := NewString(pattern)
	return NewObject(map[string]Value{
		"source": patternValue,
		"match": NewCapturingBuiltin("regexp.match", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(kwargs) > 0 {
				return NewNil(), fmt.Errorf("regexp.match does not accept keyword arguments")
			}
			if !block.IsNil() {
				return NewNil(), fmt.Errorf("regexp.match does not accept blocks")
			}
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("regexp.match expects text")
			}
			if args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("regexp.match text must be string")
			}
			text := args[0].String()
			if err := validateRegexTextPattern("regexp.match", text, pattern); err != nil {
				return NewNil(), err
			}
			indices, err := regexSubmatchFromRuneOffset("regexp.match", text, pattern, 0)
			if err != nil {
				return NewNil(), err
			}
			if indices == nil {
				return NewNil(), nil
			}
			return newMatchData(text, indices), nil
		}, patternValue),
	}), nil
}

func regexpUnionPattern(args []Value) (string, error) {
	if len(args) == 0 {
		return "(?!)", nil
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		if arg.Kind() != KindString {
			return "", fmt.Errorf("Regexp.union expects string patterns")
		}
		parts[i] = regexp.QuoteMeta(arg.String())
	}
	return strings.Join(parts, "|"), nil
}
