package parser

import (
	"slices"
	"sync/atomic"
	"unicode/utf8"

	"github.com/mgomes/vibescript/internal/ast"
)

// sourceReplayCounting and sourceReplayBytes let a test count the input bytes a
// parse re-walks to look back at source the lexer has already scanned past,
// which is the work the complexity claim is about. Wall-clock would fold in
// scheduling and the race and coverage instrumentation this repository runs
// across three operating systems. Never set outside tests; when off this costs
// one relaxed load per re-walk.
var (
	sourceReplayCounting atomic.Bool
	sourceReplayBytes    atomic.Uint64
)

func noteSourceReplay(walked int) {
	if sourceReplayCounting.Load() {
		sourceReplayBytes.Add(uint64(walked))
	}
}

// sourceReplay is the scratch one parse builds to look back at source its lexer
// has already scanned past. The parser re-reads a construct directly from the
// input whenever the lexer tokenized it under the wrong reading -- a
// command-argument regex the lexer took for division, a percent-array literal
// it took for modulo -- and each of those needs the byte offset a token
// position names and the lexer state that held there.
//
// Both used to start over at byte 0 on every request: resolving a position
// walked the input counting lines, and rebuilding lexer state re-lexed
// everything before the offset. Since a "/" following a callee that is not a
// declared local reaches the first of those, a file of ordinary divisions cost
// the square of its size to parse, all of it before any script step quota can
// meter it (#21). 20,000 `f / 2` lines took 1.26s and 5,000 `f /a/` lines 3.2s;
// both are now under 15ms.
//
// One replay is shared by every lexer over the same input. It is deliberately
// not rolled back by parser.restore, for the same reason the percent-scan
// allowance is not: it describes the source, which a discarded speculation did
// not change.
type sourceReplay struct {
	lines lineIndex

	// scan is the forward-only re-lex stateBefore answers from, parked at the
	// offset the last request stopped at.
	scan *lexer
}

// stateBefore returns the bracket depth, bracket stack, and pending ternaries
// that hold just before the first token starting at or after offset.
//
// Requests arrive in source order as the parse advances, so the re-lex stays
// where the previous request left it and resumes from there. Only a request
// pointing behind it -- after a speculative parse was rolled back, say --
// starts over, and the next request in source order resumes again.
func (r *sourceReplay) stateBefore(offset int, budget *percentScanBudget) (int, *frameStack[bracketFrame], *frameStack[ternaryFrame]) {
	if r.scan == nil || r.scan.currentOffset() > offset {
		r.scan = newLexerWithBudget(r.lines.input, budget)
	}
	resumed := r.scan.currentOffset()
	for r.scan.ch != 0 {
		if _, ok := r.scan.skipWhitespaceAndComments(); ok {
			continue
		}
		if r.scan.currentOffset() >= offset {
			break
		}
		if tok := r.scan.NextToken(); tok.Type == ast.TokenEOF {
			break
		}
	}
	noteSourceReplay(r.scan.currentOffset() - resumed)

	return r.scan.bracketDepth, r.scan.bracketStack, r.scan.ternaryStack
}

// lineIndex maps between byte offsets and the 1-indexed line/column positions
// tokens carry, over one source text and without walking it from byte 0.
//
// Columns count runes rather than bytes, so a line that is pure ASCII resolves
// by arithmetic while one holding a multi-byte rune needs a table of its rune
// starts. Building that table per line, on the first lookup that lands there,
// keeps one very long line -- the shape a whole-source ASCII test would leave
// quadratic -- as cheap as many short ones, and costs sources that never ask
// about such a line nothing at all.
//
// The line table grows the same way, a prefix at a time. Most of an index is
// read front to back and it makes no difference, but not every index covers
// source anyone reads all of: findStringInterpolationEnd drives a throwaway
// lexer over the whole rest of the source to close one `#{...}`, and that lexer
// only ever asks about offsets inside the interpolation. Scanning the entire
// suffix to answer made a percent-array literal inside an interpolation cost
// the source that follows it, so 2,000 lines of `s = "#{a %w[b]}"` walked 34 MB
// of a 34 KB source and quadrupled per doubling. They walk 74 KB now (#45).
type lineIndex struct {
	input string

	// starts holds the byte offset each line begins at, over the prefix
	// scanned so far. Input ending in a newline has a final empty line, which
	// is where the End of the last token and the one-past-the-input position
	// land.
	starts []int

	// ascii records, for each line the scan has passed the end of, whether it
	// is free of multi-byte runes. asciiRun carries the same answer for the
	// line the scan stopped inside, covering it as far as scanned, which is
	// all a column at or before that point can depend on.
	ascii    []bool
	asciiRun bool

	runes map[int]*runeTable

	// scanned is how far into the input the line table reaches. complete
	// records that it reached the end, at which point ascii holds an entry per
	// line and the last line's end is known.
	scanned  int
	complete bool
}

// extend grows the line table until it covers untilOffset bytes of input and
// has found the start of the line after untilLine, or until the input runs out.
// Zero leaves either bound already satisfied, so a caller states only the one it
// needs.
//
// Stopping at the start of the line after untilLine is what settles that line:
// its end and its ascii entry both exist only once the scan has passed its
// newline.
func (x *lineIndex) extend(untilOffset, untilLine int) {
	if x.starts == nil {
		x.starts = []int{0}
		x.asciiRun = true
	}

	from := x.scanned
	for !x.complete && (x.scanned < untilOffset || len(x.starts) <= untilLine) {
		if x.scanned == len(x.input) {
			x.complete = true
			x.ascii = append(x.ascii, x.asciiRun)
			break
		}
		i := x.scanned
		x.scanned++
		switch {
		case x.input[i] >= utf8.RuneSelf:
			x.asciiRun = false
		case x.input[i] == '\n':
			x.starts = append(x.starts, i+1)
			x.ascii = append(x.ascii, x.asciiRun)
			x.asciiRun = true
		}
	}
	noteSourceReplay(x.scanned - from)
}

