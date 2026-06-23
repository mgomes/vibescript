- **Fixed: reserved-word hash labels are accepted consistently.** Hash labels
  and keyword arguments now accept every keyword token that can precede a colon,
  including `begin:`, `rescue:`, `ensure:`, `raise:`, and `export:`. Previously
  these labels failed to parse while other reserved words such as `class:` and
  `return:` were already allowed, so keyword-shaped payload keys worked or failed
  depending on which word they happened to use. This matches Ruby's uniform
  treatment of keyword-shaped labels.
