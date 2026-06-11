package runtime

import "sync"

type memberTable struct {
	once  sync.Once
	names []string
	table map[string]Value
}

func newMemberTable(names []string) *memberTable {
	return &memberTable{
		names: names,
	}
}

func (t *memberTable) lookup(name string, build func(string) (Value, error)) (Value, bool) {
	t.once.Do(func() {
		t.buildAll(build)
	})
	member, ok := t.table[name]
	return member, ok
}

func (t *memberTable) buildAll(build func(string) (Value, error)) {
	table := make(map[string]Value, len(t.names))
	for _, name := range t.names {
		member, err := build(name)
		if err != nil {
			panic(err)
		}
		table[name] = member
	}
	t.table = table
}
