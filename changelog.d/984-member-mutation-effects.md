- **Improved: the checker keeps container facts across pure member calls.**
  Member contracts now classify receiver effects (pure, mutates-receiver,
  or unknown), and calls the registry proves pure — reads like `a.at(0)`
  and universal predicates — no longer discard the receiver's or its
  aliases' inferred facts. Mutators, unregistered members, blocks, impure
  arguments, dynamic dispatch, and user overrides stay conservative.
