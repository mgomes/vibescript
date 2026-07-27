- **Fixed: the embedding starter templates compile again.** All three scaffolds
  under `templates/embedding/` referenced value constructors on the `vibes`
  package rather than `vibes/value`, so none of them built. CI now compiles every
  template, which is what let them drift.
