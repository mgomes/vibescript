package vibes

type EnumDef struct {
	Name         string
	Members      map[string]*EnumValueDef
	MembersByKey map[string]*EnumValueDef
	Order        []string
	owner        *Script
}

type EnumValueDef struct {
	Enum   *EnumDef
	Name   string
	Symbol string
	Index  int
}
