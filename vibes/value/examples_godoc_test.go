package value_test

import (
	"fmt"

	"github.com/mgomes/vibescript/vibes/value"
)

func ExampleNewInt() {
	v := value.NewInt(42)
	fmt.Println(v.String())
	// Output: 42
}

func ExampleNewString() {
	v := value.NewString("hello")
	fmt.Println(v.String())
	// Output: hello
}

func ExampleNewArray() {
	v := value.NewArray([]value.Value{
		value.NewInt(1),
		value.NewInt(2),
		value.NewInt(3),
	})
	fmt.Println(v.String())
	// Output: [1, 2, 3]
}

func ExampleNewHash() {
	v := value.NewHash(map[string]value.Value{
		"name": value.NewString("acme"),
	})
	fmt.Println(v.String())
	// Output: {name: acme}
}

func ExampleNewMoney() {
	amount, err := value.NewMoneyFromCents(1999, "USD")
	if err != nil {
		panic(err)
	}
	v := value.NewMoney(amount)
	fmt.Println(v.String())
	// Output: 19.99 USD
}

func ExampleNewDuration() {
	v := value.NewDuration(value.DurationFromSeconds(90))
	fmt.Println(v.String())
	// Output: 90s
}

func ExampleValue_String() {
	v := value.NewString("hello")
	fmt.Println(v.String())
	// Output: hello
}

// ExampleValue_Equal contrasts equal and unequal Values across kinds.
func ExampleValue_Equal() {
	a := value.NewInt(1)
	b := value.NewInt(1)
	c := value.NewString("1")
	fmt.Println(a.Equal(b))
	fmt.Println(a.Equal(c))
	// Output:
	// true
	// false
}
