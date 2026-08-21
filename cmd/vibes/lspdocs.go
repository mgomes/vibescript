package main

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

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
			refs := docEntryRefs(heading, false)
			if len(refs) == 0 {
				continue
			}
			addDocEntries(entries, refs, strings.TrimSpace(heading), docDescription(lines, i+1))
			continue
		}
		if strings.HasPrefix(lines[i], "- `") {
			bullet, next := joinedBulletLine(lines, i)
			i = next
			signature, description, ok := splitDocBullet(bullet)
			if !ok {
				continue
			}
			refs := docEntryRefs(signature, true)
			if len(refs) == 0 {
				continue
			}
			addDocEntries(entries, refs, signature, description)
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

type builtinDocRef struct {
	name      string
	signature string
}

// docEntryRefs extracts entry names and their individual code-formatted
// signatures from a heading or bullet. `::` constant accessors normalize to
// dotted names. Bullets require qualified names so incidental inline code in
// prose never registers a bare-name entry.
func docEntryRefs(text string, requireQualified bool) []builtinDocRef {
	var refs []builtinDocRef
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
		refs = append(refs, builtinDocRef{name: name, signature: span[0]})
	}
	return refs
}

func addDocEntries(entries map[string]builtinDocEntry, refs []builtinDocRef, signature, description string) {
	markdown := signature
	if description != "" {
		markdown += "\n\n" + description
	}
	for _, ref := range refs {
		if _, exists := entries[ref.name]; exists {
			continue
		}
		entries[ref.name] = builtinDocEntry{Signature: ref.signature, Markdown: markdown}
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
	"extend":       "Removed mixin directive; modules are namespaces. Call `SomeModule.fn(receiver)` instead of `extend SomeModule`.",
	"include":      "Removed mixin directive; modules are namespaces. Call `SomeModule.fn(receiver)` instead of `include SomeModule`.",
	"module":       "Declares a namespace of `def self.` functions, constants, and nested modules; contextual — it only opens a declaration before a constant name.",
	"protected":    "Marks subsequent methods callable only within the class family.",
	"public":       "Restores public visibility for subsequent method declarations.",
}

// hoverMarkdown resolves hover documentation for word at the given
// position. Lookup order: qualified builtin (the word directly follows
// a "Namespace." receiver); then, for a word reached through a "." on
// a value receiver, the instance/value member table — a dotted call
// like money.format is never the global format builtin; otherwise bare
// builtin, namespace, and keyword docs; then a user-defined symbol in
// the current document; and finally the generic classifier line so
// hover never regresses for undocumented words.
//
// Builtin, keyword, and member documentation deliberately shadows a
// same-named user definition: the canonical docs describe what the
// word means in the language, while go-to-definition still resolves
// the user's own declaration. A user symbol is served only when every
// documentation table misses.
func hoverMarkdown(catalog builtinCatalog, program *ast.Program, lines []string, line, character int, word string) string {
	docs := builtinDocs()
	if qualified := qualifiedWordAt(lines, line, character); qualified != "" {
		if entry, ok := docs[qualified]; ok {
			return entry.Markdown
		}
	}
	if isValueMemberAccess(catalog, lines, line, character) {
		if md := memberDocMarkdown(word); md != "" {
			return md
		}
	} else {
		if entry, ok := docs[word]; ok {
			return entry.Markdown
		}
		if md := namespaceDocMarkdown(catalog, word); md != "" {
			return md
		}
		if doc, ok := keywordDocs[word]; ok {
			if word != "include" && word != "extend" {
				return fmt.Sprintf("`%s`\n\n%s", word, doc)
			}
			if mixinDirectiveContext(program, lines, line, character, word) {
				return fmt.Sprintf("`%s`\n\n%s", word, doc)
			}
		}
	}
	if md := userSymbolHover(program, lines, word, line, character); md != "" {
		return md
	}
	return fmt.Sprintf("`%s`\n\nVibescript %s", word, classifyWord(catalog, word))
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
	var receiverEnd int
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
	// The receiver must be standalone: in payload.JSON.parse the segment
	// before parse is a member access on payload, not the JSON namespace,
	// so a chained receiver never qualifies.
	if receiverStart > 0 && (runes[receiverStart-1] == '.' || runes[receiverStart-1] == ':') {
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
		index.demotePartialUniversals()
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
				// Without a dash separator the bullet may still be a
				// prose entry ("- `take(n)` to keep a prefix"), but a
				// bullet that is nothing beyond its code-span run is an
				// enumeration line ("- `strip!`, `lstrip!`, ...") whose
				// names resolve through the bang-variant fallback.
				body := strings.TrimPrefix(bullet, "- ")
				refs, rest := leadingMemberNamesRest(body, receiver)
				if strings.TrimSpace(rest) == "" {
					continue
				}
				idx.add(refs, body, "")
				continue
			}
			idx.add(leadingMemberNames(strings.TrimPrefix(bullet, "- "), receiver), signature, description)
		}
	}
}

