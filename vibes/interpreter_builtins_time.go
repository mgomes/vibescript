package vibes

import (
	"fmt"
	"time"
)

func registerTimeBuiltins(engine *Engine) {
	engine.builtins["Time"] = NewObject(map[string]Value{
		"new": NewBuiltin("Time.new", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			loc := time.Local
			if zone, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(zone)
				if err != nil {
					return NewNil(), err
				}
				if parsed != nil {
					loc = parsed
				}
			}
			t, err := timeFromParts(args, loc)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"local": NewBuiltin("Time.local", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.Local)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"mktime": NewAutoBuiltin("Time.mktime", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.Local)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"utc": NewBuiltin("Time.utc", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.UTC)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"gm": NewAutoBuiltin("Time.gm", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			t, err := timeFromParts(args, time.UTC)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"at": NewBuiltin("Time.at", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 {
				return NewNil(), fmt.Errorf("Time.at expects seconds since epoch") //nolint:staticcheck // class.method reference
			}
			var loc *time.Location
			if in, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(in)
				if err != nil {
					return NewNil(), err
				}
				loc = parsed
			}
			t, err := timeFromEpoch(args[0], loc)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
		"now": NewAutoBuiltin("Time.now", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("Time.now does not take positional arguments") //nolint:staticcheck // class.method reference
			}
			loc := time.Local
			if in, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(in)
				if err != nil {
					return NewNil(), err
				}
				if parsed != nil {
					loc = parsed
				}
			}
			return NewTime(time.Now().In(loc)), nil
		}),
		"parse": NewBuiltin("Time.parse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) < 1 || len(args) > 2 || args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("Time.parse expects a time string and optional layout") //nolint:staticcheck // class.method reference
			}
			for key := range kwargs {
				if key != "in" {
					return NewNil(), fmt.Errorf("Time.parse unknown keyword %q", key) //nolint:staticcheck // class.method reference
				}
			}

			layout := ""
			hasLayout := false
			if len(args) == 2 {
				if args[1].Kind() == KindString {
					layout = args[1].String()
					hasLayout = true
				} else if args[1].Kind() != KindNil {
					return NewNil(), fmt.Errorf("Time.parse layout must be string") //nolint:staticcheck // class.method reference
				}
			}

			var loc *time.Location
			if in, ok := kwargs["in"]; ok {
				parsed, err := parseLocation(in)
				if err != nil {
					return NewNil(), err
				}
				loc = parsed
			}

			t, err := parseTimeString(args[0].String(), layout, hasLayout, loc)
			if err != nil {
				return NewNil(), err
			}
			return NewTime(t), nil
		}),
	})
}
