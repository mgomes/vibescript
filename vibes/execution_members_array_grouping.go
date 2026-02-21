package vibes

import (
	"fmt"
	"sort"
)

func arrayMemberGrouping(property string) (Value, error) {
	switch property {
	case "sort":
		return NewAutoBuiltin("array.sort", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.sort does not take arguments")
			}
			arr := receiver.Array()
			out := make([]Value, len(arr))
			copy(out, arr)
			var sortErr error
			sort.SliceStable(out, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				if block.Block() != nil {
					cmpValue, err := exec.CallBlock(block, []Value{out[i], out[j]})
					if err != nil {
						sortErr = err
						return false
					}
					cmp, err := sortComparisonResult(cmpValue)
					if err != nil {
						sortErr = fmt.Errorf("array.sort block must return numeric comparator")
						return false
					}
					return cmp < 0
				}
				cmp, err := arraySortCompareValues(out[i], out[j])
				if err != nil {
					sortErr = fmt.Errorf("array.sort values are not comparable")
					return false
				}
				return cmp < 0
			})
			if sortErr != nil {
				return NewNil(), sortErr
			}
			return NewArray(out), nil
		}), nil
	case "sort_by":
		return NewAutoBuiltin("array.sort_by", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.sort_by does not take arguments")
			}
			if err := ensureBlock(block, "array.sort_by"); err != nil {
				return NewNil(), err
			}
			type itemWithSortKey struct {
				item  Value
				key   Value
				index int
			}
			arr := receiver.Array()
			withKeys := make([]itemWithSortKey, len(arr))
			for i, item := range arr {
				sortKey, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				withKeys[i] = itemWithSortKey{item: item, key: sortKey, index: i}
			}
			var sortErr error
			sort.SliceStable(withKeys, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				cmp, err := arraySortCompareValues(withKeys[i].key, withKeys[j].key)
				if err != nil {
					sortErr = fmt.Errorf("array.sort_by block values are not comparable")
					return false
				}
				if cmp == 0 {
					return withKeys[i].index < withKeys[j].index
				}
				return cmp < 0
			})
			if sortErr != nil {
				return NewNil(), sortErr
			}
			out := make([]Value, len(withKeys))
			for i, item := range withKeys {
				out[i] = item.item
			}
			return NewArray(out), nil
		}), nil
	case "partition":
		return NewAutoBuiltin("array.partition", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.partition does not take arguments")
			}
			if err := ensureBlock(block, "array.partition"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			left := make([]Value, 0, len(arr))
			right := make([]Value, 0, len(arr))
			for _, item := range arr {
				match, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				if match.Truthy() {
					left = append(left, item)
				} else {
					right = append(right, item)
				}
			}
			return NewArray([]Value{NewArray(left), NewArray(right)}), nil
		}), nil
	case "group_by":
		return NewAutoBuiltin("array.group_by", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.group_by does not take arguments")
			}
			if err := ensureBlock(block, "array.group_by"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			groups := make(map[string][]Value, len(arr))
			for _, item := range arr {
				groupValue, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				key, err := valueToHashKey(groupValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.group_by block must return symbol or string")
				}
				groups[key] = append(groups[key], item)
			}
			result := make(map[string]Value, len(groups))
			for key, items := range groups {
				result[key] = NewArray(items)
			}
			return NewHash(result), nil
		}), nil
	case "group_by_stable":
		return NewAutoBuiltin("array.group_by_stable", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.group_by_stable does not take arguments")
			}
			if err := ensureBlock(block, "array.group_by_stable"); err != nil {
				return NewNil(), err
			}
			arr := receiver.Array()
			order := make([]string, 0, len(arr))
			keyValues := make(map[string]Value, len(arr))
			groups := make(map[string][]Value, len(arr))
			for _, item := range arr {
				groupValue, err := exec.CallBlock(block, []Value{item})
				if err != nil {
					return NewNil(), err
				}
				key, err := valueToHashKey(groupValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.group_by_stable block must return symbol or string")
				}
				if _, exists := groups[key]; !exists {
					order = append(order, key)
					keyValues[key] = groupValue
					groups[key] = []Value{}
				}
				groups[key] = append(groups[key], item)
			}
			result := make([]Value, 0, len(order))
			for _, key := range order {
				result = append(result, NewArray([]Value{
					keyValues[key],
					NewArray(groups[key]),
				}))
			}
			return NewArray(result), nil
		}), nil
	case "tally":
		return NewAutoBuiltin("array.tally", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("array.tally does not take arguments")
			}
			arr := receiver.Array()
			counts := make(map[string]int64, len(arr))
			for _, item := range arr {
				keyValue := item
				if block.Block() != nil {
					mapped, err := exec.CallBlock(block, []Value{item})
					if err != nil {
						return NewNil(), err
					}
					keyValue = mapped
				}
				key, err := valueToHashKey(keyValue)
				if err != nil {
					return NewNil(), fmt.Errorf("array.tally values must be symbol or string")
				}
				counts[key]++
			}
			result := make(map[string]Value, len(counts))
			for key, count := range counts {
				result[key] = NewInt(count)
			}
			return NewHash(result), nil
		}), nil
	default:
		return NewNil(), fmt.Errorf("unknown array method %s", property)
	}
}
