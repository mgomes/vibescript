- **Improved: builtin member contracts live in one runtime registry.** The
  checker's static member specs and editor member completion now resolve
  from a single runtime-owned contract table (receiver kind, name, aliases,
  call shape, parameter and result types, effect metadata), and a
  registry-completeness test requires every public member to be registered
  or explicitly exempted. Dispatch for unknown receivers and user-defined
  overrides is unchanged.
