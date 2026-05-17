package vibes

var (
	_ CapabilityAdapter = (*contextCapability)(nil)
	_ CapabilityAdapter = (*dbCapability)(nil)
	_ CapabilityAdapter = (*eventsCapability)(nil)
	_ CapabilityAdapter = (*jobQueueCapability)(nil)
)

var (
	_ CapabilityContractProvider = (*dbCapability)(nil)
	_ CapabilityContractProvider = (*eventsCapability)(nil)
	_ CapabilityContractProvider = (*jobQueueCapability)(nil)
)

var (
	_ Node       = (*Program)(nil)
	_ Statement  = (*FunctionStmt)(nil)
	_ Expression = (*Identifier)(nil)
	_ StringPart = StringText{}
	_ StringPart = StringExpr{}
)
