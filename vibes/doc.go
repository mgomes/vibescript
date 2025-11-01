// Package vibes implements the VibeScript execution engine. The initial
// version supports a Ruby-flavoured syntax with the following constructs:
//   - Function definitions via `def name(args...) ... end` with implicit return.
//   - Literals for ints, floats, strings, bools, nil, arrays, hashes, and symbols.
//   - Arithmetic and comparison expressions (+, -, *, /, >, <, ==, !=).
//   - Logical operators (and/or/not) and parentheses for grouping.
//   - Indexing via `object[expr]` and property access via `object.attr`.
//   - Function and method calls with positional and keyword arguments.
//   - Built-ins such as `assert`, `money`, and `money_cents`; capabilities are
//     provided by the host and accessed as globals (ctx, db, jobs, etc.).
//
// Comments beginning with `#` are ignored. The interpreter enforces a simple
// step quota, rejecting scripts that exceed configured execution limits.
package vibes
