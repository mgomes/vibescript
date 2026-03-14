package vibes

// EnumDef represents a user-defined enumeration with named members.
type EnumDef struct {
	Name         string
	Members      map[string]*EnumValueDef
	MembersByKey map[string]*EnumValueDef
	Order        []string
	owner        *Script
}

// EnumValueDef represents a single member within an EnumDef.
type EnumValueDef struct {
	Enum   *EnumDef
	Name   string
	Symbol string
	Index  int
}
