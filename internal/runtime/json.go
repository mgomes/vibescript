package runtime

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type jsonStringifyState struct {
	seenArrays map[uintptr]struct{}
	seenHashes map[uintptr]struct{}
	depth      int
}

type jsonValueParser struct {
	raw   string
	pos   int
	depth int
}

type jsonInvalidNumberError string

func (e jsonInvalidNumberError) Error() string {
	return fmt.Sprintf("JSON.parse invalid number %q", string(e))
}

var errJSONMaxDepth = &guardLimitError{err: errors.New("exceeded max depth")}

func (p *jsonValueParser) parse() (Value, error) {
	p.skipWhitespace()
	value, err := p.parseValue()
	if err != nil {
		return NewNil(), err
	}
	p.skipWhitespace()
	if p.pos != len(p.raw) {
		return NewNil(), fmt.Errorf("trailing data")
	}
	return value, nil
}

func (p *jsonValueParser) parseValue() (Value, error) {
	if p.pos >= len(p.raw) {
		return NewNil(), fmt.Errorf("unexpected end of JSON input")
	}

	switch p.raw[p.pos] {
	case 'n':
		if p.consumeLiteral("null") {
			return NewNil(), nil
		}
	case 't':
		if p.consumeLiteral("true") {
			return NewBool(true), nil
		}
	case 'f':
		if p.consumeLiteral("false") {
			return NewBool(false), nil
		}
	case '"':
		s, err := p.parseString()
		if err != nil {
			return NewNil(), err
		}
		return NewString(s), nil
	case '[':
		return p.parseArray()
	case '{':
		return p.parseObject()
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return p.parseNumber()
	}

	return NewNil(), fmt.Errorf("invalid character %q looking for beginning of value", p.raw[p.pos])
}

func (p *jsonValueParser) parseArray() (Value, error) {
	if err := p.enterContainer(); err != nil {
		return NewNil(), err
	}
	defer p.leaveContainer()

	p.pos++
	p.skipWhitespace()
	if p.consumeByte(']') {
		return NewArray(nil), nil
	}

	values := []Value{}
	for {
		value, err := p.parseValue()
		if err != nil {
			return NewNil(), err
		}
		values = append(values, value)

		p.skipWhitespace()
		switch {
		case p.consumeByte(','):
			p.skipWhitespace()
			if p.pos < len(p.raw) && p.raw[p.pos] == ']' {
				return NewNil(), fmt.Errorf("invalid character ']' looking for beginning of value")
			}
		case p.consumeByte(']'):
			return NewArray(values), nil
		default:
			if p.pos >= len(p.raw) {
				return NewNil(), fmt.Errorf("unexpected end of JSON input")
			}
			return NewNil(), fmt.Errorf("invalid character %q after array element", p.raw[p.pos])
		}
	}
}

func (p *jsonValueParser) parseObject() (Value, error) {
	if err := p.enterContainer(); err != nil {
		return NewNil(), err
	}
	defer p.leaveContainer()

	p.pos++
	p.skipWhitespace()
	if p.consumeByte('}') {
		return NewHash(nil), nil
	}

	values := map[string]Value{}
	for {
		if p.pos >= len(p.raw) {
			return NewNil(), fmt.Errorf("unexpected end of JSON input")
		}
		if p.raw[p.pos] != '"' {
			return NewNil(), fmt.Errorf("invalid character %q looking for beginning of object key string", p.raw[p.pos])
		}
		key, err := p.parseString()
		if err != nil {
			return NewNil(), err
		}

		p.skipWhitespace()
		if !p.consumeByte(':') {
			if p.pos >= len(p.raw) {
				return NewNil(), fmt.Errorf("unexpected end of JSON input")
			}
			return NewNil(), fmt.Errorf("invalid character %q after object key", p.raw[p.pos])
		}

		p.skipWhitespace()
		value, err := p.parseValue()
		if err != nil {
			return NewNil(), err
		}
		values[key] = value

		p.skipWhitespace()
		switch {
		case p.consumeByte(','):
			p.skipWhitespace()
			if p.pos < len(p.raw) && p.raw[p.pos] == '}' {
				return NewNil(), fmt.Errorf("invalid character '}' looking for beginning of object key string")
			}
		case p.consumeByte('}'):
			return NewHash(values), nil
		default:
			if p.pos >= len(p.raw) {
				return NewNil(), fmt.Errorf("unexpected end of JSON input")
			}
			return NewNil(), fmt.Errorf("invalid character %q after object value", p.raw[p.pos])
		}
	}
}

