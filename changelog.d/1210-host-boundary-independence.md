- **Changed: the host boundary hands out independent values.** Every collection
  crossing between host Go code and script state is now independent of the
  other side: `Script.Call` results clone whenever their graph is shared,
  host-builtin arguments isolate from every script slot, host-builtin returns
  and values exchanged through `Execution.CallBlock` detach from any backing
  the host retains, and wrappers a factory installs into its capability object
  never transfer out live. Two embedder-visible channels are gone with this: a
  builtin can no longer publish behavior or results by mutating an argument
  (write into the receiver, the sanctioned factory channel, or return the
  value), and host writes through retained handles no longer reach
  script-observable state. Builtins that declare `DeclareNonRetaining` keep
  copy-free crossings; first-party capability adapters declare non-mutation,
  so their single internal copy is the only one. Adapters should not
  defensively pre-clone returns anymore -- the boundary detaches them. (#1210)
