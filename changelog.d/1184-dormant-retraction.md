- **Fixed: a memory quota now counts a frame that a block rebound while it was
  dormant.** The estimator charges a call frame holding only scalars once and
  skips it on later checks, on the understanding that nothing can change it while
  it is dormant. That cached total was dropped only inside the one walk that
  reads `nonBaseParentDepth`, and both a builtin driving a script block and a
  block-iteration region route around that walk, so a block closed over its
  dormant caller could rebind the caller's `Int` to a 400KB string and leave the
  frame charged at its scalar-only 245 bytes. Either shape ran to completion
  under a 404,951-byte quota while retaining two live 400KB strings; both now
  need the 804,361 bytes they hold. The committed total is retracted when a scope
  that could rebind it is pushed, so the invalidation no longer depends on which
  walk the next check takes.