func (p *jsonValueParser) parseNumber() (Value, error) {
	start := p.pos
	if p.consumeByte('-') && p.pos >= len(p.raw) {
		return NewNil(), fmt.Errorf("invalid number %q", p.raw[start:p.pos])
	}

	if p.consumeByte('0') {
		if p.pos < len(p.raw) && isJSONDigit(p.raw[p.pos]) {
			return NewNil(), fmt.Errorf("invalid number %q", p.raw[start:p.pos+1])
		}
	} else if p.pos < len(p.raw) && isJSONOneToNine(p.raw[p.pos]) {
		p.pos++
		for p.pos < len(p.raw) && isJSONDigit(p.raw[p.pos]) {
			p.pos++
		}
	} else {
		return NewNil(), fmt.Errorf("invalid number %q", p.raw[start:p.pos])
	}

	floatLike := false
	if p.consumeByte('.') {
		floatLike = true
		if p.pos >= len(p.raw) || !isJSONDigit(p.raw[p.pos]) {
			return NewNil(), fmt.Errorf("invalid number %q", p.raw[start:p.pos])
		}
		for p.pos < len(p.raw) && isJSONDigit(p.raw[p.pos]) {
			p.pos++
		}
	}

	if p.pos < len(p.raw) && (p.raw[p.pos] == 'e' || p.raw[p.pos] == 'E') {
		floatLike = true
		p.pos++
		if p.pos < len(p.raw) && (p.raw[p.pos] == '+' || p.raw[p.pos] == '-') {
			p.pos++
		}
		if p.pos >= len(p.raw) || !isJSONDigit(p.raw[p.pos]) {
			return NewNil(), fmt.Errorf("invalid number %q", p.raw[start:p.pos])
		}
		for p.pos < len(p.raw) && isJSONDigit(p.raw[p.pos]) {
			p.pos++
		}
	}

	literal := p.raw[start:p.pos]
	if !floatLike {
		if i, err := strconv.ParseInt(literal, 10, 64); err == nil {
			return NewInt(i), nil
		}
	}

	f, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		return NewNil(), jsonInvalidNumberError(literal)
	}
	return NewFloat(f), nil
}

func (p *jsonValueParser) parseString() (string, error) {
	p.pos++
	start := p.pos
	for p.pos < len(p.raw) {
		b := p.raw[p.pos]
		switch {
		case b == '"':
			value := p.raw[start:p.pos]
			p.pos++
			return value, nil
		case b == '\\':
			return p.parseEscapedString(start)
		case b < 0x20:
			return "", fmt.Errorf("invalid character %q in string literal", b)
		case b < utf8.RuneSelf:
			p.pos++
		default:
			r, size := utf8.DecodeRuneInString(p.raw[p.pos:])
			if r == utf8.RuneError && size == 1 {
				return p.parseEscapedString(start)
			}
			p.pos += size
		}
	}
	return "", fmt.Errorf("unexpected end of JSON input")
}

func (p *jsonValueParser) parseEscapedString(start int) (string, error) {
	var b strings.Builder
	b.Grow(len(p.raw) - start)
	b.WriteString(p.raw[start:p.pos])

	for p.pos < len(p.raw) {
		c := p.raw[p.pos]
		switch {
		case c == '"':
			p.pos++
			return b.String(), nil
		case c == '\\':
			p.pos++
			r, err := p.parseStringEscape()
			if err != nil {
				return "", err
			}
			b.WriteRune(r)
		case c < 0x20:
			return "", fmt.Errorf("invalid character %q in string literal", c)
		case c < utf8.RuneSelf:
			b.WriteByte(c)
			p.pos++
		default:
			r, size := utf8.DecodeRuneInString(p.raw[p.pos:])
			if r == utf8.RuneError && size == 1 {
				b.WriteRune(utf8.RuneError)
				p.pos++
				continue
			}
			b.WriteRune(r)
			p.pos += size
		}
	}
	return "", fmt.Errorf("unexpected end of JSON input")
}

