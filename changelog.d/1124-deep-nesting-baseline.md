- **Added: a measurement harness for deep-nesting construction under a memory
  quota.** Building a chain of nested arrays is quadratic in depth when a quota
  is in force and linear without one; the benchmark and scaling test state that
  as an executable baseline so an incremental-estimation fix has a pass/fail
  target rather than a table in an issue.
