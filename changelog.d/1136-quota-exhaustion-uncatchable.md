- **Changed: quota exhaustion is no longer rescuable.** `rescue LimitError`
  (and `RuntimeError`, `Error`, and unions) used to catch a tripped step or
  memory quota, so a loop that rescued the error could absorb the signal that
  its budget was spent and keep burning a fresh operation's worth of work per
  iteration — the memory quota even recovered once the oversized value was
  dropped. Genuine exhaustion (step quota, memory quota, `string.scan`'s
  output cap) now latches the execution: no rescue clause matches it, `ensure`
  bodies and `retry` cannot run work past it, and the host always receives the
  termination. Recursion-limit errors, stdlib input guards, and script-raised
  `raise LimitError` remain rescuable — they describe one rejected operation,
  not a spent budget. The host-visible error is unchanged:
  `*vibes.RuntimeError` with `Type == "LimitError"` and the same messages.
