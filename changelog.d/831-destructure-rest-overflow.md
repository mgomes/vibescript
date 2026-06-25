Fixed a host-crashing panic in destructuring assignment. A destructuring target
with more fixed targets than the value provides plus a rest target (for example
a block parameter `|(a, b, c, *rest)|` applied to a two-element value) sliced out
of range and panicked the interpreter, which a sandboxed script could trigger as
a denial of service. The missing fixed targets now bind to `nil` and the rest is
empty, matching Ruby.
