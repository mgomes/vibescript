package vibes

import "fmt"

func registerDurationBuiltins(engine *Engine) {
	engine.builtins["Duration"] = NewObject(map[string]Value{
		"build": NewBuiltin("Duration.build", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) == 1 && len(kwargs) == 0 {
				secs, err := numericToSeconds(args[0])
				if err != nil {
					return NewNil(), err
				}
				return NewDuration(Duration{seconds: secs}), nil
			}
			if len(args) > 0 {
				return NewNil(), fmt.Errorf("Duration.build accepts either seconds or named parts, not both") //nolint:staticcheck // class.method reference
			}
			if len(kwargs) == 0 {
				return NewNil(), fmt.Errorf("Duration.build expects seconds or named parts") //nolint:staticcheck // class.method reference
			}
			allowed := map[string]struct{}{
				"weeks":   {},
				"days":    {},
				"hours":   {},
				"minutes": {},
				"seconds": {},
			}
			for key := range kwargs {
				if _, ok := allowed[key]; !ok {
					return NewNil(), fmt.Errorf("Duration.build unknown part %q", key) //nolint:staticcheck // class.method reference
				}
			}

			parsePart := func(name string) (int64, error) {
				if v, ok := kwargs[name]; ok {
					return numericToSeconds(v)
				}
				return 0, nil
			}
			weeks, err := parsePart("weeks")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "weeks", err) //nolint:staticcheck // class.method reference
			}
			days, err := parsePart("days")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "days", err) //nolint:staticcheck // class.method reference
			}
			hours, err := parsePart("hours")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "hours", err) //nolint:staticcheck // class.method reference
			}
			minutes, err := parsePart("minutes")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "minutes", err) //nolint:staticcheck // class.method reference
			}
			seconds, err := parsePart("seconds")
			if err != nil {
				return NewNil(), fmt.Errorf("Duration.build %s: %v", "seconds", err) //nolint:staticcheck // class.method reference
			}
			return NewDuration(durationFromParts(weeks, days, hours, minutes, seconds)), nil
		}),
		"parse": NewBuiltin("Duration.parse", func(exec *Execution, receiver Value, args []Value, kwargs map[string]Value, block Value) (Value, error) {
			if len(args) != 1 || args[0].Kind() != KindString {
				return NewNil(), fmt.Errorf("Duration.parse expects a duration string") //nolint:staticcheck // class.method reference
			}
			parsed, err := parseDurationString(args[0].String())
			if err != nil {
				return NewNil(), err
			}
			return NewDuration(parsed), nil
		}),
	})
}
