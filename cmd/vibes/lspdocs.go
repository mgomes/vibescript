package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"

	vibescript "github.com/mgomes/vibescript"
	"github.com/mgomes/vibescript/internal/ast"
	"github.com/mgomes/vibescript/vibes"
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
// instance/value method (the word directly follows a "." on a value
// receiver), user-defined symbol in the current document, and finally
// the generic classifier line so hover never regresses for
// undocumented words.
//
// Builtin, keyword, and member documentation deliberately shadows a
// same-named user definition: the canonical docs describe what the
// word means in the language, while go-to-definition still resolves
// the user's own declaration. A user symbol is served only when every
// documentation table misses.
func hoverMarkdown(program *ast.Program, lines []string, line, character int, word string) string {
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
	if isValueMemberAccess(lines, line, character) {
		if md := memberDocMarkdown(word); md != "" {
			return md
		}
	}
	if md := userSymbolHover(program, lines, word); md != "" {
		return md
	}
	return fmt.Sprintf("`%s`\n\nVibescript %s", word, classifyWord(word))
}

// qualifiedWordAt returns "Receiver.word" when the word at the position
// directly follows a dotted receiver with no intervening space, or ""
// otherwise.
func qualifiedWordAt(lines []string, line, character int) string {
	runes, start, end, ok := wordSpanAtPosition(lines, line, character)
	if !ok || start == 0 {
		return ""
	}
	// Members reach their namespace through either accessor: Math.PI and
	// Math::PI both resolve to the parsed Math.PI entry.
	receiverEnd := start
	switch {
	case runes[start-1] == '.':
		receiverEnd = start - 1
	case start >= 2 && runes[start-1] == ':' && runes[start-2] == ':':
		receiverEnd = start - 2
	default:
		return ""
	}
	receiverStart := receiverEnd
	for receiverStart > 0 && isWordRune(runes[receiverStart-1]) {
		receiverStart--
	}
	if receiverStart == receiverEnd {
		return ""
	}
	return string(runes[receiverStart:receiverEnd]) + "." + string(runes[start:end])
}

// memberDocUniversal is the pseudo-receiver for the Object-level
// helpers (itself, tap, eql?, ...) that resolve on every value; they
// are documented once rather than per receiver type.
const memberDocUniversal = "universal"

// memberDocEntry is one hover/completion documentation entry for a
// builtin value member method.
type memberDocEntry struct {
	// Receiver is the runtime receiver type providing the member
	// ("array", "string", ...).
	Receiver string
	// Signature is the code-formatted usage line from the docs.
	Signature string
	// Markdown is the hover snippet: signature plus description.
	Markdown string
}

// memberDocIndex is the member documentation table: typed entries per
// member name (sorted by receiver for stable merged hovers) plus the
// universal Object-helper entries served for every receiver.
type memberDocIndex struct {
	entries   map[string][]memberDocEntry
	universal map[string]memberDocEntry
}

var (
	memberDocsOnce  sync.Once
	memberDocsTable *memberDocIndex
)

// stdlibMemberSections maps docs/stdlib_core_utilities.md "## " section
// titles to the runtime receiver type their bullets document. Sections
// absent from the map (How to Read Signatures, Builtin Functions, ...)
// contribute only receiver-qualified spans such as `regex.source`.
// Section coverage is protected by per-receiver canaries in the member
// documentation drift test.
var stdlibMemberSections = map[string]string{
	"Strings":   "string",
	"Arrays":    "array",
	"Hashes":    "hash",
	"Integers":  "int",
	"Floats":    "float",
	"Money":     "money",
	"Durations": "duration",
	"Times":     "time",
	"Symbols":   "symbol",
	"Ranges":    "range",

	"Universal Members":    memberDocUniversal,
	"Universal Predicates": memberDocUniversal,
	"Debug Representation": memberDocUniversal,
	"Universal Methods":    memberDocUniversal,
	"Object Helpers":       memberDocUniversal,
	"Object Introspection": memberDocUniversal,
}

// memberDocs returns the member documentation index, parsed once from
// the embedded stdlib reference and the narrative per-type guides. The
// compact stdlib reference is parsed first so its hover-sized entries
// win over the longer narrative bullets documenting the same member.
func memberDocs() *memberDocIndex {
	memberDocsOnce.Do(func() {
		index := &memberDocIndex{
			entries:   make(map[string][]memberDocEntry),
			universal: make(map[string]memberDocEntry),
		}
		index.parse(vibescript.StdlibDoc, "", stdlibMemberSections)
		index.parse(vibescript.StringsDoc, "string", nil)
		index.parse(vibescript.ArraysDoc, "array", nil)
		index.parse(vibescript.HashesDoc, "hash", nil)
		index.parse(vibescript.TimeDoc, "time", nil)
		index.parse(vibescript.DurationsDoc, "duration", nil)
		for _, entries := range index.entries {
			slices.SortFunc(entries, func(a, b memberDocEntry) int {
				return strings.Compare(a.Receiver, b.Receiver)
			})
		}
		memberDocsTable = index
	})
	return memberDocsTable
}

