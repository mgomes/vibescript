package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	vibescript "github.com/mgomes/vibescript"
)

// builtinDocEntry is one hover/completion documentation entry parsed
// from docs/builtins.md.
type builtinDocEntry struct {
	// Signature is the code-formatted usage line, e.g.
	// "`assert(condition, message)`".
	Signature string
	// Markdown is the full hover snippet: the signature line followed
	// by the entry's description paragraphs.
	Markdown string
}

var (
	builtinDocsOnce  sync.Once
	builtinDocTable  map[string]builtinDocEntry
	docCodeSpanRegex = regexp.MustCompile("`([^`]+)`")
	docNameRegex     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*[?!]?(\.[A-Za-z_][A-Za-z0-9_]*[?!]?)*$`)
)

// builtinDocs returns the builtin documentation table keyed by name —
// bare kernel builtins ("puts") and qualified namespace members
// ("JSON.parse_as") — parsed once from the embedded docs/builtins.md.
func builtinDocs() map[string]builtinDocEntry {
	builtinDocsOnce.Do(func() {
		builtinDocTable = parseBuiltinDocs(vibescript.BuiltinsDoc)
	})
	return builtinDocTable
}

// parseBuiltinDocs builds the name -> documentation table from the
// builtins reference markdown. It recognizes two tolerant shapes:
//
//   - "### `name(sig)`" headings, whose entry names come from every
//     code span in the heading (so "### `format(...)` / `sprintf(...)`"
//     documents both) and whose description is the following text
//     paragraphs up to the next heading, skipping fenced code blocks
//     and bullet lists.
//   - "- `Namespace.member(sig)` – description" bullets, which document
//     qualified namespace members (`Math.sqrt`, `Time.now`); `::`
//     constant accessors normalize to dotted names (`Math.PI`).
//
// The first entry for a name wins, so a duplicated mention never
// overwrites the primary documentation.
func parseBuiltinDocs(markdown string) map[string]builtinDocEntry {
	entries := make(map[string]builtinDocEntry)
	lines := strings.Split(markdown, "\n")

	inFence := false
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if heading, ok := strings.CutPrefix(lines[i], "### "); ok {
			names := docEntryNames(heading, false)
			if len(names) == 0 {
				continue
			}
			addDocEntries(entries, names, strings.TrimSpace(heading), docDescription(lines, i+1))
			continue
		}
		if strings.HasPrefix(lines[i], "- `") {
			bullet, next := joinedBulletLine(lines, i)
			i = next
			signature, description, ok := splitDocBullet(bullet)
			if !ok {
				continue
			}
			names := docEntryNames(signature, true)
			if len(names) == 0 {
				continue
			}
			addDocEntries(entries, names, signature, description)
		}
	}
	return entries
}

// docDescription collects the text paragraphs following a doc heading
// up to the next heading. Fenced code blocks and bullet lists are
// skipped so the snippet stays hover-sized rather than reproducing the
// whole section.
func docDescription(lines []string, start int) string {
	var paragraphs []string
	var current []string
	flush := func() {
		if len(current) > 0 {
			paragraphs = append(paragraphs, strings.Join(current, "\n"))
			current = nil
		}
	}

	inFence := false
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			flush()
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(lines[i], "#") {
			break
		}
		if trimmed == "" {
			flush()
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			_, i = joinedBulletLine(lines, i)
			continue
		}
		current = append(current, trimmed)
	}
	flush()
	return strings.Join(paragraphs, "\n\n")
}

// joinedBulletLine joins the bullet at index i with its indented
// continuation lines and returns the joined text along with the index
// of the bullet's last line.
func joinedBulletLine(lines []string, i int) (string, int) {
	parts := []string{strings.TrimSpace(lines[i])}
	for i+1 < len(lines) {
		next := lines[i+1]
		trimmed := strings.TrimSpace(next)
		if trimmed == "" || !strings.HasPrefix(next, "  ") || strings.HasPrefix(trimmed, "- ") {
			break
		}
		parts = append(parts, trimmed)
		i++
	}
	return strings.Join(parts, " "), i
}

// splitDocBullet splits a "- `name(sig)` – description" bullet into its
// signature and description around the en- or em-dash separator.
func splitDocBullet(bullet string) (signature, description string, ok bool) {
	body := strings.TrimPrefix(bullet, "- ")
	for _, separator := range []string{" – ", " — "} {
		if idx := strings.Index(body, separator); idx > 0 {
			return strings.TrimSpace(body[:idx]), strings.TrimSpace(body[idx+len(separator):]), true
		}
	}
	return "", "", false
}