// lineASCII reports whether the given line is free of multi-byte runes. A line
// the scan has not passed the end of yet answers for the part of it covered so
// far, which is why every caller extends past the offset it is asking about
// before asking.
func (x *lineIndex) lineASCII(line int) bool {
	if line-1 < len(x.ascii) {
		return x.ascii[line-1]
	}
	return x.asciiRun
}

// offsetForPosition returns the byte offset of the rune at pos, or false when
// the source has no such position. The position one past the final rune
// resolves to the length of the input, which is what a token ending at the end
// of the input carries as its End.
func (x *lineIndex) offsetForPosition(pos ast.Position) (int, bool) {
	if pos.Line < 1 || pos.Column < 1 {
		return 0, false
	}
	x.extend(0, pos.Line)
	if pos.Line > len(x.starts) {
		return 0, false
	}

	start, end := x.lineBounds(pos.Line)
	index := pos.Column - 1
	lastLine := pos.Line == len(x.starts)
	if !x.lineASCII(pos.Line) {
		runes := x.runeStarts(pos.Line, end)
		if index < len(runes) {
			return runes[index], true
		}
		if index == len(runes) && lastLine {
			return end, true
		}
		return 0, false
	}
	if offset := start + index; offset < end || (offset == end && lastLine) {
		return offset, true
	}
	return 0, false
}

// runeAt snaps offset forward to the start of the rune it points at or into and
// reports both that offset and its position. An offset at or past the end of
// the input reports the one-past-the-final-rune position.
func (x *lineIndex) runeAt(offset int) (int, ast.Position) {
	offset = max(offset, 0)
	offset = min(offset, len(x.input))
	x.extend(offset, 0)

	index, found := slices.BinarySearch(x.starts, offset)
	if !found {
		index--
	}
	line := index + 1

	start, _ := x.lineBounds(line)
	if x.lineASCII(line) {
		return offset, ast.Position{Line: line, Column: offset - start + 1}
	}
	runes := x.runeStarts(line, offset)
	column, _ := slices.BinarySearch(runes, offset)
	if column < len(runes) {
		return runes[column], ast.Position{Line: line, Column: column + 1}
	}
	return len(x.input), ast.Position{Line: line, Column: len(runes) + 1}
}

// lineBounds returns the byte range of the given line. The end of the last
// known line reads as the end of the input, so a caller that needs a real end
// must have extended past the line first; one that only wants the start (which
// the table always holds) need not.
func (x *lineIndex) lineBounds(line int) (int, int) {
	if line < len(x.starts) {
		return x.starts[line-1], x.starts[line]
	}
	return x.starts[line-1], len(x.input)
}

// runeTable holds the byte offset of each rune on one line, over the prefix of
// that line tabulated so far, and records once that prefix is all of it.
type runeTable struct {
	starts   []int
	scanned  int
	complete bool
}

// runeStarts tabulates the runes on the given line as far as untilOffset, keeps
// what it built for later lookups, and returns the table. Only a line holding a
// multi-byte rune needs one: on an ASCII line the column is the byte distance.
//
// It grows a prefix at a time for the same reason the line table does, and the
// one very long line is where that matters twice over: an interpolation on such
// a line hands its throwaway lexer an index whose first line runs to the end of
// the source, so tabulating the line to answer one offset near its start put the
// same quadratic back that the line table alone had already removed -- 2,000
// `#{a %w[é]}` on one line walked 88 MB, against 148 KB now (#45). Asking as far
// as the end of the line asks for all of it, which is what resolving a column
// needs.
func (x *lineIndex) runeStarts(line, untilOffset int) []int {
	table, ok := x.runes[line]
	if !ok {
		table = &runeTable{scanned: x.starts[line-1]}
		if x.runes == nil {
			x.runes = make(map[int]*runeTable)
		}
		x.runes[line] = table
	}

	from := table.scanned
	for !table.complete && (len(table.starts) == 0 || table.starts[len(table.starts)-1] < untilOffset) {
		// One byte past the rune about to be read is enough for lineBounds to
		// have the real end of the line as soon as the scan crosses its
		// newline, which is what stops this.
		x.extend(table.scanned+1, 0)
		if _, end := x.lineBounds(line); table.scanned >= end {
			table.complete = true
			break
		}
		_, width := utf8.DecodeRuneInString(x.input[table.scanned:])
		table.starts = append(table.starts, table.scanned)
		table.scanned += width
	}
	noteSourceReplay(table.scanned - from)

	return table.starts
}

// offsetForPosition returns the byte offset of the rune at pos and whether the
// source being lexed has such a position.
func (l *lexer) offsetForPosition(pos ast.Position) (int, bool) {
	return l.replay.lines.offsetForPosition(pos)
}

// positionForOffset returns the position of the first rune starting at or after
// offset, which is the one-past-the-final-rune position once offset reaches the
// end of the source.
func (l *lexer) positionForOffset(offset int) ast.Position {
	_, pos := l.replay.lines.runeAt(offset)
	return pos
}
