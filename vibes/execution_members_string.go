package vibes

import (
	"fmt"
)

func stringMember(str Value, property string) (Value, error) {
	switch property {
	case "size", "length", "bytesize", "ord", "chr", "empty?", "clear", "concat", "replace", "start_with?", "end_with?", "include?", "match", "scan", "index", "rindex", "slice":
		return stringMemberQuery(property)
	case "strip", "strip!", "squish", "squish!", "lstrip", "lstrip!", "rstrip", "rstrip!", "chomp", "chomp!", "delete_prefix", "delete_prefix!", "delete_suffix", "delete_suffix!", "upcase", "upcase!", "downcase", "downcase!", "capitalize", "capitalize!", "swapcase", "swapcase!", "reverse", "reverse!":
		return stringMemberTransforms(property)
	case "sub", "sub!", "gsub", "gsub!", "split", "template":
		return stringMemberTextOps(property)
	default:
		return NewNil(), fmt.Errorf("unknown string method %s", property)
	}
}
