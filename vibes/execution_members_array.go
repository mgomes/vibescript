package vibes

import "fmt"

func arrayMember(array Value, property string) (Value, error) {
	switch property {
	case "size", "each", "map", "select", "find", "find_index", "reduce", "include?", "index", "rindex", "count", "any?", "all?", "none?":
		return arrayMemberQuery(property)
	case "push", "pop", "uniq", "first", "last", "sum", "compact", "flatten", "chunk", "window", "join", "reverse":
		return arrayMemberTransforms(property)
	case "sort", "sort_by", "partition", "group_by", "group_by_stable", "tally":
		return arrayMemberGrouping(property)
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}
