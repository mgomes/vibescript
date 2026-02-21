package vibes

import (
	"fmt"
	"regexp"
	"unicode/utf8"
)

func builtinRegexMatch(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("Regex.match expects pattern and text")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("Regex.match does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("Regex.match does not accept blocks")
	}
	if args[0].Kind() != KindString || args[1].Kind() != KindString {
		return NewNil(), fmt.Errorf("Regex.match expects string pattern and text")
	}
	pattern := args[0].String()
	text := args[1].String()
	if len(pattern) > maxRegexPatternSize {
		return NewNil(), fmt.Errorf("Regex.match pattern exceeds limit %d bytes", maxRegexPatternSize)
	}
	if len(text) > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("Regex.match text exceeds limit %d bytes", maxRegexInputBytes)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return NewNil(), fmt.Errorf("Regex.match invalid regex: %v", err)
	}
	indices := re.FindStringIndex(text)
	if indices == nil {
		return NewNil(), nil
	}
	return NewString(text[indices[0]:indices[1]]), nil
}

func builtinRegexReplace(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return builtinRegexReplaceInternal(args, kwargs, block, false)
}

func builtinRegexReplaceAll(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	return builtinRegexReplaceInternal(args, kwargs, block, true)
}

func builtinRegexReplaceInternal(args []Value, kwargs map[string]Value, block Value, replaceAll bool) (Value, error) {
	method := "Regex.replace"
	if replaceAll {
		method = "Regex.replace_all"
	}

	if len(args) != 3 {
		return NewNil(), fmt.Errorf("%s expects text, pattern, replacement", method)
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("%s does not accept keyword arguments", method)
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("%s does not accept blocks", method)
	}
	if args[0].Kind() != KindString || args[1].Kind() != KindString || args[2].Kind() != KindString {
		return NewNil(), fmt.Errorf("%s expects string text, pattern, replacement", method)
	}

	text := args[0].String()
	pattern := args[1].String()
	replacement := args[2].String()
	if len(pattern) > maxRegexPatternSize {
		return NewNil(), fmt.Errorf("%s pattern exceeds limit %d bytes", method, maxRegexPatternSize)
	}
	if len(text) > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("%s text exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	if len(replacement) > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("%s replacement exceeds limit %d bytes", method, maxRegexInputBytes)
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return NewNil(), fmt.Errorf("%s invalid regex: %v", method, err)
	}

	if replaceAll {
		replaced, err := regexReplaceAllWithLimit(re, text, replacement, method)
		if err != nil {
			return NewNil(), err
		}
		return NewString(replaced), nil
	}

	loc := re.FindStringSubmatchIndex(text)
	if loc == nil {
		return NewString(text), nil
	}
	replaced := string(re.ExpandString(nil, replacement, text, loc))
	outputLen := len(text) - (loc[1] - loc[0]) + len(replaced)
	if outputLen > maxRegexInputBytes {
		return NewNil(), fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	return NewString(text[:loc[0]] + replaced + text[loc[1]:]), nil
}

func regexReplaceAllWithLimit(re *regexp.Regexp, text string, replacement string, method string) (string, error) {
	out := make([]byte, 0, len(text))
	lastAppended := 0
	searchStart := 0
	lastMatchEnd := -1
	for searchStart <= len(text) {
		loc, found := nextRegexReplaceAllSubmatchIndex(re, text, searchStart)
		if !found {
			break
		}
		if loc[0] == loc[1] && loc[0] == lastMatchEnd {
			if loc[0] >= len(text) {
				break
			}
			_, size := utf8.DecodeRuneInString(text[loc[0]:])
			if size == 0 {
				size = 1
			}
			searchStart = loc[0] + size
			continue
		}

		segmentLen := loc[0] - lastAppended
		if len(out) > maxRegexInputBytes-segmentLen {
			return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
		}
		out = append(out, text[lastAppended:loc[0]]...)
		out = re.ExpandString(out, replacement, text, loc)
		if len(out) > maxRegexInputBytes {
			return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
		}
		lastAppended = loc[1]
		lastMatchEnd = loc[1]

		if loc[1] > loc[0] {
			searchStart = loc[1]
			continue
		}
		if loc[1] >= len(text) {
			break
		}
		_, size := utf8.DecodeRuneInString(text[loc[1]:])
		if size == 0 {
			size = 1
		}
		searchStart = loc[1] + size
	}

	tailLen := len(text) - lastAppended
	if len(out) > maxRegexInputBytes-tailLen {
		return "", fmt.Errorf("%s output exceeds limit %d bytes", method, maxRegexInputBytes)
	}
	out = append(out, text[lastAppended:]...)
	return string(out), nil
}

func nextRegexReplaceAllSubmatchIndex(re *regexp.Regexp, text string, start int) ([]int, bool) {
	loc := re.FindStringSubmatchIndex(text[start:])
	if loc == nil {
		return nil, false
	}
	direct := offsetRegexSubmatchIndex(loc, start)
	if start == 0 || direct[0] > start {
		return direct, true
	}

	windowStart := start - 1
	locs := re.FindAllStringSubmatchIndex(text[windowStart:], 2)
	if len(locs) == 0 {
		return nil, false
	}

	first := offsetRegexSubmatchIndex(locs[0], windowStart)
	if first[0] >= start {
		return first, true
	}
	if first[1] > start {
		return direct, true
	}
	if len(locs) < 2 {
		return nil, false
	}
	second := offsetRegexSubmatchIndex(locs[1], windowStart)
	if second[0] >= start {
		return second, true
	}
	return nil, false
}

func offsetRegexSubmatchIndex(loc []int, offset int) []int {
	abs := make([]int, len(loc))
	for i, index := range loc {
		if index < 0 {
			abs[i] = -1
			continue
		}
		abs[i] = index + offset
	}
	return abs
}
