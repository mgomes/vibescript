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
	seenArrayInline [jsonInlineSeenCapacity]uintptr
	seenHashInline  [jsonInlineSeenCapacity]uintptr
	seenArrays      map[uintptr]struct{}
	seenHashes      map[uintptr]struct{}
	seenArrayLen    int
	seenHashLen     int
	depth           int
	exec            *Execution
}

type jsonValueParser struct {
	raw   string
	pos   int
	depth int
	exec  *Execution
}

type jsonSeenSlot struct {
	id    uintptr
	index int
	inMap bool
}

const (
	jsonInitialContainerCapacity = 4
	jsonInlineSeenCapacity       = 8
)

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

	values := make([]Value, 0, jsonInitialContainerCapacity)
	for {
		value, err := p.parseValue()
		if err != nil {
			return NewNil(), err
		}
		values = append(values, value)
		if err := p.checkMaterialized(NewArray(values)); err != nil {
			return NewNil(), err
		}

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

	values := NewTypedHash(jsonInitialContainerCapacity)
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
		if err := values.HashSet(NewString(key), value); err != nil {
			return NewNil(), err
		}
		if err := p.checkMaterialized(values); err != nil {
			return NewNil(), err
		}

		p.skipWhitespace()
		switch {
		case p.consumeByte(','):
			p.skipWhitespace()
			if p.pos < len(p.raw) && p.raw[p.pos] == '}' {
				return NewNil(), fmt.Errorf("invalid character '}' looking for beginning of object key string")
			}
		case p.consumeByte('}'):
			return values, nil
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

func (p *jsonValueParser) checkMaterialized(value Value) error {
	if p.exec == nil {
		return nil
	}
	return p.exec.checkMemoryWith(value)
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
		return appendJSONString(buf, val.String(), state)
	case KindEnumValue:
		if member := valueEnumValue(val); member != nil {
			return appendJSONString(buf, member.Symbol, state)
		}
		return nil, fmt.Errorf("JSON.stringify unsupported enum value")
	case KindArray:
		arr := val.Array()
		if err := state.enterContainer(); err != nil {
			return nil, err
		}
		defer state.leaveContainer()

		id := reflect.ValueOf(arr).Pointer()
		arraySlot, err := state.pushSeenArray(id)
		if err != nil {
			return nil, err
		}
		defer state.popSeenArray(arraySlot)

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
		if err := state.enterContainer(); err != nil {
			return nil, err
		}
		defer state.leaveContainer()

		id := jsonObjectIdentity(val)
		hashSlot, err := state.pushSeenHash(id)
		if err != nil {
			return nil, err
		}
		defer state.popSeenHash(hashSlot)

		entries, err := jsonObjectEntries(val)
		if err != nil {
			return nil, err
		}

		buf = append(buf, '{')
		for i, entry := range entries {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf, err = appendJSONString(buf, entry.key, state)
			if err != nil {
				return nil, err
			}
			buf = append(buf, ':')
			updated, err := appendJSONValue(buf, entry.value, state)
			if err != nil {
				if errors.Is(err, errJSONMaxDepth) {
					return nil, err
				}
				return nil, fmt.Errorf("JSON.stringify key %q: %w", entry.key, err)
			}
			buf = updated
		}
		return append(buf, '}'), nil
	default:
		return nil, fmt.Errorf("JSON.stringify unsupported value type %s", val.Kind())
	}
}

type jsonObjectEntry struct {
	key     string
	sortKey string
	value   Value
}

type jsonObjectEntriesBySortKey []jsonObjectEntry

func (entries jsonObjectEntriesBySortKey) Len() int {
	return len(entries)
}

func (entries jsonObjectEntriesBySortKey) Less(i, j int) bool {
	return entries[i].sortKey < entries[j].sortKey
}

func (entries jsonObjectEntriesBySortKey) Swap(i, j int) {
	entries[i], entries[j] = entries[j], entries[i]
}

func jsonObjectIdentity(val Value) uintptr {
	if val.Kind() == KindHash {
		if id := hashIdentity(val); id != 0 {
			return id
		}
	}
	return reflect.ValueOf(val.Hash()).Pointer()
}

func jsonObjectEntries(val Value) ([]jsonObjectEntry, error) {
	// Typed hashes carry Ruby-style insertion order, so stringify emits members
	// in that order the way Ruby's JSON.generate does. Legacy hashes and
	// objects have no recorded order and keep sorted keys for determinism.
	if val.Kind() == KindHash && hashHasTypedEntries(val) {
		hashEntries := val.HashEntries()
		entries := make([]jsonObjectEntry, len(hashEntries))
		for i, entry := range hashEntries {
			key, err := valueToHashKey(entry.Key)
			if err != nil {
				return nil, fmt.Errorf("JSON.stringify key: %w", err)
			}
			entries[i] = jsonObjectEntry{key: key, value: entry.Value}
		}
		return entries, nil
	}

	hash := val.Hash()
	entries := make([]jsonObjectEntry, 0, len(hash))
	for key, item := range hash {
		entries = append(entries, jsonObjectEntry{key: key, sortKey: key, value: item})
	}
	sort.Sort(jsonObjectEntriesBySortKey(entries))
	return entries, nil
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

func (state *jsonStringifyState) pushSeenArray(id uintptr) (jsonSeenSlot, error) {
	if id == 0 {
		return jsonSeenSlot{}, nil
	}
	for i := range state.seenArrayLen {
		if state.seenArrayInline[i] == id {
			return jsonSeenSlot{}, fmt.Errorf("JSON.stringify does not support cyclic arrays")
		}
	}
	if _, seen := state.seenArrays[id]; seen {
		return jsonSeenSlot{}, fmt.Errorf("JSON.stringify does not support cyclic arrays")
	}
	if state.seenArrays == nil && state.seenArrayLen < len(state.seenArrayInline) {
		index := state.seenArrayLen
		state.seenArrayInline[index] = id
		state.seenArrayLen++
		return jsonSeenSlot{id: id, index: index}, nil
	}
	if state.seenArrays == nil {
		state.seenArrays = make(map[uintptr]struct{}, len(state.seenArrayInline)+1)
		for _, seenID := range state.seenArrayInline {
			if seenID != 0 {
				state.seenArrays[seenID] = struct{}{}
			}
		}
	}
	state.seenArrays[id] = struct{}{}
	return jsonSeenSlot{id: id, inMap: true}, nil
}

func (state *jsonStringifyState) popSeenArray(slot jsonSeenSlot) {
	if slot.id == 0 {
		return
	}
	if slot.inMap {
		delete(state.seenArrays, slot.id)
		return
	}
	if state.seenArrays != nil {
		delete(state.seenArrays, slot.id)
	}
	last := state.seenArrayLen - 1
	if slot.index >= 0 && slot.index <= last {
		state.seenArrayInline[slot.index] = state.seenArrayInline[last]
		state.seenArrayInline[last] = 0
		state.seenArrayLen--
	}
}

func (state *jsonStringifyState) pushSeenHash(id uintptr) (jsonSeenSlot, error) {
	if id == 0 {
		return jsonSeenSlot{}, nil
	}
	for i := range state.seenHashLen {
		if state.seenHashInline[i] == id {
			return jsonSeenSlot{}, fmt.Errorf("JSON.stringify does not support cyclic objects")
		}
	}
	if _, seen := state.seenHashes[id]; seen {
		return jsonSeenSlot{}, fmt.Errorf("JSON.stringify does not support cyclic objects")
	}
	if state.seenHashes == nil && state.seenHashLen < len(state.seenHashInline) {
		index := state.seenHashLen
		state.seenHashInline[index] = id
		state.seenHashLen++
		return jsonSeenSlot{id: id, index: index}, nil
	}
	if state.seenHashes == nil {
		state.seenHashes = make(map[uintptr]struct{}, len(state.seenHashInline)+1)
		for _, seenID := range state.seenHashInline {
			if seenID != 0 {
				state.seenHashes[seenID] = struct{}{}
			}
		}
	}
	state.seenHashes[id] = struct{}{}
	return jsonSeenSlot{id: id, inMap: true}, nil
}

func (state *jsonStringifyState) popSeenHash(slot jsonSeenSlot) {
	if slot.id == 0 {
		return
	}
	if slot.inMap {
		delete(state.seenHashes, slot.id)
		return
	}
	if state.seenHashes != nil {
		delete(state.seenHashes, slot.id)
	}
	last := state.seenHashLen - 1
	if slot.index >= 0 && slot.index <= last {
		state.seenHashInline[slot.index] = state.seenHashInline[last]
		state.seenHashInline[last] = 0
		state.seenHashLen--
	}
}

func appendJSONString(buf []byte, s string, state *jsonStringifyState) ([]byte, error) {
	const hexDigits = "0123456789abcdef"

	if err := state.checkOutputBytes(len(buf) + 1); err != nil {
		return nil, err
	}
	buf = append(buf, '"')
	start := 0
	for i := 0; i < len(s); {
		if b := s[i]; b < utf8.RuneSelf {
			if b >= 0x20 && b != '\\' && b != '"' && b != '<' && b != '>' && b != '&' {
				i++
				continue
			}

			if err := state.checkOutputBytes(len(buf) + i - start + 6); err != nil {
				return nil, err
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
			if err := state.checkOutputBytes(len(buf) + i - start + len(`\ufffd`)); err != nil {
				return nil, err
			}
			buf = append(buf, s[start:i]...)
			buf = append(buf, `\ufffd`...)
			i++
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			if err := state.checkOutputBytes(len(buf) + i - start + 6); err != nil {
				return nil, err
			}
			buf = append(buf, s[start:i]...)
			buf = append(buf, '\\', 'u', '2', '0', '2', byte('8'+r-'\u2028'))
			i += size
			start = i
			continue
		}
		i += size
	}
	if err := state.checkOutputBytes(len(buf) + len(s) - start + 1); err != nil {
		return nil, err
	}
	buf = append(buf, s[start:]...)
	return append(buf, '"'), nil
}

func (state *jsonStringifyState) checkOutputBytes(size int) error {
	if size > maxJSONPayloadBytes {
		return guardLimitErrorf("JSON.stringify output exceeds limit %d bytes", maxJSONPayloadBytes)
	}
	if state.exec == nil {
		return nil
	}
	return state.exec.checkProjectedStringBytes(size)
}
