package vibes

import "time"

func (v Value) Kind() ValueKind { return v.kind }

func (v Value) IsNil() bool { return v.kind == KindNil }

func (v Value) Bool() bool {
	if v.kind == KindBool {
		return v.data.(bool)
	}
	return false
}

func (v Value) Int() int64 {
	switch v.kind {
	case KindInt:
		return v.data.(int64)
	case KindFloat:
		return int64(v.data.(float64))
	default:
		return 0
	}
}

func (v Value) Float() float64 {
	switch v.kind {
	case KindFloat:
		return v.data.(float64)
	case KindInt:
		return float64(v.data.(int64))
	default:
		return 0
	}
}

func (v Value) Array() []Value {
	if v.kind != KindArray {
		return nil
	}
	return v.data.([]Value)
}

func (v Value) Hash() map[string]Value {
	if v.kind != KindHash && v.kind != KindObject {
		return nil
	}
	return v.data.(map[string]Value)
}

func (v Value) Class() *ClassDef {
	if v.kind != KindClass {
		return nil
	}
	return v.data.(*ClassDef)
}

func (v Value) Instance() *Instance {
	if v.kind != KindInstance {
		return nil
	}
	return v.data.(*Instance)
}

func (v Value) Money() Money {
	if v.kind != KindMoney {
		return Money{}
	}
	return v.data.(Money)
}

func (v Value) Duration() Duration {
	if v.kind != KindDuration {
		return Duration{}
	}
	return v.data.(Duration)
}

func (v Value) Time() time.Time {
	if v.kind != KindTime {
		return time.Time{}
	}
	return v.data.(time.Time)
}

func (v Value) Range() Range {
	if v.kind != KindRange {
		return Range{}
	}
	return v.data.(Range)
}

func (v Value) Function() *ScriptFunction {
	if v.kind != KindFunction {
		return nil
	}
	return v.data.(*ScriptFunction)
}

func (v Value) Builtin() *Builtin {
	if v.kind != KindBuiltin {
		return nil
	}
	return v.data.(*Builtin)
}

func (v Value) Block() *Block {
	if v.kind != KindBlock {
		return nil
	}
	return v.data.(*Block)
}
