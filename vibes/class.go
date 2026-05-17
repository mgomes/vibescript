package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// ClassDef represents a user-defined class with its methods and class-level state.
type ClassDef = runtime.ClassDef

// Instance represents a runtime instance of a ClassDef with its own instance variables.
type Instance = runtime.Instance

// NewClass returns a class definition Value.
func NewClass(def *ClassDef) Value { return runtime.NewClass(def) }

// NewInstance returns a class instance Value.
func NewInstance(inst *Instance) Value { return runtime.NewInstance(inst) }

// ClassOf returns the *ClassDef stored in v, or nil if v is not a class.
func ClassOf(v Value) *ClassDef { return runtime.ClassOf(v) }

// InstanceOf returns the *Instance stored in v, or nil.
func InstanceOf(v Value) *Instance { return runtime.InstanceOf(v) }