// memberDocReceivers is the set of runtime receiver types, taken from
// the runtime's own member-completion table so the parser can never
// invent a receiver the interpreter does not dispatch on.
var memberDocReceivers = func() map[string]struct{} {
	receivers := make(map[string]struct{})
	for receiver := range vibes.MemberCompletionNames() {
		receivers[receiver] = struct{}{}
	}
	return receivers
}()

// parse walks one documentation file. With a fixed receiver the whole
// file documents that type (the narrative guides); with a section map
// the receiver follows the current "## " heading (the stdlib
// reference). Two tolerant shapes register entries: "### `name(sig)`"
// headings followed by description paragraphs, and "- `name(sig)` –
// description" bullets. Entry names come only from the leading run of
// code spans, so inline code later in a bullet never registers a
// phantom member; the first entry per (receiver, name) wins.
func (idx *memberDocIndex) parse(markdown, fixedReceiver string, sections map[string]string) {
	lines := strings.Split(markdown, "\n")
	receiver := fixedReceiver

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
		if sections != nil {
			if title, ok := strings.CutPrefix(lines[i], "## "); ok {
				receiver = sections[strings.TrimSpace(title)]
				continue
			}
		}
		if heading, ok := strings.CutPrefix(lines[i], "### "); ok {
			refs := leadingMemberNames(heading, receiver)
			if len(refs) > 0 {
				idx.add(refs, strings.TrimSpace(heading), docDescription(lines, i+1))
			}
			continue
		}
		if strings.HasPrefix(lines[i], "- `") {
			bullet, next := joinedBulletLine(lines, i)
			i = next
			signature, description, ok := splitDocBullet(bullet)
			if !ok {
				signature, description = strings.TrimPrefix(bullet, "- "), ""
			}
			idx.add(leadingMemberNames(strings.TrimPrefix(bullet, "- "), receiver), signature, description)
		}
	}
}

func (idx *memberDocIndex) add(refs []memberNameRef, signature, description string) {
	markdown := signature
	if description != "" {
		markdown += "\n\n" + description
	}
	for _, ref := range refs {
		if ref.receiver == memberDocUniversal {
			if _, exists := idx.universal[ref.name]; !exists {
				idx.universal[ref.name] = memberDocEntry{Receiver: memberDocUniversal, Signature: signature, Markdown: markdown}
			}
			continue
		}
		if slices.ContainsFunc(idx.entries[ref.name], func(entry memberDocEntry) bool {
			return entry.Receiver == ref.receiver
		}) {
			continue
		}
		idx.entries[ref.name] = append(idx.entries[ref.name], memberDocEntry{Receiver: ref.receiver, Signature: signature, Markdown: markdown})
	}
}

// memberNameRef is one receiver-qualified member name parsed from a
// heading or bullet signature.
type memberNameRef struct {
	receiver string
	name     string
}

var memberNameRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*[?!]?$`)

// leadingMemberNames extracts member names from the leading run of
// code spans in text — spans separated only by "/", ",", "and", or
// "or", the alias-list shapes the docs use ("`size` / `length`",
// "`sunday?`, `monday?`"). Scanning stops at the first prose word or
// unparseable span, so code mentioned later in a sentence never
// registers an entry.
func leadingMemberNames(text, receiver string) []memberNameRef {
	rest := strings.TrimSpace(text)
	var refs []memberNameRef
	for {
		rest = trimMemberSeparators(rest)
		if !strings.HasPrefix(rest, "`") {
			return refs
		}
		end := strings.Index(rest[1:], "`")
		if end < 0 {
			return refs
		}
		span := rest[1 : 1+end]
		rest = rest[2+end:]
		ref, ok := memberRefFromSpan(span, receiver)
		if !ok {
			return refs
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
}

// trimMemberSeparators removes the separators an alias list may use
// between its code spans: punctuation, the words "and"/"or" when a
// span follows, and parenthesized asides ("`sort!` (optional
// comparator block), `reverse!`").
func trimMemberSeparators(text string) string {
	for {
		trimmed := strings.TrimLeft(text, " \t/,")
		for _, word := range []string{"and ", "or "} {
			if rest, ok := strings.CutPrefix(trimmed, word); ok && strings.HasPrefix(strings.TrimLeft(rest, " "), "`") {
				trimmed = rest
			}
		}
		if strings.HasPrefix(trimmed, "(") {
			if rest, ok := cutBalancedParens(trimmed); ok {
				trimmed = rest
			}
		}
		if trimmed == text {
			return trimmed
		}
		text = trimmed
	}
}

