- **Testing: block-driver benchmarks now cover the shapes that went quadratic.**
  The suite gained coverage for pure versus accumulating block bodies,
  host-built versus script-built hash receivers, and walking versus
  result-building drivers, each wired into the CI smoke gate. Every one of these
  shapes regressed at some point without a benchmark noticing.
