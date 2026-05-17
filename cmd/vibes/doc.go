// Command vibes is the Vibescript CLI: it runs, formats, analyzes, and serves
// language tooling for .vibe scripts. Subcommands:
//
//	vibes run [-function NAME] [-check] [-module-path DIR] <script> [args...]
//	vibes fmt [-w] [-check] <path>...
//	vibes analyze <script>
//	vibes repl
//	vibes lsp
//	vibes help
//
// The run subcommand compiles a script and invokes a top-level function
// (default "run"), passing any remaining positional args as string values.
// -check compiles without executing. -module-path adds a directory to the
// module search path and may be repeated; the script's directory is always
// included.
//
// The fmt subcommand applies canonical formatting (CRLF and trailing
// whitespace stripping, single trailing newline) to .vibe files. It accepts
// individual files or directories, which are walked recursively. -w writes
// changes back in place; -check exits non-zero if any file needs formatting.
// With neither flag, formatted output is written to stdout.
//
// The analyze subcommand reports lint issues such as unreachable statements
// and exits non-zero when any are found.
//
// The repl subcommand starts an interactive Bubble Tea REPL with history,
// autocompletion, and meta commands (:help, :vars, :globals, :functions,
// :types, :clear, :reset, :last_error, :quit).
//
// The lsp subcommand speaks the Language Server Protocol over stdio,
// providing diagnostics, hover, and completion for .vibe documents.
//
// Host capabilities (db, events, jobs, ctx) are not registered by the CLI;
// embed package vibes to run scripts with capabilities.
package main
