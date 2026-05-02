package vibes

var _ CapabilityAdapter = (*contextCapability)(nil)
var _ CapabilityAdapter = (*dbCapability)(nil)
var _ CapabilityAdapter = (*eventsCapability)(nil)
var _ CapabilityAdapter = (*jobQueueCapability)(nil)

var _ CapabilityContractProvider = (*dbCapability)(nil)
var _ CapabilityContractProvider = (*eventsCapability)(nil)
var _ CapabilityContractProvider = (*jobQueueCapability)(nil)

var _ Node = (*Program)(nil)
var _ Statement = (*FunctionStmt)(nil)
var _ Expression = (*Identifier)(nil)
var _ StringPart = StringText{}
var _ StringPart = StringExpr{}
