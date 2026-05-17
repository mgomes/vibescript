package runtime

var (
	_ CapabilityAdapter = (*contextCapabilityAdapter)(nil)
	_ CapabilityAdapter = (*dbCapabilityAdapter)(nil)
	_ CapabilityAdapter = (*eventsCapability)(nil)
	_ CapabilityAdapter = (*jobQueueCapability)(nil)
)

var (
	_ CapabilityContractProvider = (*dbCapabilityAdapter)(nil)
	_ CapabilityContractProvider = (*eventsCapability)(nil)
	_ CapabilityContractProvider = (*jobQueueCapability)(nil)
)
