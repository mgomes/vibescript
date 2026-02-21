package vibes

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func builtinJSONParse(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 || args[0].Kind() != KindString {
		return NewNil(), fmt.Errorf("JSON.parse expects a single JSON string argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("JSON.parse does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("JSON.parse does not accept blocks")
	}

	raw := args[0].String()
	if len(raw) > maxJSONPayloadBytes {
		return NewNil(), fmt.Errorf("JSON.parse input exceeds limit %d bytes", maxJSONPayloadBytes)
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return NewNil(), fmt.Errorf("JSON.parse invalid JSON: %v", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return NewNil(), fmt.Errorf("JSON.parse invalid JSON: trailing data")
	}

	value, err := jsonValueToVibeValue(decoded)
	if err != nil {
		return NewNil(), err
	}
	return value, nil
}

func builtinJSONStringify(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("JSON.stringify expects a single value argument")
	}
	if len(kwargs) > 0 {
		return NewNil(), fmt.Errorf("JSON.stringify does not accept keyword arguments")
	}
	if !block.IsNil() {
		return NewNil(), fmt.Errorf("JSON.stringify does not accept blocks")
	}

	state := &jsonStringifyState{
		seenArrays: map[uintptr]struct{}{},
		seenHashes: map[uintptr]struct{}{},
	}
	encoded, err := vibeValueToJSONValue(args[0], state)
	if err != nil {
		return NewNil(), err
	}

	payload, err := json.Marshal(encoded)
	if err != nil {
		return NewNil(), fmt.Errorf("JSON.stringify failed: %v", err)
	}
	if len(payload) > maxJSONPayloadBytes {
		return NewNil(), fmt.Errorf("JSON.stringify output exceeds limit %d bytes", maxJSONPayloadBytes)
	}
	return NewString(string(payload)), nil
}
