- **Hardened the public jobqueue option parser.** `jobqueue.ParseEnqueueOptions`
  now rejects extra enqueue keywords that are not data-only or that contain
  cyclic references instead of cloning them through to the host, closing a
  contract gap for embedders that call it directly. A new
  `jobqueue.ParseEnqueueOptionsValidated` fast path lets the runtime adapter skip
  the redundant walk when it has already enforced the contract, and the carved
  package gained direct unit tests for constructor validation, retry detection,
  option parsing, cloning, and invalid/cyclic values.
