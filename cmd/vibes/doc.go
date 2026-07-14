// Command vibes is the Vibescript CLI: it runs, checks, formats, analyzes,
// tests, and serves language tooling for .vibe scripts. Subcommands:
//
//	vibes run [flags] <script> [args...]
//	vibes run [flags] -e SNIPPET
//	vibes check [-module-path DIR] <script>
//	vibes fmt [-w] [-check] <path>...
//	vibes analyze <script>
//	vibes test [flags] [path...]
//	vibes lsp
//	vibes repl [quota flags]
//	vibes help [command]
//
// The run subcommand compiles a script. Without -function, it executes
// top-level statements when present and otherwise invokes the run function.
// -function selects another function, and positional arguments after the script
// path are passed to it as string values. -e evaluates an inline snippet,
// -watch re-runs a file when its sources change, and -check validates static
// contracts without executing. -module-path adds a directory to the module
// search path and may be repeated; the script's directory is always included.
// The run, test, and repl commands accept named quota profiles and individual
// quota overrides.
//
// The check subcommand reports statically provable contract issues across a
// script without executing it. The test subcommand discovers *_test.vibe files
// and runs their test_ functions.
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
// After a subcommand, flags accept one or two leading hyphens and stop at the
// first positional argument. Use -- after the subcommand to end flag parsing
// explicitly; the root command does not use it to escape command selection.
// Run vibes --help for the command list or vibes help <command> for
// command-specific help.
//
// Host capabilities (db, events, jobs, ctx) are not registered by the CLI;
// embed package vibes to run scripts with capabilities.
package main
