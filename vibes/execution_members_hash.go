package vibes

import (
	"fmt"
	"sort"
)

func hashMember(obj Value, property string) (Value, error) {
	switch property {
	case "size", "length", "empty?", "key?", "has_key?", "include?", "keys", "values", "fetch", "dig", "each", "each_key", "each_value":
		return hashMemberQuery(property)
	case "merge", "slice", "except", "select", "reject", "transform_keys", "deep_transform_keys", "remap_keys", "transform_values", "compact":
		return hashMemberTransforms(property)
	default:
		return NewNil(), fmt.Errorf("unknown hash method %s", property)
	}
}

func sortedHashKeys(entries map[string]Value) []string {
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