// demotePartialUniversals moves universal-section entries the runtime
// does not dispatch on every receiver (to_s and string are rejected on
// arrays, hashes, and ranges) down to typed entries for the receivers
// that do dispatch them, so hover never advertises a method a value
// lacks as universal.
func (idx *memberDocIndex) demotePartialUniversals() {
	for name, entry := range idx.universal {
		dispatching := runtimeMemberIndex[name]
		universal := len(dispatching) > 0
		for receiver := range memberDocReceivers {
			if !dispatching[receiver] {
				universal = false
				break
			}
		}
		if universal {
			continue
		}
		delete(idx.universal, name)
		for receiver := range dispatching {
			if _, known := memberDocReceivers[receiver]; !known {
				continue
			}
			if slices.ContainsFunc(idx.entries[name], func(existing memberDocEntry) bool {
				return existing.Receiver == receiver
			}) {
				continue
			}
			idx.entries[name] = append(idx.entries[name], memberDocEntry{Receiver: receiver, Signature: entry.Signature, Markdown: entry.Markdown})
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
	refs, _ := leadingMemberNamesRest(text, receiver)
	return refs
}

// leadingMemberNamesRest additionally returns the text remaining after
// the leading code-span run, so callers can tell a prose bullet from a
// bare enumeration list.
func leadingMemberNamesRest(text, receiver string) ([]memberNameRef, string) {
	rest := strings.TrimSpace(text)
	var refs []memberNameRef
	for {
		rest = trimMemberSeparators(rest)
		if !strings.HasPrefix(rest, "`") {
			return refs, rest
		}
		end := strings.Index(rest[1:], "`")
		if end < 0 {
			return refs, rest
		}
		span := rest[1 : 1+end]
		next := rest[2+end:]
		ref, ok := memberRefFromSpan(span, receiver)
		if !ok {
			return refs, rest
		}
		rest = next
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
	if len(entries) == 0 {
		entries = bangVariantEntries(word)
	}
	switch len(entries) {
	case 0:
		return ""
	case 1:
		return entries[0].Markdown
	}
	// Receivers sharing identical documentation render as one section
	// (demoted near-universal helpers), so hover never repeats itself.
	type docGroup struct {
		markdown  string
		receivers []string
	}
	var groups []docGroup
	for _, entry := range entries {
		matched := false
		for i := range groups {
			if groups[i].markdown == entry.Markdown {
				groups[i].receivers = append(groups[i].receivers, entry.Receiver)
				matched = true
				break
			}
		}
		if !matched {
			groups = append(groups, docGroup{markdown: entry.Markdown, receivers: []string{entry.Receiver}})
		}
	}
	if len(groups) == 1 {
		return groups[0].markdown
	}
	var b strings.Builder
	for i, group := range groups {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		labels := make([]string, len(group.receivers))
		for j, receiver := range group.receivers {
			labels[j] = "`" + receiver + "." + word + "`"
		}
		fmt.Fprintf(&b, "%s\n\n%s", strings.Join(labels, " / "), group.markdown)
	}
	return b.String()
}

// runtimeMemberIndex maps member name -> receiver types the runtime
// dispatches it on, from the runtime's own completion table.
var runtimeMemberIndex = func() map[string]map[string]bool {
	index := make(map[string]map[string]bool)
	for receiver, members := range vibes.MemberCompletionNames() {
		for _, name := range members {
			if index[name] == nil {
				index[name] = make(map[string]bool)
			}
			index[name][receiver] = true
		}
	}
	return index
}()

// bangVariantEntries composes documentation for a string bang
// variant (strip!, gsub!) from its base member's entries, restricted
// to receivers where the runtime actually dispatches the bang name.
// The result convention comes from the stdlib reference's Bang
// Variants section: transformed value, or nil when nothing changed —
// except sub!/gsub!, which key their result off the match.
func bangVariantEntries(word string) []memberDocEntry {
	base, isBang := strings.CutSuffix(word, "!")
	if !isBang || base == "" {
		return nil
	}
	receivers := runtimeMemberIndex[word]
	if len(receivers) == 0 {
		return nil
	}
	note := "In-place variant of `" + base + "`: returns the transformed value, or `nil` when nothing changed."
	if word == "sub!" || word == "gsub!" {
		note = "In-place variant of `" + base + "`: returns the rewritten string whenever the pattern matched, and `nil` only when it never matched."
	}
	var out []memberDocEntry
	for _, entry := range memberDocs().entries[base] {
		if !receivers[entry.Receiver] {
			continue
		}
		out = append(out, memberDocEntry{
			Receiver:  entry.Receiver,
			Signature: "`" + word + "`",
			Markdown:  "`" + word + "`\n\n" + note + "\n\n" + entry.Markdown,
		})
	}
	return out
}

// namespaceDocsOnce parses section intros from the embedded builtins reference.
// Runtime catalog membership decides which sections represent namespaces; Proc
// and Regexp have no dedicated sections, so they carry hand-written one-liners.
var (
	namespaceDocsOnce  sync.Once
	namespaceDocsTable map[string]string
)

var namespaceIntroFallbacks = map[string]string{
	"Proc":   "Removed callable constructor: `Proc.new` fails with a teaching error, since executable code is not a value. Define a named function or attach a block instead.",
	"Regexp": "Ruby-style regular expression helpers for building and inspecting regex values.",
}

func namespaceDocs() map[string]string {
	namespaceDocsOnce.Do(func() {
		namespaceDocsTable = make(map[string]string)
		lines := strings.Split(vibescript.BuiltinsDoc, "\n")
		current := ""
		var intro []string
		flush := func() {
			if current == "" {
				return
			}
			text := strings.TrimSpace(strings.Join(intro, "\n"))
			if text != "" {
				namespaceDocsTable[current] = text
			}
		}
		for _, line := range lines {
			if title, ok := strings.CutPrefix(line, "## "); ok {
				flush()
				current, intro = "", nil
				current = strings.TrimSpace(title)
				continue
			}
			if strings.HasPrefix(line, "### ") {
				flush()
				current, intro = "", nil
				continue
			}
			if current != "" {
				intro = append(intro, line)
			}
		}
		flush()
		for name, text := range namespaceIntroFallbacks {
			if _, exists := namespaceDocsTable[name]; !exists {
				namespaceDocsTable[name] = text
			}
		}
	})
	return namespaceDocsTable
}

// namespaceDocMarkdown renders the hover for a bare namespace word
// (JSON, Math): the section intro from the builtins reference plus a
// member list generated from the parsed doc table, so the list can
// never drift from what qualified hovers serve.
func namespaceDocMarkdown(catalog builtinCatalog, word string) string {
	if !catalog.isNamespace(word) {
		return ""
	}
	var members []string
	prefix := word + "."
	for name := range builtinDocs() {
		if member, ok := strings.CutPrefix(name, prefix); ok {
			members = append(members, member)
		}
	}
	slices.Sort(members)
	markdown := "`" + word + "`"
	if intro := namespaceDocs()[word]; intro != "" {
		markdown += "\n\n" + intro
	}
	if len(members) > 0 {
		markdown += "\n\nMembers: `" + strings.Join(members, "`, `") + "`"
	}
	return markdown
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
	if entries := bangVariantEntries(name); len(entries) == 1 {
		return entries[0].Markdown
	}
	return ""
}

// isValueMemberAccess reports whether the word at the position is
// reached through a "." member access on a value receiver: directly
// preceded by a single dot (".." is a range, "::" a scope accessor)
// whose receiver is not a documented namespace like JSON or Math. A
// dot with no receiver word (a chained call continuing the previous
// line) still counts as member access.
func isValueMemberAccess(catalog builtinCatalog, lines []string, line, character int) bool {
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
	return !catalog.isNamespace(receiver)
}

// userSymbolHover resolves hover documentation for a symbol declared
// in the current document: top-level functions, classes and modules
// with their methods, and enums with their members. The setter form
// "name=" is tried when the bare word has no declaration, mirroring
// go-to-definition.
func userSymbolHover(program *ast.Program, lines []string, word string, hoverLine, hoverCharacter int) string {
	if program == nil || word == "" {
		return ""
	}
	// At a write site (c.value = 3) the setter is the symbol in play, so
	// the name= candidate probes first; everywhere else the plain name
	// wins so reads keep resolving to the getter.
	candidates := []string{word, word + "="}
	// Only member assignments (obj.value = 3, self.value = 3) and setter
	// declarations (def value=) name the setter; a bare identifier write
	// is a local assignment.
	if assignmentFollowsWord(lines, hoverLine, hoverCharacter) &&
		(dotPrecedesWord(lines, hoverLine, hoverCharacter) || defPrecedesWord(lines, hoverLine, hoverCharacter)) {
		candidates = []string{word + "=", word}
	}
	qualifier := receiverWordBefore(lines, hoverLine, hoverCharacter)
	memberShaped := dotPrecedesWord(lines, hoverLine, hoverCharacter)
	for _, candidate := range candidates {
		if md := userSymbolDoc(program, lines, candidate, hoverLine+1, qualifier, memberShaped); md != "" {
			return md
		}
	}
	return ""
}

// receiverWordBefore returns the receiver word when the hovered word is
// reached through "Receiver::" or "Receiver.", so qualified usages like
// First::Draft can filter candidates by their owner.
func receiverWordBefore(lines []string, line, character int) string {
	runes, start, _, ok := wordSpanAtPosition(lines, line, character)
	if !ok || start == 0 {
		return ""
	}
	var receiverEnd int
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
	receiver := string(runes[receiverStart:receiverEnd])
	// self is not an owner name: inside a class body the positional
	// scope rules already resolve self. members, so it never filters.
	if receiver == "self" {
		return ""
	}
	return receiver
}

// dotPrecedesWord reports whether the word at the position is reached
// through a "." member accessor.
func dotPrecedesWord(lines []string, line, character int) bool {
	runes, start, _, ok := wordSpanAtPosition(lines, line, character)
	return ok && start > 0 && runes[start-1] == '.'
}

// defPrecedesWord reports whether the word at the position is a method
// name in a def declaration, optionally through a self. receiver, so
// hovering the name in "def value=(v)" resolves the setter.
func defPrecedesWord(lines []string, line, character int) bool {
	runes, start, _, ok := wordSpanAtPosition(lines, line, character)
	if !ok {
		return false
	}
	i := start
	if i >= 5 && string(runes[i-5:i]) == "self." {
		i -= 5
	}
	for i > 0 && (runes[i-1] == ' ' || runes[i-1] == '\t') {
		i--
	}
	return i >= 3 && string(runes[i-3:i]) == "def" && (i == 3 || !isWordRune(runes[i-4]))
}

// assignmentFollowsWord reports whether the word at the position is
// followed (after spaces) by a bare assignment '=': the shape of a
// setter call. Comparison and match operators (==, =~) do not count.
func assignmentFollowsWord(lines []string, line, character int) bool {
	runes, _, end, ok := wordSpanAtPosition(lines, line, character)
	if !ok {
		return false
	}
	i := end
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	if i >= len(runes) || runes[i] != '=' {
		return false
	}
	return i+1 >= len(runes) || (runes[i+1] != '=' && runes[i+1] != '~')
}

// mixinDirectiveContext reports whether include/extend at this position is
// a mixin directive in the same cases the parser recognizes
// (startsMixinDirective): only in a class or module body, and then when
// followed on the same line by a name, `(`, or `self`, or when it stands
// alone and is not a local. Assignment (`include = 1`) and uses outside
// those containers stay ordinary identifiers.
func mixinDirectiveContext(program *ast.Program, lines []string, line, character int, word string) bool {
	runes, start, end, ok := wordSpanAtPosition(lines, line, character)
	if !ok {
		return false
	}
	st := mixinContainer(program, lines, line+1, start+1)
	if st == nil {
		return false
	}
	if sourceInsideLiteral(lines, line+1, start+1) ||
		sourceInsideBlockComment(lines, line+1) {
		return false
	}
	if classControlFlowContains(st, lines, line+1, start+1) {
		return false
	}
	i := end
	for i < len(runes) && (runes[i] == ' ' || runes[i] == '\t') {
		i++
	}
	if i >= len(runes) || runes[i] == '#' || runes[i] == ';' {
		return !classBodyHasLocal(st, word, line+1, start+1) &&
			!classBodyHasAccessor(st, word, lines, line+1, start+1) &&
			!classBodyHasAlias(st, word, lines, line+1, start+1)
	}
	switch runes[i] {
	case '=':
		return false
	case '(':
		return true
	default:
		if !ast.IsIdentifierStart(runes[i]) {
			return false
		}
		identStart := i
		i++
		for i < len(runes) && ast.IsIdentifierRune(runes[i]) {
			i++
		}
		switch ast.LookupIdent(string(runes[identStart:i])) {
		case ast.TokenIdent, ast.TokenSelf:
			return !classBodyHasAlias(st, word, lines, line+1, start+1)
		default:
			return false
		}
	}
}

func mixinContainer(program *ast.Program, lines []string, hoverLine, hoverColumn int) *ast.ClassStmt {
	if program == nil {
		return nil
	}
	fileEnd := len(lines) + 1
	var found *ast.ClassStmt
	var walk func(st *ast.ClassStmt, start, end int)
	walk = func(st *ast.ClassStmt, start, end int) {
		siblingStarts := classChildStarts(st)
		nestedClasses := append([]*ast.ClassStmt(nil), st.Modules...)
		for _, stmt := range st.Body {
			if nested, ok := stmt.(*ast.ClassStmt); ok {
				nestedClasses = append(nestedClasses, nested)
			}
		}
		for _, nested := range nestedClasses {
			nestedEnd := end
			for _, sibling := range siblingStarts {
				if sibling > nested.Position.Line && sibling-1 < nestedEnd {
					nestedEnd = sibling - 1
				}
			}
			if sourceEnd := sourceBlockEndLine(lines, nested.Position, nestedEnd); sourceEnd < nestedEnd {
				nestedEnd = sourceEnd
			}
			walk(nested, nested.Position.Line, nestedEnd)
			if found != nil {
				return
			}
			if hoverInsideRange(lines, hoverLine, hoverColumn, nested.Position, nestedEnd) {
				return
			}
		}
		if !hoverInsideRange(lines, hoverLine, hoverColumn, st.Position, end) {
			return
		}
		if classMethodBodyContains(st, lines, hoverLine, hoverColumn, end) {
			return
		}
		found = st
	}
	for i, stmt := range program.Statements {
		st, ok := stmt.(*ast.ClassStmt)
		if !ok {
			continue
		}
		nextStart := fileEnd
		if i+1 < len(program.Statements) {
			if pos := program.Statements[i+1].Pos(); pos.Line > 0 {
				nextStart = pos.Line
			}
		}
		walk(st, st.Position.Line, nextStart-1)
		if found != nil {
			return found
		}
	}
	return nil
}

func hoverInsideRange(lines []string, hoverLine, hoverColumn int, start ast.Position, endLine int) bool {
	if hoverLine < start.Line || hoverLine > endLine {
		return false
	}
	if hoverLine == start.Line && hoverColumn > 0 && hoverColumn <= start.Column {
		return false
	}
	if hoverLine == endLine && hoverColumn > 0 &&
		sourceBlockClosedBeforeColumn(lines, start, hoverLine, hoverColumn) {
		return false
	}
	return true
}

func sourceBlockClosedBeforeColumn(lines []string, start ast.Position, hoverLine, hoverColumn int) bool {
	if start.Line != hoverLine || hoverColumn <= start.Column {
		return false
	}
	line := lineAt(lines, hoverLine-1)
	limit := columnToByte(line, hoverColumn)
	if limit <= 0 {
		return false
	}
	text := lineFromColumn(line[:limit], start.Column)
	depth := 0
	atStmtStart := true
	inLoopHeader := false
	prev := ""
	scan := sourceScan{}
	for _, tok := range scan.tokens(text) {
		switch tok {
		case "def", "class", "module", "begin", "case":
			depth++
			atStmtStart = false
			inLoopHeader = false
		case "for", "while", "until":
			if atStmtStart || prefixControlOpens(prev) {
				depth++
				inLoopHeader = true
			}
			atStmtStart = false
		case "if", "unless":
			if atStmtStart || prefixControlOpens(prev) {
				depth++
			}
			atStmtStart = false
			inLoopHeader = false
		case "do":
			if inLoopHeader {
				inLoopHeader = false
			} else {
				depth++
			}
			atStmtStart = false
		case "end":
			depth--
			inLoopHeader = false
			if depth == 0 {
				return true
			}
			atStmtStart = false
		default:
			atStmtStart = tok == ";"
			if tok == ";" {
				inLoopHeader = false
			}
		}
		if tok != ";" {
			prev = tok
		}
	}
	return false
}

func classMethodBodyContains(st *ast.ClassStmt, lines []string, hoverLine, hoverColumn, classEnd int) bool {
	starts := classChildStarts(st)
	check := func(m *ast.FunctionStmt) bool {
		mEnd := methodSourceEndLine(lines, m, classEnd)
		for _, sibling := range starts {
			if sibling > m.Position.Line && sibling-1 < mEnd {
				mEnd = sibling - 1
			}
		}
		if hoverLine < m.Position.Line || hoverLine > mEnd {
			return false
		}
		if hoverLine == m.Position.Line && hoverColumn > 0 && hoverColumn <= m.Position.Column {
			return false
		}
		if hoverLine == mEnd && hoverColumn > 0 &&
			sourceClosedBeforeColumn(lines, m.Position, hoverLine, hoverColumn) {
			return false
		}
		return true
	}
	for _, m := range st.Methods {
		if check(m) {
			return true
		}
	}
	for _, m := range st.ClassMethods {
		if check(m) {
			return true
		}
	}
	return false
}

func classBodyHasLocal(st *ast.ClassStmt, word string, hoverLine, hoverColumn int) bool {
	if st == nil {
		return false
	}
	return statementsBindLocal(st.Body, word, hoverLine, hoverColumn)
}

func classBodyHasAlias(st *ast.ClassStmt, word string, lines []string, hoverLine, hoverColumn int) bool {
	if st == nil || word == "" {
		return false
	}
	check := func(a *ast.AliasStmt) bool {
		if a == nil || (a.NewName != word && a.OldName != word) {
			return false
		}
		return propertyDeclCoversHover(lines, ast.PropertyDecl{Position: a.Position}, hoverLine, hoverColumn)
	}
	for _, a := range st.Aliases {
		if check(a) {
			return true
		}
	}
	for _, member := range st.Members {
		if check(member.Alias) {
			return true
		}
	}
	return false
}

func classBodyHasAccessor(st *ast.ClassStmt, word string, lines []string, hoverLine, hoverColumn int) bool {
	if st == nil || word == "" {
		return false
	}
	for _, prop := range st.Properties {
		covers := false
		for _, name := range prop.Names {
			if name.Name == word {
				covers = true
				break
			}
		}
		if covers && propertyDeclCoversHover(lines, prop, hoverLine, hoverColumn) {
			return true
		}
	}
	return false
}

func propertyDeclCoversHover(lines []string, prop ast.PropertyDecl, hoverLine, hoverColumn int) bool {
	if prop.Position.Line != hoverLine {
		return false
	}
	line := lineAt(lines, hoverLine-1)
	rest := lineFromColumn(line, prop.Position.Column)
	semi := strings.IndexByte(rest, ';')
	endCol := prop.Position.Column + utf8.RuneCountInString(rest)
	if semi >= 0 {
		endCol = prop.Position.Column + utf8.RuneCountInString(rest[:semi])
	}
	return hoverColumn > prop.Position.Column && hoverColumn < endCol
}

func statementsBindLocal(stmts []ast.Statement, word string, hoverLine, hoverColumn int) bool {
	for _, stmt := range stmts {
		if statementsBindLocalOne(stmt, word, hoverLine, hoverColumn) {
			return true
		}
	}
	return false
}

func posBeforeHover(pos ast.Position, hoverLine, hoverColumn int) bool {
	if pos.Line < hoverLine {
		return true
	}
	return pos.Line == hoverLine && pos.Column < hoverColumn
}

func statementsBindLocalOne(stmt ast.Statement, word string, hoverLine, hoverColumn int) bool {
	if stmt == nil || stmt.Pos().Line > hoverLine {
		return false
	}
	switch s := stmt.(type) {
	case *ast.AssignStmt:
		return posBeforeHover(stmt.Pos(), hoverLine, hoverColumn) && assignmentBindsName(s.Target, word)
	case *ast.ForStmt:
		if posBeforeHover(stmt.Pos(), hoverLine, hoverColumn) && assignmentBindsName(s.Target, word) {
			return true
		}
		return statementsBindLocal(s.Body, word, hoverLine, hoverColumn)
	case *ast.IfStmt:
		if statementsBindLocal(s.Consequent, word, hoverLine, hoverColumn) ||
			statementsBindLocal(s.Alternate, word, hoverLine, hoverColumn) {
			return true
		}
		for _, branch := range s.ElseIf {
			if statementsBindLocalOne(branch, word, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.WhileStmt:
		return statementsBindLocal(s.Body, word, hoverLine, hoverColumn)
	case *ast.UntilStmt:
		return statementsBindLocal(s.Body, word, hoverLine, hoverColumn)
	case *ast.TryStmt:
		if statementsBindLocal(s.Body, word, hoverLine, hoverColumn) ||
			statementsBindLocal(s.Else, word, hoverLine, hoverColumn) ||
			statementsBindLocal(s.Ensure, word, hoverLine, hoverColumn) {
			return true
		}
		for _, clause := range s.Rescues {
			if clause.Binding == word && rescueBindingCovers(clause, hoverLine, hoverColumn) {
				return true
			}
			if statementsBindLocal(clause.Body, word, hoverLine, hoverColumn) {
				return true
			}
		}
	}
	return false
}

func methodSourceEndLine(lines []string, m *ast.FunctionStmt, classEnd int) int {
	if m == nil {
		return classEnd
	}
	return sourceBlockEndLine(lines, m.Position, classEnd)
}

func sourceBlockEndLine(lines []string, start ast.Position, classEnd int) int {
	if start.Line < 1 {
		return classEnd
	}
	last := classEnd
	if last > len(lines) {
		last = len(lines)
	}
	depth := 0
	inLoopHeader := false
	inBlockComment := false
	scan := sourceScan{}
	for line := start.Line; line <= last; line++ {
		atStmtStart := true
		scan.afterValue = false
		scan.afterDot = false
		prev := ""
		text := lineAt(lines, line-1)
		if inBlockComment {
			if isBlockCommentEnd(text) {
				inBlockComment = false
			}
			continue
		}
		if isBlockCommentBegin(text) {
			inBlockComment = true
			continue
		}
		if line == start.Line {
			text = lineFromColumn(text, start.Column)
		}
		for _, tok := range scan.tokens(text) {
			switch tok {
			case "def", "class", "module", "begin", "case":
				depth++
				atStmtStart = false
				inLoopHeader = false
			case "for", "while", "until":
				if atStmtStart || prefixControlOpens(prev) {
					depth++
					inLoopHeader = true
				}
				atStmtStart = false
			case "if", "unless":
				if atStmtStart || prefixControlOpens(prev) {
					depth++
				}
				atStmtStart = false
				inLoopHeader = false
			case "do":
				if inLoopHeader {
					inLoopHeader = false
				} else {
					depth++
				}
				atStmtStart = false
			case "end":
				depth--
				inLoopHeader = false
				if depth == 0 {
					return line
				}
				atStmtStart = false
			default:
				atStmtStart = tok == ";"
				if tok == ";" {
					inLoopHeader = false
				}
			}
			if tok != ";" {
				prev = tok
			}
		}
		inLoopHeader = false
	}
	return classEnd
}

func lineFromColumn(line string, column int) string {
	idx := columnToByte(line, column)
	if idx >= len(line) {
		return ""
	}
	return line[idx:]
}

func columnToByte(line string, column int) int {
	if column <= 1 {
		return 0
	}
	n := 1
	for i := range line {
		if n == column {
			return i
		}
		n++
	}
	return len(line)
}

func prefixControlOpens(prev string) bool {
	return !tokenIsValue(prev)
}

func tokenIsValue(tok string) bool {
	if tok == "" {
		return false
	}
	switch tok {
	case "end", "true", "false", "nil", "self", ")", "]", "}", `""`, "//", "%":
		return true
	}
	r, _ := utf8.DecodeRuneInString(tok)
	return ast.IsIdentifierStart(r) || r >= '0' && r <= '9'
}

func interpIdentSkip(s string, i int) (name string, extra int, ok bool) {
	r, size := utf8.DecodeRuneInString(s[i:])
	if size < 1 {
		size = 1
	}
	if !ast.IsIdentifierStart(r) && (r < '0' || r > '9') {
		return "", 0, false
	}
	start := i
	i += size
	for i < len(s) {
		r, size = utf8.DecodeRuneInString(s[i:])
		if size < 1 {
			size = 1
		}
		if !ast.IsIdentifierRune(r) {
			break
		}
		i += size
	}
	return s[start:i], i - start - 1, true
}

func seedImplicitBlockLocals(locals map[string]struct{}) {
	locals["it"] = struct{}{}
	for i := 1; i <= 9; i++ {
		locals["_"+string(rune('0'+i))] = struct{}{}
	}
}

func isControlKeyword(tok string) bool {
	switch tok {
	case "def", "class", "module", "begin", "case", "end",
		"if", "unless", "while", "until", "for", "do",
		"rescue", "ensure", "else", "elsif", "when":
		return true
	default:
		return false
	}
}

func identCanBeLocal(tok string) bool {
	if tok == "" || strings.HasPrefix(tok, ".") || strings.HasSuffix(tok, ":") {
		return false
	}
	r, _ := utf8.DecodeRuneInString(tok)
	return ast.IsIdentifierStart(r)
}

func slashStartsRegex(afterValue, afterSpace bool, s string, i int, lastIdent string, locals map[string]struct{}) bool {
	if !afterValue {
		return true
	}
	if !afterSpace || i+1 >= len(s) {
		return false
	}
	switch s[i+1] {
	case ' ', '\t', '\r', '\n':
		return false
	default:
		if !identCanBeLocal(lastIdent) {
			return false
		}
		if _, ok := locals[lastIdent]; ok {
			return false
		}
		return true
	}
}

func posixClassAt(s string, i int) bool {
	if i < 0 || i+3 >= len(s) || s[i] != '[' || s[i+1] != ':' {
		return false
	}
	j := i + 2
	if j < len(s) && s[j] == '^' {
		j++
	}
	nameStart := j
	for j < len(s) {
		c := s[j]
		if c == ':' {
			return j > nameStart && j+1 < len(s) && s[j+1] == ']'
		}
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
		j++
	}
	return false
}

func interpRegexConsume(s string, i int, c byte, escape, inCharClass, inCharClassStart bool) (
	nextEscape, closed, nextClass, nextClassStart bool, extra int,
) {
	if escape {
		return false, false, inCharClass, inCharClassStart, 0
	}
	if c == '\\' {
		return true, false, inCharClass, inCharClassStart, 0
	}
	if inCharClass {
		if inCharClassStart && c == '^' {
			return false, false, true, true, 0
		}
		if inCharClassStart && c == ']' {
			return false, false, true, false, 0
		}
		if posixClassAt(s, i) {
			j := i + 2
			for j+1 < len(s) && (s[j] != ':' || s[j+1] != ']') {
				j++
			}
			if j+1 < len(s) {
				j++
			}
			return false, false, true, false, j - i
		}
		if c == ']' {
			return false, false, false, false, 0
		}
		return false, false, true, false, 0
	}
	if c == '[' {
		return false, false, true, true, 0
	}
	if c == '/' {
		return false, true, false, false, 0
	}
	return false, false, false, false, 0
}

func sourceClosedBeforeColumn(lines []string, start ast.Position, hoverLine, hoverColumn int) bool {
	if start.Line != hoverLine || hoverColumn <= start.Column {
		return false
	}
	line := lineAt(lines, hoverLine-1)
	limit := columnToByte(line, hoverColumn)
	if limit <= 0 {
		return false
	}
	scan := sourceScan{}
	depth := 0
	atStmtStart := true
	inLoopHeader := false
	prev := ""
	seenDef := false
	for _, tok := range scan.tokens(line[:limit]) {
		if !seenDef {
			if tok == "def" {
				seenDef = true
				depth = 1
				atStmtStart = false
				prev = tok
			}
			continue
		}
		switch tok {
		case "def", "class", "module", "begin", "case":
			depth++
			atStmtStart = false
			inLoopHeader = false
		case "for", "while", "until":
			if atStmtStart || prefixControlOpens(prev) {
				depth++
				inLoopHeader = true
			}
			atStmtStart = false
		case "if", "unless":
			if atStmtStart || prefixControlOpens(prev) {
				depth++
			}
			atStmtStart = false
			inLoopHeader = false
		case "do":
			if inLoopHeader {
				inLoopHeader = false
			} else {
				depth++
			}
			atStmtStart = false
		case "end":
			depth--
			inLoopHeader = false
			if depth <= 0 {
				return true
			}
			atStmtStart = false
		default:
			atStmtStart = tok == ";"
			if tok == ";" {
				inLoopHeader = false
			}
		}
		if tok != ";" {
			prev = tok
		}
	}
	return false
}

type sourceScan struct {
	inStr              byte
	percentOpen        byte
	percentClose       byte
	percentDepth       int
	interpDepth        int
	escape             bool
	afterValue         bool
	afterDot           bool
	inRegex            bool
	hitComment         bool
	innerStr           byte
	inCharClass        bool
	inCharClassStart   bool
	lastIdent          string
	locals             map[string]struct{}
	afterDef           bool
	afterDefName       bool
	inParams           bool
	paramDepth         int
	inPipes            bool
	afterBrace         bool
	interpPercentOpen  byte
	interpPercentClose byte
	interpPercentDepth int
	openers            []string
	localsStack        []map[string]struct{}
}

func (sc *sourceScan) tokens(s string) []string {
	var tokens []string
	start := -1
	inStr := sc.inStr
	percentOpen := sc.percentOpen
	percentClose := sc.percentClose
	percentDepth := sc.percentDepth
	escape := sc.escape
	afterValue := sc.afterValue
	afterDot := sc.afterDot
	inRegex := sc.inRegex
	interpDepth := sc.interpDepth
	innerStr := sc.innerStr
	hitComment := false
	inCharClass := sc.inCharClass
	inCharClassStart := sc.inCharClassStart
	lastIdent := sc.lastIdent
	locals := sc.locals
	if locals == nil {
		locals = map[string]struct{}{}
	}
	afterDef := sc.afterDef
	afterDefName := sc.afterDefName
	inParams := sc.inParams
	paramDepth := sc.paramDepth
	inPipes := sc.inPipes
	afterBrace := sc.afterBrace
	interpPercentOpen := sc.interpPercentOpen
	interpPercentClose := sc.interpPercentClose
	interpPercentDepth := sc.interpPercentDepth
	openers := sc.openers
	localsStack := sc.localsStack
	afterSpace := false
	atStmtStart := true
	inLoopHeader := false
	prevTok := ""
	applyOpener := func(tok string) {
		switch tok {
		case "def":
			localsStack = append(localsStack, locals)
			locals = map[string]struct{}{}
			afterDef = true
			afterDefName = false
			inParams = false
			paramDepth = 0
			inPipes = false
			openers = append(openers, "def")
			atStmtStart = false
			inLoopHeader = false
		case "class", "module", "begin", "case":
			openers = append(openers, tok)
			atStmtStart = false
			inLoopHeader = false
			afterDef = false
		case "for", "while", "until":
			if atStmtStart || prefixControlOpens(prevTok) {
				openers = append(openers, tok)
				inLoopHeader = true
			}
			atStmtStart = false
		case "if", "unless":
			if atStmtStart || prefixControlOpens(prevTok) {
				openers = append(openers, tok)
			}
			atStmtStart = false
			inLoopHeader = false
		case "do":
			if inLoopHeader {
				inLoopHeader = false
			} else {
				openers = append(openers, "do")
				localsStack = append(localsStack, locals)
				cloned := make(map[string]struct{}, len(locals))
				for name := range locals {
					cloned[name] = struct{}{}
				}
				seedImplicitBlockLocals(cloned)
				locals = cloned
			}
			atStmtStart = false
		case "end":
			if n := len(openers); n > 0 {
				top := openers[n-1]
				openers = openers[:n-1]
				if (top == "def" || top == "do" || top == "brace") && len(localsStack) > 0 {
					locals = localsStack[len(localsStack)-1]
					localsStack = localsStack[:len(localsStack)-1]
					afterDef = false
					afterDefName = false
					inParams = false
					paramDepth = 0
					inPipes = false
				}
			}
			atStmtStart = false
			inLoopHeader = false
		default:
			atStmtStart = tok == ";"
			if tok == ";" {
				inLoopHeader = false
				afterDef = false
			}
		}
		if tok != ";" {
			prevTok = tok
		}
	}
	flush := func(i int) {
		if start >= 0 {
			tok := s[start:i]
			j := i
			for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
				j++
			}
			if j < len(s) && s[j] == ':' {
				tok += ":"
			} else if afterDot {
				tok = "." + tok
			}
			tokens = append(tokens, tok)
			start = -1
			afterValue = true
			afterDot = false
			lastIdent = tok
			if (inParams || inPipes) && identCanBeLocal(tok) {
				locals[tok] = struct{}{}
			} else if afterDef && !inParams && identCanBeLocal(tok) && !isControlKeyword(tok) {
				if afterDefName {
					locals[tok] = struct{}{}
				} else {
					afterDefName = true
				}
			}
			applyOpener(tok)
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr != 0 {
			if interpDepth > 0 {
				if innerStr != 0 {
					if escape {
						escape = false
						continue
					}
					if c == '\\' {
						escape = true
						continue
					}
					if c == innerStr {
						innerStr = 0
					}
					continue
				}
				if inRegex {
					var extra int
					var closed bool
					escape, closed, inCharClass, inCharClassStart, extra = interpRegexConsume(
						s, i, c, escape, inCharClass, inCharClassStart)
					if closed {
						inRegex = false
						afterValue = true
					}
					i += extra
					continue
				}
				if interpPercentDepth > 0 {
					end, depth, closed := skipPercentArrayFrom(s, i, interpPercentOpen, interpPercentClose, interpPercentDepth)
					if closed {
						interpPercentDepth = 0
						afterValue = true
						i = end
						continue
					}
					interpPercentDepth = depth
					break
				}
				if c == ' ' || c == '\t' {
					afterSpace = true
					continue
				}
				if c == '/' && slashStartsRegex(afterValue, afterSpace, s, i, lastIdent, locals) {
					inRegex = true
					inCharClass = false
					inCharClassStart = false
					afterSpace = false
					continue
				}
				afterSpace = false
				if !afterValue && c == '%' && i+2 < len(s) && strings.ContainsRune("wWiIqQ", rune(s[i+1])) {
					open := s[i+2]
					close := percentLiteralCloser(open)
					end, depth, closed := skipPercentArrayFrom(s, i+3, open, close, 1)
					if closed {
						afterValue = true
						i = end
						continue
					}
					interpPercentOpen = open
					interpPercentClose = close
					interpPercentDepth = depth
					break
				}
				if c == '#' {
					if i+1 < len(s) && s[i+1] == '{' {
						interpDepth++
						afterValue = false
						lastIdent = ""
						i++
						continue
					}
					break
				}
				if c == '"' || c == '\'' {
					innerStr = c
					afterValue = true
					continue
				}
				if c == '{' {
					interpDepth++
					afterValue = false
					lastIdent = ""
					continue
				}
				if c == '}' {
					interpDepth--
					afterValue = true
					continue
				}
				if name, extra, ok := interpIdentSkip(s, i); ok {
					afterValue = true
					lastIdent = name
					i += extra
					continue
				}
				afterValue = false
				lastIdent = ""
				continue
			}
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == '#' && i+1 < len(s) && s[i+1] == '{' {
				interpDepth = 1
				afterValue = false
				afterSpace = false
				lastIdent = ""
				i++
				continue
			}
			if c == inStr {
				inStr = 0
				tokens = append(tokens, `""`)
				afterValue = true
			}
			continue
		}
		if percentClose != 0 {
			if interpDepth > 0 {
				if innerStr != 0 {
					if escape {
						escape = false
						continue
					}
					if c == '\\' {
						escape = true
						continue
					}
					if c == innerStr {
						innerStr = 0
					}
					continue
				}
				if inRegex {
					var extra int
					var closed bool
					escape, closed, inCharClass, inCharClassStart, extra = interpRegexConsume(
						s, i, c, escape, inCharClass, inCharClassStart)
					if closed {
						inRegex = false
						afterValue = true
					}
					i += extra
					continue
				}
				if interpPercentDepth > 0 {
					end, depth, closed := skipPercentArrayFrom(s, i, interpPercentOpen, interpPercentClose, interpPercentDepth)
					if closed {
						interpPercentDepth = 0
						afterValue = true
						i = end
						continue
					}
					interpPercentDepth = depth
					break
				}
				if c == ' ' || c == '\t' {
					afterSpace = true
					continue
				}
				if c == '/' && slashStartsRegex(afterValue, afterSpace, s, i, lastIdent, locals) {
					inRegex = true
					inCharClass = false
					inCharClassStart = false
					afterSpace = false
					continue
				}
				afterSpace = false
				if !afterValue && c == '%' && i+2 < len(s) && strings.ContainsRune("wWiIqQ", rune(s[i+1])) {
					open := s[i+2]
					close := percentLiteralCloser(open)
					end, depth, closed := skipPercentArrayFrom(s, i+3, open, close, 1)
					if closed {
						afterValue = true
						i = end
						continue
					}
					interpPercentOpen = open
					interpPercentClose = close
					interpPercentDepth = depth
					break
				}
				if c == '#' {
					if i+1 < len(s) && s[i+1] == '{' {
						interpDepth++
						afterValue = false
						lastIdent = ""
						i++
						continue
					}
					break
				}
				if c == '"' || c == '\'' {
					innerStr = c
					afterValue = true
					continue
				}
				if c == '{' {
					interpDepth++
					afterValue = false
					lastIdent = ""
					continue
				}
				if c == '}' {
					interpDepth--
					afterValue = true
					continue
				}
				if name, extra, ok := interpIdentSkip(s, i); ok {
					afterValue = true
					lastIdent = name
					i += extra
					continue
				}
				afterValue = false
				lastIdent = ""
				continue
			}
			if c == '#' && i+1 < len(s) && s[i+1] == '{' {
				interpDepth = 1
				afterValue = false
				afterSpace = false
				lastIdent = ""
				i++
				continue
			}
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if c == percentOpen && percentOpen != percentClose {
				percentDepth++
			} else if c == percentClose {
				percentDepth--
				if percentDepth <= 0 {
					percentOpen = 0
					percentClose = 0
					percentDepth = 0
					afterValue = true
					tokens = append(tokens, "%")
				}
			}
			continue
		}
		if inRegex {
			if escape {
				escape = false
				continue
			}
			if c == '\\' {
				escape = true
				continue
			}
			if inCharClass {
				if inCharClassStart && c == '^' {
					continue
				}
				if inCharClassStart && c == ']' {
					inCharClassStart = false
					continue
				}
				inCharClassStart = false
				if posixClassAt(s, i) {
					i += 2
					for i+1 < len(s) && (s[i] != ':' || s[i+1] != ']') {
						i++
					}
					if i+1 < len(s) {
						i++
					}
					continue
				}
				if c == ']' {
					inCharClass = false
				}
				continue
			}
			if c == '[' {
				inCharClass = true
				inCharClassStart = true
				continue
			}
			if c == '/' {
				inRegex = false
				afterValue = true
				tokens = append(tokens, "//")
			}
			continue
		}
		if c == '#' {
			hitComment = true
			break
		}
		if c == '%' && i+1 < len(s) {
			kind := s[i+1]
			if strings.ContainsRune("wWiIqQ", rune(kind)) && i+2 < len(s) {
				open := s[i+2]
				close := percentLiteralCloser(open)
				flush(i)
				i += 3
				depth := 1
				for i < len(s) && depth > 0 {
					if interpDepth > 0 {
						ch := s[i]
						if innerStr != 0 {
							if escape {
								escape = false
								i++
								continue
							}
							if ch == '\\' {
								escape = true
								i++
								continue
							}
							if ch == innerStr {
								innerStr = 0
							}
							i++
							continue
						}
						if inRegex {
							var extra int
							var closed bool
							escape, closed, inCharClass, inCharClassStart, extra = interpRegexConsume(
								s, i, ch, escape, inCharClass, inCharClassStart)
							if closed {
								inRegex = false
								afterValue = true
							}
							i += extra + 1
							continue
						}
						if ch == '/' && slashStartsRegex(afterValue, afterSpace, s, i, lastIdent, locals) {
							inRegex = true
							inCharClass = false
							inCharClassStart = false
							afterSpace = false
							i++
							continue
						}
						if ch == '#' {
							if i+1 < len(s) && s[i+1] == '{' {
								interpDepth++
								afterValue = false
								lastIdent = ""
								i += 2
								continue
							}
							break
						}
						if ch == '"' || ch == '\'' {
							innerStr = ch
							i++
							continue
						}
						if ch == '{' {
							interpDepth++
							afterValue = false
							lastIdent = ""
							i++
							continue
						}
						if ch == '}' {
							interpDepth--
							afterValue = true
							i++
							continue
						}
						if name, extra, ok := interpIdentSkip(s, i); ok {
							afterValue = true
							lastIdent = name
							i += extra + 1
							continue
						}
						afterValue = false
						lastIdent = ""
						i++
						continue
					}
					if s[i] == '\\' && i+1 < len(s) {
						i += 2
						continue
					}
					if s[i] == '#' && i+1 < len(s) && s[i+1] == '{' {
						interpDepth = 1
						afterValue = false
						lastIdent = ""
						i += 2
						continue
					}
					if s[i] == open && open != close {
						depth++
					} else if s[i] == close {
						depth--
						if depth == 0 {
							break
						}
					}
					i++
				}
				if depth > 0 {
					percentOpen = open
					percentClose = close
					percentDepth = depth
				} else {
					tokens = append(tokens, "%")
				}
				afterValue = true
				afterDot = false
				afterSpace = false
				continue
			}
		}
		if c == ':' && i+1 < len(s) && (s[i+1] == '_' ||
			s[i+1] >= 'A' && s[i+1] <= 'Z' ||
			s[i+1] >= 'a' && s[i+1] <= 'z') {
			flush(i)
			i++
			start = i
			i++
			for i < len(s) {
				ch := s[i]
				if ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
					i++
					continue
				}
				break
			}
			start = -1
			afterValue = true
			afterSpace = false
			i--
			continue
		}
		if c == '"' || c == '\'' {
			flush(i)
			inStr = c
			afterValue = true
			afterSpace = false
			continue
		}
		if c == ' ' || c == '\t' {
			flush(i)
			afterSpace = true
			continue
		}
		if c == '/' && slashStartsRegex(afterValue, afterSpace, s, i, lastIdent, locals) {
			flush(i)
			i++
			escape = false
			closed := false
			inCharClass = false
			for i < len(s) {
				ch := s[i]
				if escape {
					escape = false
					i++
					continue
				}
				if ch == '\\' {
					escape = true
					i++
					continue
				}
				if inCharClass {
					if inCharClassStart && ch == '^' {
						i++
						continue
					}
					if inCharClassStart && ch == ']' {
						inCharClassStart = false
						i++
						continue
					}
					inCharClassStart = false
					if posixClassAt(s, i) {
						i += 2
						for i+1 < len(s) && (s[i] != ':' || s[i+1] != ']') {
							i++
						}
						if i+1 < len(s) {
							i++
						}
						i++
						continue
					}
					if ch == ']' {
						inCharClass = false
					}
					i++
					continue
				}
				if ch == '[' {
					inCharClass = true
					inCharClassStart = true
					i++
					continue
				}
				if ch == '/' {
					closed = true
					i++
					for i < len(s) && (s[i] == 'i' || s[i] == 'm' || s[i] == 'x' || s[i] == 'o' || s[i] == 'u' || s[i] == 'n') {
						i++
					}
					tokens = append(tokens, "//")
					break
				}
				i++
			}
			if !closed {
				inRegex = true
			}
			i--
			afterValue = true
			afterSpace = false
			continue
		}
		afterSpace = false
		if c == '.' {
			flush(i)
			afterDot = true
			afterValue = false
			continue
		}
		if c == '=' {
			if i+1 < len(s) && (s[i+1] == '=' || s[i+1] == '~' || s[i+1] == '>') {
				flush(i)
				i++
				afterValue = false
				afterDot = false
				continue
			}
			flush(i)
			for j := len(tokens) - 1; j >= 0; j-- {
				tok := tokens[j]
				if tok == ";" || tok == "=" || isControlKeyword(tok) {
					break
				}
				if identCanBeLocal(tok) {
					locals[tok] = struct{}{}
				}
			}
			tokens = append(tokens, "=")
			afterValue = false
			afterDot = false
			continue
		}
		if c == ';' {
			flush(i)
			tokens = append(tokens, ";")
			applyOpener(";")
			afterValue = false
			afterDot = false
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if size < 1 {
			size = 1
		}
		identCont := start >= 0 && ast.IsIdentifierRune(r)
		identStart := start < 0 && (ast.IsIdentifierStart(r) || r >= '0' && r <= '9')
		if identStart || identCont {
			if start < 0 {
				start = i
			}
			i += size - 1
			continue
		}
		flush(i)
		switch c {
		case '(', ')', '[', ']', '{', '}', ',',
			'+', '-', '*', '/', '%', '&', '|', '^', '<', '>', '!':
			tokens = append(tokens, string(c))
		}
		switch c {
		case '(':
			if afterDef {
				inParams = true
				afterDef = false
				paramDepth = 1
			} else if inParams {
				paramDepth++
			}
		case ')':
			if inParams {
				paramDepth--
				if paramDepth <= 0 {
					inParams = false
				}
			}
		case '|':
			inPipes = !inPipes
			afterBrace = false
		case '{':
			afterDef = false
			afterBrace = true
			if afterValue {
				openers = append(openers, "brace")
				localsStack = append(localsStack, locals)
				cloned := make(map[string]struct{}, len(locals))
				for name := range locals {
					cloned[name] = struct{}{}
				}
				seedImplicitBlockLocals(cloned)
				locals = cloned
			}
		case '}':
			afterBrace = false
			if n := len(openers); n > 0 && openers[n-1] == "brace" {
				openers = openers[:n-1]
				if len(localsStack) > 0 {
					locals = localsStack[len(localsStack)-1]
					localsStack = localsStack[:len(localsStack)-1]
				}
				inPipes = false
			}
		case ';':
			afterDef = false
			afterBrace = false
		}
		afterValue = false
		afterDot = false
	}
	flush(len(s))
	sc.inStr = inStr
	sc.percentOpen = percentOpen
	sc.percentClose = percentClose
	sc.percentDepth = percentDepth
	sc.escape = escape
	sc.afterValue = afterValue
	sc.afterDot = afterDot
	sc.inRegex = inRegex
	sc.interpDepth = interpDepth
	sc.innerStr = innerStr
	sc.hitComment = hitComment
	sc.inCharClass = inCharClass
	sc.inCharClassStart = inCharClassStart
	sc.lastIdent = lastIdent
	sc.locals = locals
	sc.afterDef = afterDef
	sc.afterDefName = afterDefName
	sc.inParams = inParams
	sc.paramDepth = paramDepth
	sc.inPipes = inPipes
	sc.afterBrace = afterBrace
	sc.interpPercentOpen = interpPercentOpen
	sc.interpPercentClose = interpPercentClose
	sc.interpPercentDepth = interpPercentDepth
	sc.openers = openers
	sc.localsStack = localsStack
	return tokens
}

func isBlockCommentBegin(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "=begin") {
		return false
	}
	rest := trimmed[len("=begin"):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

func isBlockCommentEnd(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "=end") {
		return false
	}
	rest := trimmed[len("=end"):]
	return rest == "" || rest[0] == ' ' || rest[0] == '\t'
}

func sourceInsideBlockComment(lines []string, hoverLine int) bool {
	in := false
	for line := 1; line <= hoverLine && line <= len(lines); line++ {
		text := lineAt(lines, line-1)
		if in {
			if line == hoverLine {
				return true
			}
			if isBlockCommentEnd(text) {
				in = false
			}
			continue
		}
		if isBlockCommentBegin(text) {
			in = true
			if line == hoverLine {
				return true
			}
		}
	}
	return false
}

func sourceInsideLiteral(lines []string, hoverLine, hoverColumn int) bool {
	if hoverLine < 1 {
		return false
	}
	scan := sourceScan{}
	inBlockComment := false
	for line := 1; line < hoverLine && line <= len(lines); line++ {
		text := lineAt(lines, line-1)
		if inBlockComment {
			if isBlockCommentEnd(text) {
				inBlockComment = false
			}
			continue
		}
		if isBlockCommentBegin(text) {
			inBlockComment = true
			continue
		}
		scan.afterValue = false
		scan.afterDot = false
		scan.tokens(text)
	}
	if inBlockComment {
		return true
	}
	if hoverLine > len(lines) {
		return scan.inStr != 0 || scan.percentClose != 0 || scan.inRegex
	}
	text := lineAt(lines, hoverLine-1)
	idx := columnToByte(text, hoverColumn)
	if idx > len(text) {
		idx = len(text)
	}
	if idx < len(text) {
		_, size := utf8.DecodeRuneInString(text[idx:])
		if size < 1 {
			size = 1
		}
		idx += size
	}
	scan.afterValue = false
	scan.afterDot = false
	scan.tokens(text[:idx])
	return scan.inStr != 0 || scan.percentClose != 0 || scan.inRegex ||
		scan.hitComment || scan.interpDepth > 0
}

func skipPercentArrayFrom(s string, i int, open, close byte, depth int) (end, newDepth int, closed bool) {
	for i < len(s) && depth > 0 {
		if s[i] == '\\' && i+1 < len(s) {
			i += 2
			continue
		}
		if open != close && s[i] == open {
			depth++
		} else if s[i] == close {
			depth--
			if depth == 0 {
				return i, 0, true
			}
		}
		i++
	}
	return i, depth, false
}

func percentLiteralCloser(open byte) byte {
	switch open {
	case '[':
		return ']'
	case '(':
		return ')'
	case '{':
		return '}'
	case '<':
		return '>'
	default:
		return open
	}
}

func classControlFlowContains(st *ast.ClassStmt, lines []string, hoverLine, hoverColumn int) bool {
	if st == nil {
		return false
	}
	return nestedControlContains(st.Body, lines, hoverLine, hoverColumn)
}

func nestedControlContains(stmts []ast.Statement, lines []string, hoverLine, hoverColumn int) bool {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ExprStmt:
			if expressionContainsHover(s.Expr, hoverLine, hoverColumn) {
				return true
			}
		case *ast.AssignStmt:
			if expressionContainsHover(s.Target, hoverLine, hoverColumn) ||
				expressionContainsHover(s.Value, hoverLine, hoverColumn) {
				return true
			}
		case *ast.RaiseStmt:
			if expressionContainsHover(s.Value, hoverLine, hoverColumn) ||
				expressionContainsHover(s.Message, hoverLine, hoverColumn) {
				return true
			}
		case *ast.ReturnStmt:
			if expressionContainsHover(s.Value, hoverLine, hoverColumn) {
				return true
			}
		case *ast.BreakStmt:
			if expressionContainsHover(s.Value, hoverLine, hoverColumn) {
				return true
			}
		case *ast.NextStmt:
			if expressionContainsHover(s.Value, hoverLine, hoverColumn) {
				return true
			}
		case *ast.IfStmt:
			if expressionContainsHover(s.Condition, hoverLine, hoverColumn) ||
				lineInStatements(s.Consequent, lines, hoverLine, hoverColumn) ||
				lineInStatements(s.Alternate, lines, hoverLine, hoverColumn) {
				return true
			}
			for _, branch := range s.ElseIf {
				if nestedControlContains([]ast.Statement{branch}, lines, hoverLine, hoverColumn) {
					return true
				}
			}
		case *ast.ForStmt:
			if expressionContainsHover(s.Target, hoverLine, hoverColumn) ||
				expressionContainsHover(s.Iterable, hoverLine, hoverColumn) ||
				lineInStatements(s.Body, lines, hoverLine, hoverColumn) {
				return true
			}
		case *ast.WhileStmt:
			if expressionContainsHover(s.Condition, hoverLine, hoverColumn) ||
				lineInStatements(s.Body, lines, hoverLine, hoverColumn) {
				return true
			}
		case *ast.UntilStmt:
			if expressionContainsHover(s.Condition, hoverLine, hoverColumn) ||
				lineInStatements(s.Body, lines, hoverLine, hoverColumn) {
				return true
			}
		case *ast.TryStmt:
			if lineInStatements(s.Body, lines, hoverLine, hoverColumn) ||
				lineInStatements(s.Else, lines, hoverLine, hoverColumn) ||
				lineInStatements(s.Ensure, lines, hoverLine, hoverColumn) {
				return true
			}
			for _, clause := range s.Rescues {
				if lineInStatements(clause.Body, lines, hoverLine, hoverColumn) {
					return true
				}
			}
		}
	}
	return false
}

func memberPropertyContainsHover(e *ast.MemberExpr, hoverLine, hoverColumn int) bool {
	if e == nil || e.Property == "" {
		return false
	}
	line, col, ok := expressionSourceEnd(e.Object)
	if !ok || line != hoverLine {
		return false
	}
	sep := 1
	if e.Safe {
		sep = 2
	}
	start := col + sep
	if hoverColumn <= 0 {
		return true
	}
	return hoverColumn >= start && hoverColumn < start+utf8.RuneCountInString(e.Property)
}

func scopePropertyContainsHover(e *ast.ScopeExpr, hoverLine, hoverColumn int) bool {
	if e == nil || e.Property == "" {
		return false
	}
	line, col, ok := expressionSourceEnd(e.Object)
	if !ok || line != hoverLine {
		return false
	}
	start := col + 2
	if hoverColumn <= 0 {
		return true
	}
	return hoverColumn >= start && hoverColumn < start+utf8.RuneCountInString(e.Property)
}

func expressionSourceEnd(expr ast.Expression) (line, col int, ok bool) {
	if expr == nil {
		return 0, 0, false
	}
	switch e := expr.(type) {
	case *ast.Identifier:
		return e.Pos().Line, e.Pos().Column + utf8.RuneCountInString(e.Name), true
	case *ast.ScopeExpr:
		line, col, ok = expressionSourceEnd(e.Object)
		if !ok || line != e.Object.Pos().Line {
			return 0, 0, false
		}
		return line, col + 2 + utf8.RuneCountInString(e.Property), true
	default:
		return 0, 0, false
	}
}

func expressionContainsHover(expr ast.Expression, hoverLine, hoverColumn int) bool {
	if expr == nil {
		return false
	}
	switch e := expr.(type) {
	case *ast.CallExpr:
		if e.Block != nil && lineInStatements(e.Block.Body, nil, hoverLine, hoverColumn) {
			return true
		}
		if expressionContainsHover(e.Callee, hoverLine, hoverColumn) {
			return true
		}
		for _, arg := range e.Args {
			if expressionContainsHover(arg, hoverLine, hoverColumn) {
				return true
			}
		}
		for _, kwarg := range e.KwArgs {
			if expressionContainsHover(kwarg.Value, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.ArrayLiteral:
		for _, el := range e.Elements {
			if expressionContainsHover(el, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.HashLiteral:
		for _, pair := range e.Pairs {
			if expressionContainsHover(pair.Key, hoverLine, hoverColumn) ||
				expressionContainsHover(pair.Value, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.MemberExpr:
		if expressionContainsHover(e.Object, hoverLine, hoverColumn) {
			return true
		}
		return memberPropertyContainsHover(e, hoverLine, hoverColumn)
	case *ast.ScopeExpr:
		if expressionContainsHover(e.Object, hoverLine, hoverColumn) {
			return true
		}
		return scopePropertyContainsHover(e, hoverLine, hoverColumn)
	case *ast.IndexExpr:
		if expressionContainsHover(e.Object, hoverLine, hoverColumn) {
			return true
		}
		for _, index := range e.Indices {
			if expressionContainsHover(index, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.UnaryExpr:
		return expressionContainsHover(e.Right, hoverLine, hoverColumn)
	case *ast.BinaryExpr:
		return expressionContainsHover(e.Left, hoverLine, hoverColumn) ||
			expressionContainsHover(e.Right, hoverLine, hoverColumn)
	case *ast.RangeExpr:
		return expressionContainsHover(e.Start, hoverLine, hoverColumn) ||
			expressionContainsHover(e.End, hoverLine, hoverColumn)
	case *ast.ConditionalExpr:
		return expressionContainsHover(e.Condition, hoverLine, hoverColumn) ||
			expressionContainsHover(e.Consequent, hoverLine, hoverColumn) ||
			expressionContainsHover(e.Alternate, hoverLine, hoverColumn)
	case *ast.RescueExpr:
		return expressionContainsHover(e.Body, hoverLine, hoverColumn) ||
			expressionContainsHover(e.Fallback, hoverLine, hoverColumn)
	case *ast.SplatArg:
		return expressionContainsHover(e.Value, hoverLine, hoverColumn)
	case *ast.YieldExpr:
		for _, arg := range e.Args {
			if expressionContainsHover(arg, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.BlockLiteral:
		return lineInStatements(e.Body, nil, hoverLine, hoverColumn)
	case *ast.TryStmt:
		if lineInStatements(e.Body, nil, hoverLine, hoverColumn) ||
			lineInStatements(e.Else, nil, hoverLine, hoverColumn) ||
			lineInStatements(e.Ensure, nil, hoverLine, hoverColumn) {
			return true
		}
		for _, clause := range e.Rescues {
			if lineInStatements(clause.Body, nil, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.IfExpr:
		if expressionContainsHover(e.Condition, hoverLine, hoverColumn) ||
			expressionContainsHover(e.Consequent, hoverLine, hoverColumn) ||
			expressionContainsHover(e.Alternate, hoverLine, hoverColumn) {
			return true
		}
		for _, branch := range e.ElseIf {
			if expressionContainsHover(branch.Condition, hoverLine, hoverColumn) ||
				expressionContainsHover(branch.Result, hoverLine, hoverColumn) {
				return true
			}
		}
	case *ast.WhileStmt:
		if expressionContainsHover(e.Condition, hoverLine, hoverColumn) {
			return true
		}
		return lineInStatements(e.Body, nil, hoverLine, hoverColumn)
	case *ast.UntilStmt:
		if expressionContainsHover(e.Condition, hoverLine, hoverColumn) {
			return true
		}
		return lineInStatements(e.Body, nil, hoverLine, hoverColumn)
	case *ast.ForStmt:
		if expressionContainsHover(e.Target, hoverLine, hoverColumn) ||
			expressionContainsHover(e.Iterable, hoverLine, hoverColumn) {
			return true
		}
		return lineInStatements(e.Body, nil, hoverLine, hoverColumn)
	case *ast.CaseExpr:
		if expressionContainsHover(e.Target, hoverLine, hoverColumn) {
			return true
		}
		for _, clause := range e.Clauses {
			if expressionContainsHover(clause.Result, hoverLine, hoverColumn) {
				return true
			}
			for _, value := range clause.Values {
				if expressionContainsHover(value.Expr, hoverLine, hoverColumn) {
					return true
				}
			}
		}
		return expressionContainsHover(e.ElseExpr, hoverLine, hoverColumn)
	case *ast.Identifier:
		if e.Pos().Line != hoverLine {
			return false
		}
		if hoverColumn <= 0 {
			return true
		}
		start := e.Pos().Column
		return hoverColumn >= start && hoverColumn < start+utf8.RuneCountInString(e.Name)
	}
	return false
}

func lineInStatements(stmts []ast.Statement, lines []string, hoverLine, hoverColumn int) bool {
	if nestedControlContains(stmts, lines, hoverLine, hoverColumn) {
		return true
	}
	for _, stmt := range stmts {
		if stmt == nil {
			continue
		}
		if hoverInsideStatement(stmt, lines, hoverLine, hoverColumn) {
			return true
		}
	}
	return false
}

func hoverInsideStatement(stmt ast.Statement, lines []string, hoverLine, hoverColumn int) bool {
	start := stmt.Pos()
	endLine := statementLastLine(stmt)
	if hoverLine < start.Line || hoverLine > endLine {
		return false
	}
	if hoverLine == start.Line && hoverColumn > 0 && hoverColumn < start.Column {
		return false
	}
	if hoverLine == endLine && hoverColumn > 0 && len(lines) > 0 {
		line := lineAt(lines, hoverLine-1)
		from := 0
		if hoverLine == start.Line && start.Column > 0 {
			from = columnToByte(line, start.Column)
		}
		to := columnToByte(line, hoverColumn+1)
		if from < to {
			segment := line[from:to]
			if i := strings.LastIndex(segment, "end"); i >= 0 {
				rest := strings.TrimSpace(segment[i+3:])
				if rest == "" || strings.HasPrefix(rest, ";") || strings.HasPrefix(rest, "#") {
					return false
				}
			}
		}
	}
	return true
}

func rescueBindingCovers(clause ast.RescueClause, hoverLine, hoverColumn int) bool {
	start := clause.Position
	if hoverLine < start.Line || (hoverLine == start.Line && hoverColumn < start.Column) {
		return false
	}
	endLine := start.Line
	for _, stmt := range clause.Body {
		if line := statementLastLine(stmt); line > endLine {
			endLine = line
		}
	}
	return hoverLine <= endLine
}

func statementLastLine(stmt ast.Statement) int {
	if stmt == nil {
		return 0
	}
	last := stmt.Pos().Line
	switch s := stmt.(type) {
	case *ast.IfStmt:
		last = max(last, statementsLastLine(s.Consequent), statementsLastLine(s.Alternate))
		for _, branch := range s.ElseIf {
			last = max(last, statementLastLine(branch))
		}
	case *ast.ForStmt:
		last = max(last, statementsLastLine(s.Body))
	case *ast.WhileStmt:
		last = max(last, statementsLastLine(s.Body))
	case *ast.UntilStmt:
		last = max(last, statementsLastLine(s.Body))
	case *ast.TryStmt:
		last = max(last, statementsLastLine(s.Body), statementsLastLine(s.Else), statementsLastLine(s.Ensure))
		for _, clause := range s.Rescues {
			last = max(last, clause.Position.Line, statementsLastLine(clause.Body))
		}
	}
	return last
}

func statementsLastLine(stmts []ast.Statement) int {
	last := 0
	for _, stmt := range stmts {
		if line := statementLastLine(stmt); line > last {
			last = line
		}
	}
	return last
}

func assignmentBindsName(target ast.Expression, word string) bool {
	switch typed := target.(type) {
	case *ast.Identifier:
		return typed.Name == word
	case *ast.DestructureTarget:
		for _, el := range typed.Elements {
			if assignmentBindsName(el.Target, word) {
				return true
			}
		}
	}
	return false
}

// userSymbolCandidate is one declaration matching the hovered word.
// containerStart/containerEnd bound the class, module, or enum body the
// declaration lives in (1-based, inclusive); scoped is false for
// top-level declarations, whose container is the whole file.
type userSymbolKind int

const (
	userSymbolDecl userSymbolKind = iota
	userSymbolInstanceMethod
	userSymbolClassMethod
	userSymbolEnumMember
)

type userSymbolCandidate struct {
	markdown       string
	declLine       int
	containerStart int
	containerEnd   int
	scoped         bool
	owner          string
	kind           userSymbolKind
}

// userSymbolDoc resolves word against every matching declaration in the
// document, preferring the one the hover position points at: the
// declaration on the hovered line first, then a declaration whose
// enclosing class/module/enum body contains the hover position (the
// latest-starting such container when they nest), then the nearest
// declaration above the position, so duplicate names across classes
// resolve to the copy in scope. hoverLine is 1-based.
func userSymbolDoc(program *ast.Program, lines []string, word string, hoverLine int, qualifier string, memberShaped bool) string {
	candidates := collectUserSymbols(program, lines, word)
	if qualifier != "" {
		owned := make([]userSymbolCandidate, 0, len(candidates))
		for _, c := range candidates {
			if c.owner == qualifier {
				owned = append(owned, c)
			}
		}
		if len(owned) > 0 {
			// The receiver names the owner itself (Client.save,
			// First::Draft), so class methods and enum members outrank
			// instance methods, which that receiver cannot dispatch.
			direct := make([]userSymbolCandidate, 0, len(owned))
			for _, c := range owned {
				if c.kind == userSymbolClassMethod || c.kind == userSymbolEnumMember || c.kind == userSymbolDecl {
					direct = append(direct, c)
				}
			}
			// A class-named receiver dispatches only class methods and
			// enum members; with none, the qualified hover has no valid
			// target rather than the owner's instance methods.
			candidates = direct
		} else {
			// A qualified access can never resolve to a top-level
			// declaration, and an instance receiver cannot dispatch
			// class methods or enum members; only instance methods (the
			// receiver may be an instance of their class) stay eligible.
			instance := make([]userSymbolCandidate, 0, len(candidates))
			for _, c := range candidates {
				if c.scoped && c.kind == userSymbolInstanceMethod {
					instance = append(instance, c)
				}
			}
			candidates = instance
		}
	}
	if len(candidates) == 0 {
		return ""
	}
	for _, c := range candidates {
		if c.declLine == hoverLine {
			return c.markdown
		}
	}
	best := -1
	for i, c := range candidates {
		if !c.scoped || hoverLine < c.containerStart || hoverLine > c.containerEnd {
			continue
		}
		if best == -1 || c.containerStart > candidates[best].containerStart {
			best = i
		}
	}
	if best != -1 {
		return candidates[best].markdown
	}
	// Outside every container, top-level declarations outrank methods
	// scoped to some class the position is not in: code in the gap after
	// a class body must not resolve to that class's methods.
	unscoped := make([]userSymbolCandidate, 0, len(candidates))
	for _, c := range candidates {
		if !c.scoped {
			unscoped = append(unscoped, c)
		}
	}
	pool := unscoped
	if len(pool) == 0 {
		// Outside every container, a bare word cannot reach scoped
		// members; only a member-shaped access (obj.x, self.x, X::y) may
		// still resolve them.
		if qualifier == "" && !memberShaped {
			return ""
		}
		pool = candidates
	}
	for i := len(pool) - 1; i >= 0; i-- {
		if pool[i].declLine <= hoverLine {
			return pool[i].markdown
		}
	}
	return pool[0].markdown
}

func collectUserSymbols(program *ast.Program, lines []string, word string) []userSymbolCandidate {
	var out []userSymbolCandidate
	fileEnd := len(lines) + 1
	for i, stmt := range program.Statements {
		nextStart := fileEnd
		if i+1 < len(program.Statements) {
			if pos := program.Statements[i+1].Pos(); pos.Line > 0 {
				nextStart = pos.Line
			}
		}
		switch st := stmt.(type) {
		case *ast.FunctionStmt:
			if st.Name == word {
				out = append(out, userSymbolCandidate{
					markdown: userSymbolMarkdown(lines, userDefSignature(st, false), st.Position, st.Name),
					declLine: st.Position.Line,
				})
			}
		case *ast.ClassStmt:
			out = append(out, collectClassSymbols(st, lines, word, st.Position.Line, nextStart-1, "")...)
		case *ast.EnumStmt:
			if st.Name == word {
				out = append(out, userSymbolCandidate{
					markdown: userSymbolMarkdown(lines, "enum "+st.Name, st.Position, st.Name),
					declLine: st.Position.Line,
				})
			}
			for _, member := range st.Members {
				if member.Name == word {
					out = append(out, userSymbolCandidate{
						markdown:       userSymbolMarkdown(lines, st.Name+"::"+member.Name, member.Position, member.Name),
						declLine:       member.Position.Line,
						containerStart: st.Position.Line,
						containerEnd:   nextStart - 1,
						scoped:         true,
						owner:          st.Name,
						kind:           userSymbolEnumMember,
					})
				}
			}
		}
	}
	return out
}

// collectClassSymbols gathers matches within one class or module
// declaration: the declaration itself, its instance and self. methods,
// and nested modules, recursively. Nested containers inherit the
// parent's end bound since statement positions only carry starts.
func collectClassSymbols(st *ast.ClassStmt, lines []string, word string, start, end int, parentOwner string) []userSymbolCandidate {
	var out []userSymbolCandidate
	if st.Name == word {
		keyword := "class"
		if st.IsModule {
			keyword = "module"
		}
		// A nested declaration records its parent as owner so qualified
		// references (Outer::Inner) survive the qualifier filter.
		out = append(out, userSymbolCandidate{
			markdown: userSymbolMarkdown(lines, keyword+" "+st.Name, st.Position, st.Name),
			declLine: st.Position.Line,
			owner:    parentOwner,
		})
	}
	for _, method := range st.Methods {
		if method.Name == word {
			out = append(out, userSymbolCandidate{
				markdown:       userSymbolMarkdown(lines, userDefSignature(method, false), method.Position, method.Name),
				declLine:       method.Position.Line,
				containerStart: start,
				containerEnd:   end,
				scoped:         true,
				owner:          st.Name,
				kind:           userSymbolInstanceMethod,
			})
		}
	}
	for _, method := range st.ClassMethods {
		if method.Name == word {
			out = append(out, userSymbolCandidate{
				markdown:       userSymbolMarkdown(lines, userDefSignature(method, true), method.Position, method.Name),
				declLine:       method.Position.Line,
				containerStart: start,
				containerEnd:   end,
				scoped:         true,
				owner:          st.Name,
				kind:           userSymbolClassMethod,
			})
		}
	}
	// A nested module's scope ends at the next sibling declaration, so a
	// method defined after it in the parent body is never attributed to
	// the nested module.
	siblingStarts := classChildStarts(st)
	for _, nested := range st.Modules {
		nestedEnd := end
		for _, sibling := range siblingStarts {
			if sibling > nested.Position.Line && sibling-1 < nestedEnd {
				nestedEnd = sibling - 1
			}
		}
		out = append(out, collectClassSymbols(nested, lines, word, nested.Position.Line, nestedEnd, st.Name)...)
	}
	return out
}

// classChildStarts returns the start lines of every direct child of a
// class or module — declarations and plain body statements alike — so
// a nested module's scope ends at whatever follows it in the parent.
func classChildStarts(st *ast.ClassStmt) []int {
	var starts []int
	for _, method := range st.Methods {
		starts = append(starts, method.Position.Line)
	}
	for _, method := range st.ClassMethods {
		starts = append(starts, method.Position.Line)
	}
	for _, nested := range st.Modules {
		starts = append(starts, nested.Position.Line)
	}
	for _, alias := range st.Aliases {
		starts = append(starts, alias.Position.Line)
	}
	for _, prop := range st.Properties {
		if prop.Position.Line > 0 {
			starts = append(starts, prop.Position.Line)
		}
	}
	for _, stmt := range st.Body {
		if pos := stmt.Pos(); pos.Line > 0 {
			starts = append(starts, pos.Line)
		}
	}
	return starts
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
