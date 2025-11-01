package vibes

import (
	"errors"
	"fmt"
)

func builtinAssert(exec *Execution, receiver Value, args []Value, kwargs map[string]Value) (Value, error) {
	if len(args) == 0 {
		return NewNil(), fmt.Errorf("assert requires a condition argument")
	}
	cond := args[0]
	if cond.Truthy() {
		return NewNil(), nil
	}
	message := "assertion failed"
	if len(args) > 1 {
		message = args[1].String()
	} else if msg, ok := kwargs["message"]; ok {
		message = msg.String()
	}
	return NewNil(), errors.New(message)
}

func builtinMoney(exec *Execution, receiver Value, args []Value, kwargs map[string]Value) (Value, error) {
	if len(args) != 1 {
		return NewNil(), fmt.Errorf("money expects a single string literal")
	}
	lit := args[0]
	if lit.Kind() != KindString {
		return NewNil(), fmt.Errorf("money expects a string literal")
	}
	parsed, err := parseMoneyLiteral(lit.String())
	if err != nil {
		return NewNil(), err
	}
	return NewMoney(parsed), nil
}

func builtinMoneyCents(exec *Execution, receiver Value, args []Value, kwargs map[string]Value) (Value, error) {
	if len(args) != 2 {
		return NewNil(), fmt.Errorf("money_cents expects cents and currency")
	}
	centsVal := args[0]
	currencyVal := args[1]

	cents, err := valueToInt(centsVal)
	if err != nil {
		return NewNil(), fmt.Errorf("money_cents expects integer cents")
	}
	if currencyVal.Kind() != KindString {
		return NewNil(), fmt.Errorf("money_cents expects currency string")
	}

	money, err := newMoneyFromCents(int64(cents), currencyVal.String())
	if err != nil {
		return NewNil(), err
	}
	return NewMoney(money), nil
}
