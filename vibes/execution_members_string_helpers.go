package vibes

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

func chompDefault(text string) string {
	if strings.HasSuffix(text, "\r\n") {
		return text[:len(text)-2]
	}
	if strings.HasSuffix(text, "\n") || strings.HasSuffix(text, "\r") {
		return text[:len(text)-1]
	}
	return text
}

func stringRuneIndex(text, needle string, offset int) int {
	hayRunes := []rune(text)
	needleRunes := []rune(needle)
	if offset < 0 || offset > len(hayRunes) {
		return -1
	}
	if len(needleRunes) == 0 {
		return offset
	}
	limit := len(hayRunes) - len(needleRunes)
	if limit < offset {
		return -1
	}
	for i := offset; i <= limit; i++ {
		match := true
		for j := range len(needleRunes) {
			if hayRunes[i+j] != needleRunes[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func stringRuneRIndex(text, needle string, offset int) int {
	hayRunes := []rune(text)
	needleRunes := []rune(needle)
	if offset < 0 {
		return -1
	}
	if offset > len(hayRunes) {
		offset = len(hayRunes)
	}
	if len(needleRunes) == 0 {
		return offset
	}
	if len(needleRunes) > len(hayRunes) {
		return -1
	}
	start := offset
	maxStart := len(hayRunes) - len(needleRunes)
	if start > maxStart {
		start = maxStart
	}
	for i := start; i >= 0; i-- {
		match := true
		for j := range len(needleRunes) {
			if hayRunes[i+j] != needleRunes[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func stringRuneSlice(text string, start, length int) (string, bool) {
	runes := []rune(text)
	if start < 0 || start >= len(runes) {
		return "", false
	}
	if length < 0 {
		return "", false
	}
	remaining := len(runes) - start
	if length >= remaining {
		return string(runes[start:]), true
	}
	end := start + length
	return string(runes[start:end]), true
}

func stringCapitalize(text string) string {
	runes := []rune(text)
	if len(runes) == 0 {
		return ""
	}
	runes[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

func stringSwapCase(text string) string {
	runes := []rune(text)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			runes[i] = unicode.ToLower(r)
			continue
		}
		if unicode.IsLower(r) {
			runes[i] = unicode.ToUpper(r)
		}
	}
	return string(runes)
}

func stringReverse(text string) string {
	runes := []rune(text)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func stringRegexOption(method string, kwargs map[string]Value) (bool, error) {
	if len(kwargs) == 0 {
		return false, nil
	}
	regexVal, ok := kwargs["regex"]
	if !ok || len(kwargs) > 1 {
		return false, fmt.Errorf("string.%s supports only regex keyword", method)
	}
	if regexVal.Kind() != KindBool {
		return false, fmt.Errorf("string.%s regex keyword must be bool", method)
	}
	return regexVal.Bool(), nil
}

func stringSub(text, pattern, replacement string, regex bool) (string, error) {
	if !regex {
		return strings.Replace(text, pattern, replacement, 1), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	loc := re.FindStringSubmatchIndex(text)
	if loc == nil {
		return text, nil
	}
	replaced := re.ExpandString(nil, replacement, text, loc)
	return text[:loc[0]] + string(replaced) + text[loc[1]:], nil
}

func stringGSub(text, pattern, replacement string, regex bool) (string, error) {
	if !regex {
		return strings.ReplaceAll(text, pattern, replacement), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}
	return re.ReplaceAllString(text, replacement), nil
}

func stringBangResult(original, updated string) Value {
	if updated == original {
		return NewNil()
	}
	return NewString(updated)
}

func stringSquish(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func stringTemplateOption(kwargs map[string]Value) (bool, error) {
	if len(kwargs) == 0 {
		return false, nil
	}
	value, ok := kwargs["strict"]
	if !ok || len(kwargs) != 1 {
		return false, fmt.Errorf("string.template supports only strict keyword")
	}
	if value.Kind() != KindBool {
		return false, fmt.Errorf("string.template strict keyword must be bool")
	}
	return value.Bool(), nil
}

func stringTemplateLookup(context Value, keyPath string) (Value, bool) {
	current := context
	for _, segment := range strings.Split(keyPath, ".") {
		if segment == "" {
			return NewNil(), false
		}
		if current.Kind() != KindHash && current.Kind() != KindObject {
			return NewNil(), false
		}
		next, ok := current.Hash()[segment]
		if !ok {
			return NewNil(), false
		}
		current = next
	}
	return current, true
}

func stringTemplateScalarValue(value Value, keyPath string) (string, error) {
	switch value.Kind() {
	case KindNil, KindBool, KindInt, KindFloat, KindString, KindSymbol, KindMoney, KindDuration, KindTime:
		return value.String(), nil
	default:
		return "", fmt.Errorf("string.template placeholder %s value must be scalar", keyPath)
	}
}

func stringTemplate(text string, context Value, strict bool) (string, error) {
	templateErr := error(nil)
	rendered := stringTemplatePattern.ReplaceAllStringFunc(text, func(match string) string {
		if templateErr != nil {
			return match
		}
		submatch := stringTemplatePattern.FindStringSubmatch(match)
		if len(submatch) != 2 {
			return match
		}
		keyPath := submatch[1]
		value, ok := stringTemplateLookup(context, keyPath)
		if !ok {
			if strict {
				templateErr = fmt.Errorf("string.template missing placeholder %s", keyPath)
			}
			return match
		}
		segment, err := stringTemplateScalarValue(value, keyPath)
		if err != nil {
			templateErr = err
			return match
		}
		return segment
	})
	if templateErr != nil {
		return "", templateErr
	}
	return rendered, nil
}
