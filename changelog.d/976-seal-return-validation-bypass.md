- **Changed: capability return validation can no longer be bypassed by host
  adapters (#976).** The public `CapabilityMethodContract` no longer has the
  `ReturnValidatedByBuiltin` field, which let any adapter assert an internal
  runtime proof and skip its declared `ValidateReturn`. The runtime now always
  validates capability method returns; first-party adapters that already
  validate and isolate their results record an internal, unforgeable per-call
  proof instead, so they still avoid validating the same value twice.
  Embedders that set the field should delete it — return contracts are now
  enforced unconditionally.
