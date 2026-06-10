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
| `textDocument/hover` | Classifies the word under the cursor as a keyword, builtin, or symbol. |
| `textDocument/completion` | Context-aware: member methods after `.`, otherwise keywords, builtins, user-defined functions, and the enclosing function's parameters and locals. |
| `textDocument/formatting` | Full-document canonical formatting (the same formatter as `vibes fmt`). |

### Diagnostics

Every `didOpen` and `didChange` notification recompiles the document and
publishes parse errors with line/column positions. Errors that cannot be
mapped to a position are reported at the start of the document.

### Hover

Hovering an identifier reports whether it is a Vibescript keyword, a builtin
function, or a plain symbol. UTF-16 positions (the LSP wire encoding) are
translated correctly for documents containing multi-byte characters.

### Completion

Completion is context-sensitive. After a `.` receiver it offers the union
of every builtin member method (type-unaware), labeled with the receiver
types that provide each method; a dot inside a numeric literal does not
trigger it. Elsewhere it offers keywords, builtins, the script's
user-defined functions, and the parameters and locals of the function
enclosing the cursor. Symbols come from the most recent successfully
compiled version of the document, so they keep working while the buffer
is mid-edit and temporarily unparsable.

## Protocol details

- Transport is stdio with standard `Content-Length` framing.
- Document sync is full-text (`textDocumentSync: 1`): each change replaces the
  entire in-memory document.
- Inbound payloads are capped at 8 MiB; larger messages are rejected.
- Documents live only in memory. The server never reads or writes the
  filesystem, so unsaved editor buffers are analyzed as-is.

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

## Limitations

Intentionally absent for now (tracked for future work):

- **Type-aware member completion** — completion after `.` offers the
  union of all builtin member methods rather than narrowing by the
  receiver's type.
- **Go-to-definition and document symbols** — no symbol index exists yet, so
  navigation requests are not supported.
- **Signature help** — no parameter hints on `(` or `,`.
- **Diagnostic ranges at end of input** — diagnostics span the offending
  token when the parser knows it; errors reported at end of input (for
  example an unterminated block) degrade to single-character ranges.
- **Incremental sync** — the server requests full-document sync; very large
  files are re-parsed on every keystroke.
- **Multi-file awareness** — each document is analyzed in isolation;
  `require`d modules are not resolved for diagnostics or navigation.
