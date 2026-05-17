package runtime

import "github.com/mgomes/vibescript/vibes/capability/db"

// Internal aliases for db capability types so runtime code (and tests)
// can keep referring to short names that match the public vibes facade.
type (
	Database        = db.Database
	DatabaseReader  = db.DatabaseReader
	DatabaseWriter  = db.DatabaseWriter
	DBFindRequest   = db.DBFindRequest
	DBQueryRequest  = db.DBQueryRequest
	DBUpdateRequest = db.DBUpdateRequest
	DBSumRequest    = db.DBSumRequest
	DBEachRequest   = db.DBEachRequest
)

// NewDBCapability constructs a database capability adapter bound to the
// provided script-facing name. The vibes facade re-exports this entry
// point under the same name.
func NewDBCapability(name string, impl Database) (CapabilityAdapter, error) {
	cap, err := db.NewCapability(name, impl)
	if err != nil {
		return nil, err
	}
	return &dbCapabilityAdapter{cap: cap}, nil
}

// MustNewDBCapability is the panicking variant of NewDBCapability.
func MustNewDBCapability(name string, impl Database) CapabilityAdapter {
	cap, err := NewDBCapability(name, impl)
	if err != nil {
		panic(err)
	}
	return cap
}

type dbCapabilityAdapter struct {
	cap *db.Capability
}

func (a *dbCapabilityAdapter) Bind(_ CapabilityBinding) (map[string]Value, error) {
	name := a.cap.Name()
	methods := map[string]Value{
		"find":   NewBuiltin(name+".find", a.wrapCall(a.cap.CallFind)),
		"query":  NewBuiltin(name+".query", a.wrapCall(a.cap.CallQuery)),
		"update": NewBuiltin(name+".update", a.wrapCall(a.cap.CallUpdate)),
		"sum":    NewBuiltin(name+".sum", a.wrapCall(a.cap.CallSum)),
		"each":   NewBuiltin(name+".each", a.wrapCall(a.cap.CallEach)),
	}
	return map[string]Value{name: NewObject(methods)}, nil
}

func (a *dbCapabilityAdapter) CapabilityContracts() map[string]CapabilityMethodContract {
	src := a.cap.Contracts()
	out := make(map[string]CapabilityMethodContract, len(src))
	for k, v := range src {
		out[k] = CapabilityMethodContract{
			ValidateArgs:   v.ValidateArgs,
			ValidateReturn: v.ValidateReturn,
		}
	}
	return out
}

func (a *dbCapabilityAdapter) wrapCall(fn func(db.ExecutionContext, []Value, map[string]Value, Value) (Value, error)) BuiltinFunc {
	return func(exec *Execution, _ Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
		return fn(exec, args, kwargs, block)
	}
}
