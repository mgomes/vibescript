- **Added: static local type inference on the check path (ADR-004).** Locals
  now take the types of the expressions assigned to them, and `vibes run
  -check` errors wherever known types contradict a typed boundary: wrong
  argument types through local bindings, annotated returns, conflicting
  sequential reassignment, and operators whose operand kinds the runtime
  provably rejects. Sibling branches merge into unions, loop and block bodies
  degrade to unknown, and unknown values (JSON payloads, host globals, dynamic
  dispatch) are never rejected — the runtime checks remain the final guard.
- **Added: `vibes check <script>`.** A top-level command that reports every
  statically checkable contract issue across the whole script — all functions,
  class methods, and top-level code — without executing anything, printing
  `path:line:column: message (function)` per issue and exiting non-zero for CI
  and deployment gates.
- **Added: `JSON.parse_as(raw, shape)`.** Parses JSON and validates the result
  against a shape in one step, raising the standard typed-boundary error on
  mismatch; the checker treats its result as that shape, so downstream field
  access is inferred and checked. Shape literals are now legal in expression
  position: a braced group whose field values all name built-in types is a
  first-class shape value, while hashes with value expressions or unknown
  identifiers keep their existing semantics.
