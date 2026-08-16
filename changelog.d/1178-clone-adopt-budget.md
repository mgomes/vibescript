- **Fixed: a class returned to the host no longer copies its property types
  once per parameter.** An unannotated ivar parameter (`def m(@x)`) carries the
  property contract its class declares once, and cloning a class for the host
  copied that whole type expression again for every parameter that named it. A
  38KB script with a 1000-field property type and 500 such methods therefore
  retained 80MB, allocated inside the host clone after the run had ended, where
  no quota could observe it. A clone now copies each type expression once for
  the whole operation and gives that copy to every parameter and class reaching
  it, so that script retains 0.5MB. The copy is still the clone's own: editing
  a contract on a class from `Classes()` cannot reach the compiled script that
  later calls run.
- **Fixed: what a required file builds during initialization is inside the
  calling execution's memory quota.** A required module's environment is only a
  Go local until require publishes its exports, so the classes and constants it
  was building hung off no root the memory estimator walked, and every check
  running inside its initialization measured a graph that did not contain them.
  That environment is now reachable by the estimator while the module
  initializes.
