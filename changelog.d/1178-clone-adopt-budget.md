- **Fixed: a class returned to the host no longer copies its property types
  once per parameter.** An unannotated ivar parameter (`def m(@x)`) carries the
  property contract its class declares once, and cloning a class for the host
  copied that whole type expression again for every parameter that named it. A
  38KB script with a 1000-field property type and 500 such methods therefore
  retained 80MB, allocated inside the host clone after the run had ended, where
  no quota could observe it. A clone now copies each contract once and gives
  that copy to every parameter naming the property, so the same script retains
  0.5MB and the copy is still the clone's own: editing a contract on a class
  from `Classes()` cannot reach the compiled script that later calls run.
- **Fixed: adopting a module's constants into an including class is now
  metered.** Every class with an included module runs class-body initialization
  whether or not it has a body, and the constant adoption that runs there was
  charged nothing at all. A source that spells one module of constants and many
  classes including it turned into `classes * constants` permanent class-constant
  entries: 300 classes including one 4000-constant module, from 139KB of source,
  allocated 263MB before the first check after initialization could see it, and
  10,000 adoptions completed under a 5,000-step quota. Each module resolution
  and each copied constant now costs a step, and the copy measures itself
  against the memory quota as it goes rather than once it is finished, so the
  work a mixin's constants cost is bounded by the quotas rather than by how many
  classes include it. A required module's environment is now reachable by the
  memory estimator while the module initializes, so the same bound applies to
  what a `require`d file builds instead of only to what the calling script does.
