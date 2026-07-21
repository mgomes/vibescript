- **Added: `Script.CheckedCall` static gate.** One opt-in API checks the exact
  call — function, argument values, and options — and executes only when the
  checker reports no diagnostics, returning static warnings separately from
  runtime failures. The ordinary Call API stays gradual.
