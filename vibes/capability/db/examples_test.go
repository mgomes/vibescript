package db_test

import (
	"context"
	"fmt"

	"github.com/mgomes/vibescript/vibes"
	"github.com/mgomes/vibescript/vibes/capability/db"
	"github.com/mgomes/vibescript/vibes/value"
)

// stubDB returns canned responses for the godoc Examples. Real embedders
// would query an actual database, ORM, or external API.
type stubDB struct{}

func (stubDB) Find(_ context.Context, req db.DBFindRequest) (value.Value, error) {
	return value.NewHash(map[string]value.Value{
		"id":   req.ID,
		"name": value.NewString("Ada"),
	}), nil
}

func (stubDB) Query(_ context.Context, _ db.DBQueryRequest) (value.Value, error) {
	return value.NewArray(nil), nil
}

func (stubDB) Update(_ context.Context, _ db.DBUpdateRequest) (value.Value, error) {
	return value.NewBool(true), nil
}

func (stubDB) Sum(_ context.Context, _ db.DBSumRequest) (value.Value, error) {
	return value.NewInt(0), nil
}

func (stubDB) Each(_ context.Context, _ db.DBEachRequest) ([]value.Value, error) {
	return nil, nil
}

func ExampleNewCapability() {
	cap, err := db.NewCapability("users", stubDB{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println(cap.Name())
	// Output: users
}

func ExampleMustNewCapability() {
	cap := db.MustNewCapability("users", stubDB{})
	fmt.Println(cap.Name())
	// Output: users
}

// Example shows how to wire a db.Capability into a script invocation
// via the vibes facade and observe the find dispatch.
func Example() {
	engine := vibes.MustNewEngine(vibes.Config{})
	script, err := engine.Compile(`def run()
  user = users.find("Player", "p-1")
  user[:name]
end`)
	if err != nil {
		fmt.Println("compile:", err)
		return
	}

	result, err := script.Call(context.Background(), "run", nil, vibes.CallOptions{
		Capabilities: []vibes.CapabilityAdapter{
			vibes.MustNewDBCapability("users", stubDB{}),
		},
	})
	if err != nil {
		fmt.Println("call:", err)
		return
	}
	fmt.Println(result.String())
	// Output: Ada
}