// cutBalancedParens removes a leading balanced parenthesized group and
// returns the remainder.
func cutBalancedParens(text string) (string, bool) {
	depth := 0
	for i, r := range text {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[i+1:], true
			}
		}
	}
	return text, false
}

// memberRefFromSpan resolves one code span to a member reference. The
// span must be a bare member name optionally followed by an argument
// list, block shape, or "-> type" tail ("size", "fetch(key, default)",
// "pop -> value | nil"); anything else — operators, bracket forms,
// prose — is rejected. A "type.name" span names its own receiver, which
// must be a runtime receiver type, so `regex.source` documents regex
// while `Regexp.new` (a namespace builtin) is skipped.
func memberRefFromSpan(span, receiver string) (memberNameRef, bool) {
	name, rest := span, ""
	if cut := strings.IndexAny(span, " ({"); cut >= 0 {
		name, rest = span[:cut], span[cut:]
	}
	rest = strings.TrimLeft(rest, " ")
	if rest != "" && !strings.HasPrefix(rest, "(") && !strings.HasPrefix(rest, "{") && !strings.HasPrefix(rest, "->") {
		return memberNameRef{}, false
	}
	if prefix, member, ok := strings.Cut(name, "."); ok {
		if _, known := memberDocReceivers[prefix]; !known || !memberNameRegex.MatchString(member) {
			return memberNameRef{}, false
		}
		return memberNameRef{receiver: prefix, name: member}, true
	}
	if receiver == "" || !memberNameRegex.MatchString(name) {
		return memberNameRef{}, false
	}
	return memberNameRef{receiver: receiver, name: name}, true
}

// memberDocMarkdown returns the hover markdown for a value member
// name: the universal Object-helper entry when one exists (it applies
// to every receiver), the single typed entry when exactly one receiver
// documents the name, or a merged view with one "`receiver.name`"
// section per receiver — in stable receiver order — separated by
// horizontal rules. An empty string means the name is undocumented.
func memberDocMarkdown(word string) string {
	docs := memberDocs()
	if entry, ok := docs.universal[word]; ok {
		return entry.Markdown
	}
	entries := docs.entries[word]
	switch len(entries) {
	case 0:
		return ""
	case 1:
		return entries[0].Markdown
	}
	var b strings.Builder
	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		fmt.Fprintf(&b, "`%s.%s`\n\n%s", entry.Receiver, word, entry.Markdown)
	}
	return b.String()
}

// unambiguousMemberDocMarkdown returns member documentation for
// completion items: only names with a single interpretation (a
// universal helper, or exactly one documenting receiver) carry docs,
// since a completion list has no receiver context to disambiguate.
func unambiguousMemberDocMarkdown(name string) string {
	docs := memberDocs()
	if entry, ok := docs.universal[name]; ok {
		return entry.Markdown
	}
	if entries := docs.entries[name]; len(entries) == 1 {
		return entries[0].Markdown
	}
	return ""
}

// lspDocNamespaces is the set of namespace builtins (JSON, Math, ...)
// whose members resolve through the builtins.md table rather than the
// value member table.
var lspDocNamespaces = func() map[string]struct{} {
	set := make(map[string]struct{})
	for _, name := range lspBuiltins {
		if name != "" && name[0] >= 'A' && name[0] <= 'Z' {
			set[name] = struct{}{}
		}
	}
	return set
}()

// isValueMemberAccess reports whether the word at the position is
// reached through a "." member access on a value receiver: directly
// preceded by a single dot (".." is a range, "::" a scope accessor)
// whose receiver is not a documented namespace like JSON or Math. A
// dot with no receiver word (a chained call continuing the previous
// line) still counts as member access.
func isValueMemberAccess(lines []string, line, character int) bool {
	runes, start, _, ok := wordSpanAtPosition(lines, line, character)
	if !ok || start == 0 || runes[start-1] != '.' {
		return false
	}
	if start >= 2 && runes[start-2] == '.' {
		return false
	}
	receiverEnd := start - 1
	receiverStart := receiverEnd
	for receiverStart > 0 && isWordRune(runes[receiverStart-1]) {
		receiverStart--
	}
	receiver := string(runes[receiverStart:receiverEnd])
	_, namespace := lspDocNamespaces[receiver]
	return !namespace
}