// docEntryNames extracts entry names from the code spans of a heading
// or bullet signature: the span text up to its argument list or block,
// with `::` constant accessors normalized to dotted names. Bullets set
// requireQualified so incidental inline code in prose never registers a
// bare-name entry.
func docEntryNames(text string, requireQualified bool) []string {
	var names []string
	seen := make(map[string]struct{})
	for _, span := range docCodeSpanRegex.FindAllStringSubmatch(text, -1) {
		name := span[1]
		if cut := strings.IndexAny(name, " ({"); cut >= 0 {
			name = name[:cut]
		}
		name = strings.ReplaceAll(name, "::", ".")
		if !docNameRegex.MatchString(name) {
			continue
		}
		if requireQualified && !strings.Contains(name, ".") {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func addDocEntries(entries map[string]builtinDocEntry, names []string, signature, description string) {
	markdown := signature
	if description != "" {
		markdown += "\n\n" + description
	}
	for _, name := range names {
		if _, exists := entries[name]; exists {
			continue
		}
		entries[name] = builtinDocEntry{Signature: signature, Markdown: markdown}
	}
}

// lspContextualWords are the declaration words that are not reserved
// keywords (they parse as identifiers outside their declaring position)
// but still deserve keyword-style hover documentation.
var lspContextualWords = []string{
	"alias",
	"alias_method",
	"extend",
	"include",
	"module",
	"protected",
	"public",
}

// keywordDocs maps every reserved keyword (ast.Keywords()) and every
// contextual declaration word (lspContextualWords) to a one-line
// description sourced from docs/language_reference.md and
// docs/classes.md. Coverage of the parser's keyword set is enforced by
// tests.
var keywordDocs = map[string]string{
	"and":      "Reserved word; not a boolean operator in Vibescript — use `&&`. Usable as a method name or hash label.",
	"begin":    "Opens a block whose failures are handled by `rescue` clauses, with optional `else` and `ensure`.",
	"break":    "Exits the nearest loop; `break value` becomes the loop's value.",
	"case":     "Opens a multi-branch match whose `when` clauses compare with case equality (`===`).",
	"class":    "Declares a class grouping behavior and methods; instances are created with `Klass.new`.",
	"def":      "Declares a function or method; the body runs until the matching `end`.",
	"do":       "Opens a block (`do |args| ... end`) passed to a call.",
	"else":     "Fallback branch of an `if`, `unless`, `case`, or `begin`/`rescue`.",
	"elsif":    "Adds another condition branch to an `if`.",
	"end":      "Closes a `def`, `class`, `if`, `do`, or other block opener.",
	"ensure":   "Runs cleanup code whether the protected body raised or not.",
	"enum":     "Declares a nominal state set; members are accessed with `::` (`Status::Draft`).",
	"export":   "Marks a top-level function as exported from a module file.",
	"false":    "Boolean false literal.",
	"for":      "Iterates over a collection: `for item in items ... end`.",
	"getter":   "Declares a read-only accessor backed by the instance variable of the same name.",
	"if":       "Runs its body when the condition is truthy; also usable as a modifier and as an expression.",
	"in":       "Separates the loop variable from the collection in `for ... in`.",
	"next":     "Skips to the next iteration of the nearest loop.",
	"nil":      "The absence-of-value literal.",
	"not":      "Reserved word; not a boolean operator in Vibescript — use `!`.",
	"or":       "Reserved word; not a boolean operator in Vibescript — use `||`.",
	"private":  "Marks subsequent (or one prefixed) method declarations as internal to the class or module.",
	"property": "Declares a read-write accessor (`x` and `x=`) backed by an instance variable.",
	"raise":    "Raises an error, unwinding to the nearest matching `rescue`.",
	"rescue":   "Handles an error raised in the preceding `begin` or method body; also an expression modifier.",
	"retry":    "Re-runs the `begin` body from inside a `rescue` handler.",
	"return":   "Exits the enclosing function with an optional value.",
	"self":     "The current instance; `def self.name` declares a class method.",
	"setter":   "Declares a write-only accessor (`x=`) backed by an instance variable.",
	"then":     "Optional separator between a condition and its single-line body.",
	"true":     "Boolean true literal.",
	"unless":   "Runs its body when the condition is falsy; also usable as a statement modifier.",
	"until":    "Loops until its condition becomes truthy; also usable as a statement modifier.",
	"when":     "One branch of a `case`, matched with case equality (`===`).",
	"while":    "Loops while its condition stays truthy; also usable as a statement modifier.",
	"yield":    "Invokes the block passed to the current method.",

	"alias":        "Declares an alternate name for a method: `alias new_name old_name`.",
	"alias_method": "Declares an alternate method name: `alias_method :new_name, :old_name`.",
	"extend":       "Mixes a module's methods into the class's own (`self.`) methods.",
	"include":      "Mixes a module's instance-style methods into the class's instance methods.",
	"module":       "Declares a namespace of module functions and constants; contextual — it only opens a declaration before a constant name.",
	"protected":    "Marks subsequent methods callable only within the class family.",
	"public":       "Restores public visibility for subsequent method declarations.",
}

// hoverMarkdown resolves hover documentation for word at the given
// position. Lookup order: qualified builtin (the word directly follows
// a "Namespace." receiver), bare builtin, keyword or contextual word,
// and finally the generic classifier line so hover never regresses for
// undocumented words.
func hoverMarkdown(lines []string, line, character int, word string) string {
	docs := builtinDocs()
	if qualified := qualifiedWordAt(lines, line, character); qualified != "" {
		if entry, ok := docs[qualified]; ok {
			return entry.Markdown
		}
	}
	if entry, ok := docs[word]; ok {
		return entry.Markdown
	}
	if doc, ok := keywordDocs[word]; ok {
		return fmt.Sprintf("`%s`\n\n%s", word, doc)
	}
	return fmt.Sprintf("`%s`\n\nVibescript %s", word, classifyWord(word))
}

// qualifiedWordAt returns "Receiver.word" when the word at the position
// directly follows a dotted receiver with no intervening space, or ""
// otherwise.
func qualifiedWordAt(lines []string, line, character int) string {
	runes, start, end, ok := wordSpanAtPosition(lines, line, character)
	if !ok || start == 0 || runes[start-1] != '.' {
		return ""
	}
	receiverEnd := start - 1
	receiverStart := receiverEnd
	for receiverStart > 0 && isWordRune(runes[receiverStart-1]) {
		receiverStart--
	}
	if receiverStart == receiverEnd {
		return ""
	}
	return string(runes[receiverStart:receiverEnd]) + "." + string(runes[start:end])
}
