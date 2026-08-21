# Language Server (`vibes lsp`)

`vibes lsp` starts a Language Server Protocol implementation that speaks LSP
over stdio. It is meant to be launched by an editor's language-server client
rather than run interactively.

```bash
vibes lsp
```

## Supported features

| LSP request | Support |
| --- | --- |
| `textDocument/publishDiagnostics` | Parse errors reported on open and on every change. |
| `textDocument/hover` | Documentation for builtins, namespace members, and keywords; other words are classified as symbols. |
| `textDocument/completion` | Context-aware: member methods after `.`, otherwise keywords, builtins, user-defined functions, and the enclosing function's parameters and locals. |
| `textDocument/formatting` | Full-document canonical formatting (the same formatter as `vibes fmt`). |
| `textDocument/signatureHelp` | Parameter hints on `(` and `,` for user-defined functions and global builtins. |
| `textDocument/definition` | Jump to top-level functions, classes, class methods, enums, and enum members in the current document. |
| `textDocument/documentSymbol` | Hierarchical outline: functions, classes with methods, enums with members. |

### Diagnostics

Every `didOpen` and `didChange` notification recompiles the document and
publishes parse errors with line/column positions. Errors that cannot be
mapped to a position are reported at the start of the document. A
`didClose` notification publishes an empty diagnostics set so editors
clear stale squiggles for the closed document.

### Hover

Hovering a builtin function shows its signature and description from the
embedded [builtins reference](builtins.md). A word directly following a
namespace receiver resolves to the qualified entry first, so hovering
`parse_as` in `JSON.parse_as(...)` shows the `JSON.parse_as` documentation;
`Math`, `Time`, `Duration`, `Regexp`, and `Hash` members resolve the
same way. Hovering `Proc` explains that callable constructors were removed.
Reserved keywords and contextual declaration words
(`module`, `alias`, `private`, ...) show a one-line description. Any other
identifier falls back to being classified as a plain symbol. UTF-16 positions
(the LSP wire encoding) are translated correctly for documents containing
multi-byte characters.

Completion items for builtins and keywords carry the same documentation, and
documented builtins use their signature as the completion detail.

### Completion

Completion is context-sensitive. After a `.` receiver it offers the union
of every builtin member method (type-unaware), labeled with the receiver
types that provide each method; a dot inside a numeric literal does not
trigger it. Elsewhere it offers keywords, builtins, the script's
user-defined functions, and the parameters and locals of the function
enclosing the cursor. Symbols come from the most recent successfully
compiled version of the document, so they keep working while the buffer
is mid-edit and temporarily unparsable. The script-local index of
functions, parameters, and locals is built on the first completion
request for a given document version and reused until the next edit, so
the diagnostics-only path that runs on every keystroke does not pay to
build it.

## Protocol details

- Transport is stdio with standard `Content-Length` framing.
- Document sync is full-text (`textDocumentSync: 1`): each change replaces the
  entire in-memory document.
- Inbound payloads are capped at 8 MiB; larger messages are rejected.
- Documents live only in memory. The server never reads or writes the
  filesystem, so unsaved editor buffers are analyzed as-is.
- `didClose` drops all server state for the document (text, compiled
  script, navigation, completion, diagnostics, and outline caches), so
  long editor sessions do not accumulate memory for closed files. A
  later `didOpen` re-establishes state from the client's text.

## Editor setup

### Zed

Install the official [Vibescript extension](https://zed.dev/extensions/vibescript),
which bundles syntax highlighting (via the
[tree-sitter grammar](https://github.com/mgomes/tree-sitter-vibescript)) and
launches `vibes lsp` automatically. The `vibes` binary must be on your `PATH`
(`just install` puts it there; see [tooling.md](tooling.md)).

### Other editors

Any editor with a generic LSP client can use the server. Configure the client
to run `vibes lsp` for files matching `*.vibe`. For example, in Neovim with
`nvim-lspconfig`-style manual configuration:

```lua
vim.lsp.start({
  name = "vibes",
  cmd = { "vibes", "lsp" },
  root_dir = vim.fn.getcwd(),
})
```

Syntax highlighting for editors that consume tree-sitter grammars is available
from [tree-sitter-vibescript](https://github.com/mgomes/tree-sitter-vibescript).

Completion after `.` narrows to the receiver's kind whenever that kind is
known from the source alone: a literal (`"x".`, `[1].`, `{a: 1}.`) or a
parameter carrying a declared type (`def f(s: string)`). Anything else — an
unannotated parameter, a local, a call result, a nullable or union type, or a
class-typed receiver — still offers the full union, so gradual typing is
unaffected.

## Limitations

Intentionally absent for now (tracked for future work):

- **Cross-file navigation** — definition and symbols are single-file;
  `require`d modules are not indexed yet.
- **Single-line signature help** — parameter hints resolve the call by
  scanning the cursor's line, so calls spanning multiple lines lose
  hints on continuation lines.
- **Diagnostic ranges at end of input** — diagnostics span the offending
  token when the parser knows it; errors reported at end of input (for
  example an unterminated block) degrade to single-character ranges.
- **Incremental sync** — the server requests full-document sync; very large
  files are re-parsed on every keystroke.
- **Multi-file awareness** — each document is analyzed in isolation;
  `require`d modules are not resolved for diagnostics or navigation.