func (p *jsonValueParser) parseStringEscape() (rune, error) {
	if p.pos >= len(p.raw) {
		return 0, fmt.Errorf("unexpected end of JSON input")
	}

	switch c := p.raw[p.pos]; c {
	case '"', '\\', '/':
		p.pos++
		return rune(c), nil
	case 'b':
		p.pos++
		return '\b', nil
	case 'f':
		p.pos++
		return '\f', nil
	case 'n':
		p.pos++
		return '\n', nil
	case 'r':
		p.pos++
		return '\r', nil
	case 't':
		p.pos++
		return '\t', nil
	case 'u':
		p.pos++
		r, err := p.parseUnicodeEscape()
		if err != nil {
			return 0, err
		}
		return r, nil
	default:
		return 0, fmt.Errorf("invalid character %q in string escape code", c)
	}
}

func (p *jsonValueParser) parseUnicodeEscape() (rune, error) {
	r, err := p.readHexRune()
	if err != nil {
		return 0, err
	}
	if r < 0xd800 || r > 0xdfff {
		return r, nil
	}
	if r > 0xdbff {
		return utf8.RuneError, nil
	}
	if p.pos+2 > len(p.raw) || p.raw[p.pos] != '\\' || p.raw[p.pos+1] != 'u' {
		return utf8.RuneError, nil
	}

	save := p.pos
	p.pos += 2
	low, err := p.readHexRune()
	if err != nil {
		p.pos = save
		return utf8.RuneError, nil
	}
	if low < 0xdc00 || low > 0xdfff {
		p.pos = save
		return utf8.RuneError, nil
	}
	return utf16.DecodeRune(r, low), nil
}

func (p *jsonValueParser) readHexRune() (rune, error) {
	if p.pos+4 > len(p.raw) {
		return 0, fmt.Errorf("unexpected end of JSON input")
	}
	var r rune
	for range 4 {
		c := p.raw[p.pos]
		p.pos++
		value, ok := jsonHexValue(c)
		if !ok {
			return 0, fmt.Errorf("invalid character %q in unicode escape", c)
		}
		r = r<<4 | rune(value)
	}
	return r, nil
}

func (p *jsonValueParser) skipWhitespace() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\n', '\r', '\t':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonValueParser) consumeLiteral(literal string) bool {
	if !strings.HasPrefix(p.raw[p.pos:], literal) {
		return false
	}
	p.pos += len(literal)
	return true
}

func (p *jsonValueParser) consumeByte(b byte) bool {
	if p.pos < len(p.raw) && p.raw[p.pos] == b {
		p.pos++
		return true
	}
	return false
}

func (p *jsonValueParser) enterContainer() error {
	if p.depth >= maxJSONNestingDepth {
		return errJSONMaxDepth
	}
	p.depth++
	return nil
}

func (p *jsonValueParser) leaveContainer() {
	p.depth--
}

func isJSONDigit(c byte) bool {
	return c >= '0' && c <= '9'
}

func isJSONOneToNine(c byte) bool {
	return c >= '1' && c <= '9'
}

func jsonHexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func appendJSONValue(buf []byte, val Value, state *jsonStringifyState) ([]byte, error) {
	switch val.Kind() {
	case KindNil:
		return append(buf, "null"...), nil
	case KindBool:
		if val.Bool() {
			return append(buf, "true"...), nil
		}
		return append(buf, "false"...), nil
	case KindInt:
		return strconv.AppendInt(buf, val.Int(), 10), nil
	case KindFloat:
		f := val.Float()
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("JSON.stringify failed: json: unsupported value: %s", formatFloat(f))
		}
		return appendJSONFloat(buf, f), nil
	case KindString, KindSymbol:
		return appendJSONString(buf, val.String()), nil
	case KindEnumValue:
		if member := valueEnumValue(val); member != nil {
			return appendJSONString(buf, member.Symbol), nil
		}
		return nil, fmt.Errorf("JSON.stringify unsupported enum value")
	case KindArray:
		arr := val.Array()
		if err := state.enterContainer(); err != nil {
			return nil, err
		}
		defer state.leaveContainer()

		id := reflect.ValueOf(arr).Pointer()
		if id != 0 {
			if _, seen := state.seenArrays[id]; seen {
				return nil, fmt.Errorf("JSON.stringify does not support cyclic arrays")
			}
			state.seenArrays[id] = struct{}{}
			defer delete(state.seenArrays, id)
		}

		buf = append(buf, '[')
		for i, item := range arr {
			if i > 0 {
				buf = append(buf, ',')
			}
			updated, err := appendJSONValue(buf, item, state)
			if err != nil {
				if errors.Is(err, errJSONMaxDepth) {
					return nil, err
				}
				return nil, fmt.Errorf("JSON.stringify array index %d: %w", i, err)
			}
			buf = updated
		}
		return append(buf, ']'), nil
	case KindHash, KindObject:
		hash := val.Hash()
		if err := state.enterContainer(); err != nil {
			return nil, err
		}
		defer state.leaveContainer()

		id := reflect.ValueOf(hash).Pointer()
		if id != 0 {
			if _, seen := state.seenHashes[id]; seen {
				return nil, fmt.Errorf("JSON.stringify does not support cyclic objects")
			}
			state.seenHashes[id] = struct{}{}
			defer delete(state.seenHashes, id)
		}

		keys := make([]string, 0, len(hash))
		for key := range hash {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		buf = append(buf, '{')
		for i, key := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendJSONString(buf, key)
			buf = append(buf, ':')
			updated, err := appendJSONValue(buf, hash[key], state)
			if err != nil {
				if errors.Is(err, errJSONMaxDepth) {
					return nil, err
				}
				return nil, fmt.Errorf("JSON.stringify key %q: %w", key, err)
			}
			buf = updated
		}
		return append(buf, '}'), nil
	default:
		return nil, fmt.Errorf("JSON.stringify unsupported value type %s", val.Kind())
	}
}

func appendJSONFloat(buf []byte, f float64) []byte {
	format := byte('f')
	abs := math.Abs(f)
	if abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}

	buf = strconv.AppendFloat(buf, f, format, -1, 64)
	if format == 'e' {
		n := len(buf)
		if n >= 4 && buf[n-4] == 'e' && buf[n-3] == '-' && buf[n-2] == '0' {
			buf[n-2] = buf[n-1]
			buf = buf[:n-1]
		}
	}
	return buf
}

func (state *jsonStringifyState) enterContainer() error {
	if state.depth >= maxJSONNestingDepth {
		return fmt.Errorf("JSON.stringify %w", errJSONMaxDepth)
	}
	state.depth++
	return nil
}

func (state *jsonStringifyState) leaveContainer() {
	state.depth--
}

func appendJSONString(buf []byte, s string) []byte {
	const hexDigits = "0123456789abcdef"

	buf = append(buf, '"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if b >= 0x20 && b != '\\' && b != '"' && b != '<' && b != '>' && b != '&' {
				i++
				continue
			}

			buf = append(buf, s[start:i]...)
			switch b {
			case '\\', '"':
				buf = append(buf, '\\', b)
			case '\b':
				buf = append(buf, '\\', 'b')
			case '\f':
				buf = append(buf, '\\', 'f')
			case '\n':
				buf = append(buf, '\\', 'n')
			case '\r':
				buf = append(buf, '\\', 'r')
			case '\t':
				buf = append(buf, '\\', 't')
			default:
				buf = append(buf, '\\', 'u', '0', '0', hexDigits[b>>4], hexDigits[b&0x0f])
			}
			i++
			start = i
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			buf = append(buf, s[start:i]...)
			buf = append(buf, `\ufffd`...)
			i++
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			buf = append(buf, s[start:i]...)
			buf = append(buf, '\\', 'u', '2', '0', '2', byte('8'+r-'\u2028'))
			i += size
			start = i
			continue
		}
		i += size
	}
	buf = append(buf, s[start:]...)
	return append(buf, '"')
}