// userSymbolHover resolves hover documentation for a symbol declared
// in the current document: top-level functions, classes and modules
// with their methods, and enums with their members. The setter form
// "name=" is tried when the bare word has no declaration, mirroring
// go-to-definition.
func userSymbolHover(program *ast.Program, lines []string, word string) string {
	if program == nil || word == "" {
		return ""
	}
	for _, candidate := range []string{word, word + "="} {
		if md := userSymbolDoc(program, lines, candidate); md != "" {
			return md
		}
	}
	return ""
}

func userSymbolDoc(program *ast.Program, lines []string, word string) string {
	for _, stmt := range program.Statements {
		switch st := stmt.(type) {
		case *ast.FunctionStmt:
			if st.Name == word {
				return userSymbolMarkdown(lines, userDefSignature(st, false), st.Position, st.Name)
			}
		case *ast.ClassStmt:
			if md := classSymbolDoc(st, lines, word); md != "" {
				return md
			}
		case *ast.EnumStmt:
			if st.Name == word {
				return userSymbolMarkdown(lines, "enum "+st.Name, st.Position, st.Name)
			}
			for _, member := range st.Members {
				if member.Name == word {
					return userSymbolMarkdown(lines, st.Name+"::"+member.Name, member.Position, member.Name)
				}
			}
		}
	}
	return ""
}

// classSymbolDoc resolves word within one class or module declaration:
// the declaration itself, its instance and self. methods, and nested
// modules, recursively.
func classSymbolDoc(st *ast.ClassStmt, lines []string, word string) string {
	if st.Name == word {
		keyword := "class"
		if st.IsModule {
			keyword = "module"
		}
		return userSymbolMarkdown(lines, keyword+" "+st.Name, st.Position, st.Name)
	}
	for _, method := range st.Methods {
		if method.Name == word {
			return userSymbolMarkdown(lines, userDefSignature(method, false), method.Position, method.Name)
		}
	}
	for _, method := range st.ClassMethods {
		if method.Name == word {
			return userSymbolMarkdown(lines, userDefSignature(method, true), method.Position, method.Name)
		}
	}
	for _, nested := range st.Modules {
		if md := classSymbolDoc(nested, lines, word); md != "" {
			return md
		}
	}
	return ""
}

// userDefSignature reconstructs a declaration line from the AST: def,
// name (with a self. receiver for class methods), parameters with
// their type annotations and default markers, and the return type.
func userDefSignature(fn *ast.FunctionStmt, classMethod bool) string {
	name := fn.Name
	if classMethod {
		name = "self." + name
	}
	signature := "def " + name
	if len(fn.Params) > 0 {
		labels := make([]string, len(fn.Params))
		for i, param := range fn.Params {
			labels[i] = paramLabel(param)
		}
		signature += "(" + strings.Join(labels, ", ") + ")"
	}
	if fn.ReturnTy != nil {
		signature += " -> " + ast.FormatTypeExpr(fn.ReturnTy)
	}
	return signature
}

// userSymbolMarkdown renders a user-symbol hover: the reconstructed
// signature in a fenced code block, plus — when the lines immediately
// above the declaration in the live buffer form a contiguous "#"
// comment block — the stripped comment text as prose.
func userSymbolMarkdown(lines []string, signature string, pos ast.Position, anchorName string) string {
	markdown := "```vibe\n" + signature + "\n```"
	if line := anchorDeclLine(lines, pos, anchorName); line >= 0 {
		if comment := docCommentAbove(lines, line); comment != "" {
			markdown += "\n\n" + comment
		}
	}
	return markdown
}

// docCommentAbove collects the contiguous "#" comment block directly
// above declLine, stripped of its markers. Directive lines ("# vibe:"
// pragmas and "# uses:" capability declarations) are machine-facing
// and excluded from the prose.
func docCommentAbove(lines []string, declLine int) string {
	var block []string
	for i := declLine - 1; i >= 0; i-- {
		rest, ok := strings.CutPrefix(strings.TrimSpace(lineAt(lines, i)), "#")
		if !ok {
			break
		}
		block = append(block, strings.TrimPrefix(rest, " "))
	}
	slices.Reverse(block)
	parts := make([]string, 0, len(block))
	for _, text := range block {
		if strings.HasPrefix(text, "vibe:") || strings.HasPrefix(text, "uses:") {
			continue
		}
		parts = append(parts, text)
	}
	for len(parts) > 0 && strings.TrimSpace(parts[0]) == "" {
		parts = parts[1:]
	}
	for len(parts) > 0 && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	return strings.Join(parts, "\n")
}
