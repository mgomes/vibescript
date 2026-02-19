# Tooling Commands

The `vibes` CLI provides a small set of stable tooling commands for local
development and CI.

## `vibes run <script>`

Compiles and executes a script file.

```bash
vibes run ./examples/strings/operations.vibe
```

Useful flags:

- `-function <name>`: invoke a specific function (default `run`).
- `-check`: compile only, without executing.
- `-module-path <dir>`: add module search paths for `require`.

## `vibes fmt <path>`

Applies canonical formatting for `.vibe` files.

```bash
vibes fmt ./examples
vibes fmt -w ./examples
vibes fmt -check .
```

Flags:

- `-w`: write formatted output back to files.
- `-check`: fail when any file would be reformatted.

## `vibes analyze <script>`

Runs script-level lint checks.

```bash
vibes analyze ./examples/strings/operations.vibe
```

Current checks include unreachable statements after terminating operations such
as `return` and `raise`.

## `vibes lsp`

Starts an LSP prototype over stdio, with hover, completion, and diagnostics.

```bash
vibes lsp
```

This command is meant to be launched by your editor's language-server client.
It currently tracks in-memory document updates from `didOpen`/`didChange`.

## `vibes repl`

Starts the interactive REPL for quick evaluation.

```bash
vibes repl
```

REPL command set:

- `:help`, `:vars`, `:globals`, `:functions`, `:types`
- `:last_error`, `:clear`, `:reset`, `:quit`
