package vibes

import "github.com/mgomes/vibescript/internal/runtime"

// CapabilityAdapter binds host capabilities into a script invocation.
type CapabilityAdapter = runtime.CapabilityAdapter

// CapabilityMethodContract validates capability method calls at the boundary.
type CapabilityMethodContract = runtime.CapabilityMethodContract

// CapabilityContractProvider exposes per-method contracts for capability adapters.
type CapabilityContractProvider = runtime.CapabilityContractProvider

// CapabilityBinding provides execution context for adapters during binding.
type CapabilityBinding = runtime.CapabilityBinding
