- **Fixed: the array-comparison tests no longer fail under the race detector.**
  Four of them enforced wall-clock ceilings to catch an exponential regression,
  and the race detector's slowdown blew those ceilings on work that had not
  changed. An exponential regression does not finish at all, which go test's
  own package timeout already reports.
