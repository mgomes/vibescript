package parser

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mgomes/vibescript/internal/ast"
)

func (p *parser) parseExpression(precedence int) ast.Expression {
	if p.lineLimitedExprs > 0 {
		return p.parseExpressionWithLineLimit(precedence, p.curToken.Pos.Line, true)
	}
	return p.parseExpressionWithLineLimit(precedence, 0, false)
}

func (p *parser) parseLineExpression(precedence int) ast.Expression {
	return p.parseLineExpressionUntil(precedence)
}

func (p *parser) parseLineExpressionUntil(precedence int, stop ...ast.TokenType) ast.Expression {
	p.lineLimitedExprs++
	stopLen := len(p.lineLimitedStops)
	p.lineLimitedStops = append(p.lineLimitedStops, stop...)
	defer func() {
		p.lineLimitedExprs--
		p.lineLimitedStops = p.lineLimitedStops[:stopLen]
	}()
	return p.parseExpression(precedence)
}

func (p *parser) parseExpressionWithLineLimit(precedence, limitLine int, lineLimited bool) ast.Expression {
	prefix := prefixParserKind(p.curToken.Type)
	if prefix == prefixParserNone {
		if p.curToken.Type == ast.TokenRange || p.curToken.Type == ast.TokenRangeExcl {
			p.addParseErrorSpan(p.curToken.Pos, tokenEnd(p.curToken), "range is missing start expression")
			return nil
		}
		if p.curToken.Type == ast.TokenSlash {
			p.recoverUnsupportedRegexLiteral()
			return nil
		}
		p.errorUnexpected(p.curToken)
		return nil
	}

	left := p.parsePrefix(prefix)
	if left == nil {
		return nil
	}

	return p.continueExpressionParse(left, precedence, limitLine, lineLimited)
}

// continueExpressionParse applies infix and postfix parselets to an already
// parsed left-hand expression, following precedence and line-limit rules. It
// is the shared continuation used both after parsing a prefix and after the
// parser materializes an operand directly (such as a percent-array call
// argument) that must still accept trailing postfixes like `[i]` or `.member`.
func (p *parser) continueExpressionParse(left ast.Expression, precedence, limitLine int, lineLimited bool) ast.Expression {
	for p.peekToken.Type != ast.TokenEOF {
		if lineLimited && p.peekStopsLineExpression() {
			return left
		}

		if lineLimited && p.peekToken.Type == ast.TokenSemicolon {
			return left
		}

		if lineLimited && p.peekToken.Pos.Line > limitLine && !p.lineLimitedContinuationToken(p.peekToken) {
			return left
		}

		if p.canParseParenlessCall(left, precedence, lineLimited) {
			left = p.parseParenlessCallExpression(left)
			if left == nil {
				return nil
			}
			if lineLimited {
				limitLine = p.curToken.Pos.Line
			}
			continue
		}

		if precedence >= p.peekPrecedence() {
			return left
		}
		infix := infixParserKind(p.peekToken.Type)
		if infix == infixParserNone {
			return left
		}
		p.nextToken()
		left = p.parseInfix(infix, left)
		if left == nil {
			return nil
		}
		if lineLimited {
			limitLine = p.curToken.Pos.Line
		}
	}

	return left
}

func (p *parser) peekStopsLineExpression() bool {
	if p.peekToken.Pos.Line != p.curToken.Pos.Line {
		return false
	}
	for _, stop := range p.lineLimitedStops {
		if p.peekToken.Type == stop {
			if stop == ast.TokenDo && p.peekPeek.Type == ast.TokenPipe {
				return false
			}
			return true
		}
	}
	return false
}

func (p *parser) canParseParenlessCall(left ast.Expression, precedence int, lineLimited bool) bool {
	if !lineLimited || precedence != lowestPrec {
		return false
	}
	if !isParenlessCallCallee(left) {
		return false
	}
	if p.peekToken.Pos.Line != p.curToken.Pos.Line {
		return false
	}
	if p.peekStartsPercentArrayArgument(left) {
		return true
	}
	if p.peekStartsParenlessKeywordLabel() {
		return true
	}
	if p.peekToken.Type == ast.TokenAmpersand {
		// "&" is both the binary intersection operator and the (unsupported)
		// block-pass / symbol-to-proc sigil. Ruby disambiguates by spacing:
		// "foo &bar" passes a block while "foo & bar", "foo&bar", and a
		// trailing "&" line continuation are all the binary operator. Only the
		// block-pass shape starts a parenless argument here so the helpful
		// block-pass diagnostic still fires; the operator shapes fall through
		// to the infix path.
		return p.peekAmpersandStartsBlockPass()
	}
	return isParenlessArgumentStart(p.peekToken.Type)
}

// peekAmpersandStartsBlockPass reports whether the lookahead "&" has the
// spacing Ruby reads as a block-pass argument ("foo &bar") rather than the
// binary intersection operator. Ruby disambiguates purely by spacing: the
// block-pass shape requires the "&" to be separated from the callee yet flush
// against its operand. Concretely both of these must hold:
//
//   - The "&" is detached from the callee. "foo &bar" is a block-pass, while
//     "foo&bar" (flush on both sides) is the binary operator, so a "&" that
//     abuts the callee on the same line is never a block-pass.
//   - The operand is flush against the "&" on the same line. "foo & bar" is the
//     binary operator, and a trailing "&" that ends the line is the intersection
//     line-continuation operator (see lineLimitedContinuationToken); neither is
//     a block-pass.
func (p *parser) peekAmpersandStartsBlockPass() bool {
	if p.peekToken.Type != ast.TokenAmpersand {
		return false
	}
	calleeFlush := p.peekToken.Pos.Line == p.curToken.End.Line &&
		p.peekToken.Pos.Column == p.curToken.End.Column
	if calleeFlush {
		return false
	}
	operandFlush := p.peekPeek.Pos.Line == p.peekToken.Pos.Line &&
		p.peekPeek.Pos.Column == p.peekToken.End.Column
	return operandFlush
}

// peekStartsParenlessKeywordLabel reports whether the lookahead begins a
// keyword-argument label (`name:`) for a parenless call. Reserved keywords
// such as `rescue` are valid only as labels here, so they are not accepted
// by isParenlessArgumentStart; recognizing the `label:` shape lets forms
// like `record rescue: 1` start a parenless call. The trailing colon is the
// disambiguator, mirroring Ruby, where `record rescue 1` is the rescue
// modifier while `record rescue: 1` is a keyword argument.
func (p *parser) peekStartsParenlessKeywordLabel() bool {
	return isLabelNameToken(p.peekToken) && p.peekPeek.Type == ast.TokenColon
}

func isParenlessCallCallee(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.Identifier, *ast.MemberExpr:
		return true
	default:
		return false
	}
}

func (p *parser) peekStartsPercentArrayArgument(callee ast.Expression) bool {
	if p.peekToken.Type != ast.TokenPercent || !p.percentArrayLiteralArgumentAt(p.peekToken.Pos) {
		return false
	}
	if ident, ok := callee.(*ast.Identifier); ok && p.isLocalName(ident.Name) {
		return false
	}
	return true
}

func isParenlessArgumentStart(tt ast.TokenType) bool {
	switch tt {
	case ast.TokenLParen, ast.TokenLBracket, ast.TokenLBrace, ast.TokenMinus, ast.TokenPlus:
		return false
	case ast.TokenAmpersand:
		return true
	}
	return prefixParserKind(tt) != prefixParserNone
}

func (p *parser) percentArrayLiteralArgumentAt(pos ast.Position) bool {
	offset, ok := sourceOffsetForPosition(p.l.input, pos)
	if !ok || !offsetHasLeadingWhitespace(p.l.input, offset) {
		return false
	}
	_, _, _, ok = scanPercentArrayLiteralAt(p.l.input, offset)
	return ok
}

func (p *parser) recoverUnsupportedRegexLiteral() {
	startLine := p.curToken.Pos.Line
	closingSlash := p.findUnsupportedRegexLiteralClose()
	spanEnd := tokenEnd(p.curToken)
	if closingSlash != (ast.Position{}) {
		spanEnd = ast.Position{Line: closingSlash.Line, Column: closingSlash.Column + 1}
	}
	p.addParseErrorSpan(
		p.curToken.Pos,
		spanEnd,
		"regex literals are not supported; use quoted string patterns with Regex.match or string regex helpers",
	)
	if closingSlash != (ast.Position{}) {
		p.recoverToPosition(closingSlash)
		return
	}
	for p.peekToken.Type != ast.TokenEOF && p.peekToken.Pos.Line == startLine {
		p.nextToken()
	}
}

func (p *parser) findUnsupportedRegexLiteralClose() ast.Position {
	start := p.curToken.Pos
	if start.Line <= 0 || start.Column <= 0 {
		return ast.Position{}
	}
	lines := strings.Split(p.l.input, "\n")
	if start.Line > len(lines) {
		return ast.Position{}
	}
	lineRunes := []rune(lines[start.Line-1])
	if start.Column > len(lineRunes) || lineRunes[start.Column-1] != '/' {
		return ast.Position{}
	}

	escaped := false
	inCharClass := false
	closingColumn := 0
	for i := start.Column; i < len(lineRunes); i++ {
		r := lineRunes[i]
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '[' && !inCharClass {
			inCharClass = true
			continue
		}
		if r == ']' && inCharClass {
			inCharClass = false
			continue
		}
		if r == '/' && !inCharClass {
			closingColumn = i + 1
			break
		}
	}
	if closingColumn == 0 {
		return ast.Position{}
	}
	endColumn := closingColumn
	for i := closingColumn; i < len(lineRunes); i++ {
		if !isRegexFlagRune(lineRunes[i]) {
			break
		}
		endColumn = i + 1
	}
	return ast.Position{Line: start.Line, Column: endColumn}
}

func isRegexFlagRune(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
}

func (p *parser) recoverToPosition(pos ast.Position) {
	for p.peekToken.Type != ast.TokenEOF {
		if p.peekToken.Pos.Line > pos.Line || (p.peekToken.Pos.Line == pos.Line && p.peekToken.Pos.Column > pos.Column) {
			return
		}
		p.nextToken()
		if p.curToken.Pos == pos {
			return
		}
	}
}

type prefixParseKind uint8

const (
	prefixParserNone prefixParseKind = iota
	prefixParserIdentifier
	prefixParserIntegerLiteral
	prefixParserFloatLiteral
	prefixParserStringLiteral
	prefixParserInterpolatedStringLiteral
	prefixParserPercentWordsLiteral
	prefixParserPercentSymbolsLiteral
	prefixParserPercentInterpWordsLiteral
	prefixParserPercentInterpSymbolsLiteral
	prefixParserBooleanLiteral
	prefixParserNilLiteral
	prefixParserSymbolLiteral
	prefixParserIvarLiteral
	prefixParserClassVarLiteral
	prefixParserSelfLiteral
	prefixParserGroupedExpression
	prefixParserArrayLiteral
	prefixParserHashLiteral
	prefixParserPrefixExpression
	prefixParserYieldExpression
	prefixParserIfExpression
	prefixParserCaseExpression
)

func prefixParserKind(tt ast.TokenType) prefixParseKind {
	switch tt {
	case ast.TokenIdent:
		return prefixParserIdentifier
	case ast.TokenInt:
		return prefixParserIntegerLiteral
	case ast.TokenFloat:
		return prefixParserFloatLiteral
	case ast.TokenString:
		return prefixParserStringLiteral
	case ast.TokenInterpolatedString:
		return prefixParserInterpolatedStringLiteral
	case ast.TokenWords:
		return prefixParserPercentWordsLiteral
	case ast.TokenSymbols:
		return prefixParserPercentSymbolsLiteral
	case ast.TokenInterpWords:
		return prefixParserPercentInterpWordsLiteral
	case ast.TokenInterpSymbols:
		return prefixParserPercentInterpSymbolsLiteral
	case ast.TokenTrue, ast.TokenFalse:
		return prefixParserBooleanLiteral
	case ast.TokenNil:
		return prefixParserNilLiteral
	case ast.TokenSymbol:
		return prefixParserSymbolLiteral
	case ast.TokenIvar:
		return prefixParserIvarLiteral
	case ast.TokenClassVar:
		return prefixParserClassVarLiteral
	case ast.TokenSelf:
		return prefixParserSelfLiteral
	case ast.TokenLParen:
		return prefixParserGroupedExpression
	case ast.TokenLBracket:
		return prefixParserArrayLiteral
	case ast.TokenLBrace:
		return prefixParserHashLiteral
	case ast.TokenBang, ast.TokenNot, ast.TokenMinus, ast.TokenPlus:
		return prefixParserPrefixExpression
	case ast.TokenYield:
		return prefixParserYieldExpression
	case ast.TokenIf:
		return prefixParserIfExpression
	case ast.TokenCase:
		return prefixParserCaseExpression
	default:
		return prefixParserNone
	}
}

func (p *parser) parsePrefix(kind prefixParseKind) ast.Expression {
	switch kind {
	case prefixParserIdentifier:
		return p.parseIdentifier()
	case prefixParserIntegerLiteral:
		return p.parseIntegerLiteral()
	case prefixParserFloatLiteral:
		return p.parseFloatLiteral()
	case prefixParserStringLiteral:
		return p.parseStringLiteral()
	case prefixParserInterpolatedStringLiteral:
		return p.parseInterpolatedStringLiteral()
	case prefixParserPercentWordsLiteral:
		return p.parsePercentWordsLiteral()
	case prefixParserPercentSymbolsLiteral:
		return p.parsePercentSymbolsLiteral()
	case prefixParserPercentInterpWordsLiteral:
		return p.parsePercentInterpWordsLiteral()
	case prefixParserPercentInterpSymbolsLiteral:
		return p.parsePercentInterpSymbolsLiteral()
	case prefixParserBooleanLiteral:
		return p.parseBooleanLiteral()
	case prefixParserNilLiteral:
		return p.parseNilLiteral()
	case prefixParserSymbolLiteral:
		return p.parseSymbolLiteral()
	case prefixParserIvarLiteral:
		return p.parseIvarLiteral()
	case prefixParserClassVarLiteral:
		return p.parseClassVarLiteral()
	case prefixParserSelfLiteral:
		return p.parseSelfLiteral()
	case prefixParserGroupedExpression:
		return p.parseGroupedExpression()
	case prefixParserArrayLiteral:
		return p.parseArrayLiteral()
	case prefixParserHashLiteral:
		return p.parseHashLiteral()
	case prefixParserPrefixExpression:
		return p.parsePrefixExpression()
	case prefixParserYieldExpression:
		return p.parseYieldExpression()
	case prefixParserIfExpression:
		return p.parseIfExpression()
	case prefixParserCaseExpression:
		return p.parseCaseExpression()
	default:
		return nil
	}
}

type infixParseKind uint8

const (
	infixParserNone infixParseKind = iota
	infixParserInfixExpression
	infixParserConditionalExpression
	infixParserRangeExpression
	infixParserCallExpression
	infixParserMemberExpression
	infixParserScopeExpression
	infixParserIndexExpression
	infixParserTrailingBlockExpression
)

func infixParserKind(tt ast.TokenType) infixParseKind {
	switch tt {
	case ast.TokenPlus, ast.TokenMinus, ast.TokenSlash, ast.TokenAsterisk, ast.TokenPower, ast.TokenPercent,
		ast.TokenEQ, ast.TokenCaseEQ, ast.TokenNotEQ, ast.TokenLT, ast.TokenLTE, ast.TokenGT, ast.TokenGTE,
		ast.TokenSpaceship, ast.TokenAnd, ast.TokenOr, ast.TokenShovel, ast.TokenAmpersand:
		return infixParserInfixExpression
	case ast.TokenQuestion:
		return infixParserConditionalExpression
	case ast.TokenRange, ast.TokenRangeExcl:
		return infixParserRangeExpression
	case ast.TokenLParen:
		return infixParserCallExpression
	case ast.TokenDot, ast.TokenSafeNav:
		return infixParserMemberExpression
	case ast.TokenScope:
		return infixParserScopeExpression
	case ast.TokenLBracket:
		return infixParserIndexExpression
	case ast.TokenDo, ast.TokenLBrace:
		return infixParserTrailingBlockExpression
	default:
		return infixParserNone
	}
}

func (p *parser) parseInfix(kind infixParseKind, left ast.Expression) ast.Expression {
	switch kind {
	case infixParserInfixExpression:
		return p.parseInfixExpression(left)
	case infixParserConditionalExpression:
		return p.parseConditionalExpression(left)
	case infixParserRangeExpression:
		return p.parseRangeExpression(left)
	case infixParserCallExpression:
		return p.parseCallExpression(left)
	case infixParserMemberExpression:
		return p.parseMemberExpression(left)
	case infixParserScopeExpression:
		return p.parseScopeExpression(left)
	case infixParserIndexExpression:
		return p.parseIndexExpression(left)
	case infixParserTrailingBlockExpression:
		return p.parseTrailingBlockExpression(left)
	default:
		return nil
	}
}

func (p *parser) lineLimitedContinuationToken(tok ast.Token) bool {
	switch tok.Type {
	case ast.TokenDot, ast.TokenSafeNav, ast.TokenScope, ast.TokenSlash, ast.TokenPower, ast.TokenPercent, ast.TokenRange, ast.TokenRangeExcl, ast.TokenEQ, ast.TokenCaseEQ, ast.TokenNotEQ, ast.TokenLT, ast.TokenLTE, ast.TokenGT, ast.TokenGTE, ast.TokenSpaceship, ast.TokenAnd, ast.TokenOr, ast.TokenQuestion, ast.TokenShovel, ast.TokenAmpersand:
		return true
	case ast.TokenAsterisk:
		// A line that begins with "*" continues the previous expression as a
		// multiplication, unless it opens a destructuring-assignment target
		// list (such as "*, last = vals" or "*rest, last = vals"). In that
		// case the statement boundary wins so the "*" parses as an anonymous
		// or named rest target rather than a multiplication operator.
		return !p.lineStartsSplatAssignment(tok)
	case ast.TokenPlus, ast.TokenMinus:
		return p.signContinuesLine(tok)
	default:
		return false
	}
}

// lineStartsSplatAssignment reports whether the leading "*" token begins a
// destructuring-assignment target list rather than continuing the previous
// line as a multiplication. It scans ahead with a throwaway lexer (leaving
// the parser's own lookahead untouched) and accepts only the tokens that may
// form a destructuring left-hand side at the top level, requiring a top-level
// "=" to terminate the list.
//
// A target list may span several physical lines at exactly the points the
// real parser continues a statement across a newline:
//   - inside an open bracket/paren group, where a nested sub-target spans the
//     newline ("*rest, (a,\n b) = values");
//   - after a trailing top-level "," , where the list continues with another
//     element ("*rest,\n last = values"); and
//   - via Vibescript's newline-before-"=" continuation (the same rule that
//     lets "x\n  = 1" parse as an assignment), where the terminating "=" sits
//     on a later line.
//
// To keep the newline-before-"=" continuation from reviving the multiplication
// ambiguity, a bare "*operand" that crosses a newline only completes a splat
// assignment when the leading "*" is shaped like a splat target: bare
// (immediately followed by a terminator such as "," or "=") or flush against
// its operand ("*rest"). A spaced "*" ("* b") is a multiplication operator, so
// "x = a" / "* b" / "= c" stays a multiplication followed by a dangling "="
// rather than becoming "*b = c". Once a top-level "," has appeared the list is
// unambiguously a destructuring list (a comma cannot follow a multiplicand at
// the top level), so the splat-shaped guard no longer applies.
//
// Anything else - a multiplicand operand, a comparison, an arithmetic
// operator, or a token that starts a new line without a continuation point -
// means the "*" is an ordinary multiplication continuation, so it returns
// false.
func (p *parser) lineStartsSplatAssignment(star ast.Token) bool {
	offset, ok := sourceOffsetForPosition(p.l.input, star.Pos)
	if !ok {
		return false
	}

	scan := newLexer(p.l.input)
	scan.seek(offset, ast.Token{})

	tok := scan.NextToken()
	if tok.Type != ast.TokenAsterisk {
		return false
	}
	starEnd := tok.End

	first := true
	splatShaped := false
	sawTopLevelComma := false
	depth := 0
	prev := ast.Token{}
	prevLine := star.Pos.Line
	for {
		tok = scan.NextToken()
		if tok.Type == ast.TokenEOF {
			return false
		}
		if first {
			// The "*" is splat-shaped when it is bare (a terminator follows) or
			// flush against its operand, distinguishing the splat target "*rest"
			// from the multiplication operator "* b".
			splatShaped = isAnonymousRestTerminator(tok.Type) ||
				(tok.Pos.Line == starEnd.Line && tok.Pos.Column == starEnd.Column)
			first = false
		}
		// A token that starts a later physical line only stays part of the
		// target list when the list was mid-continuation: inside a bracket
		// group, after a trailing top-level comma, when a target splits a member
		// access across the newline ("record\n  .field = values"), or completing
		// the newline-before-"=" rule. Otherwise the leading "*" is a
		// multiplication continuation (such as "x = a" / "* b") and the lookahead
		// stops.
		if tok.Pos.Line > prevLine {
			switch {
			case depth > 0 || prev.Type == ast.TokenComma:
				// Bracket group or trailing-comma continuation: the list keeps
				// going, so fall through to the normal token handling below.
			case (splatShaped || sawTopLevelComma) && splitsMemberAccess(tok, prev):
				// The real target parser uses a line-limited expression, which
				// continues a member or scope access onto a line that begins
				// with "." or "::" ("record\n  .field"). Keep scanning so the
				// element completes instead of severing the list at the newline.
				// This only applies once the list is committed to a splat
				// assignment - a bare "*" target or a top-level comma. A spaced
				// "*" with no comma ("* obj\n  .field") stays a multiplication,
				// the same disambiguation the newline-before-"=" rule uses.
			case tok.Type == ast.TokenAssign && (sawTopLevelComma || splatShaped):
				return true
			default:
				return false
			}
		}
		if depth == 0 {
			if tok.Type == ast.TokenAssign {
				return true
			}
			if !splatAssignmentTopLevelToken(tok, prev) {
				return false
			}
			if tok.Type == ast.TokenComma {
				sawTopLevelComma = true
			}
		}
		switch tok.Type {
		case ast.TokenLParen, ast.TokenLBracket:
			depth++
		case ast.TokenRParen, ast.TokenRBracket:
			if depth > 0 {
				depth--
			}
		case ast.TokenSemicolon:
			return false
		}
		prev = tok
		prevLine = tok.Pos.Line
	}
}

// splitsMemberAccess reports whether tok begins a later physical line that
// continues the current destructuring target's member or scope access, as in
// "record\n  .field = values". The real target parser builds each element with
// a line-limited expression, and lineLimitedContinuationToken lets such an
// expression continue onto a line that starts with "." or "::". The lookahead
// honors the same rule so a split member target completes instead of severing
// the list at the newline (which would let the previous statement consume the
// leading "*" as multiplication). prev is the preceding depth-zero token: the
// continuation only applies when it can end a member-access receiver, so a
// leading "." or "::" with no operand before it is not mistaken for one.
func splitsMemberAccess(tok, prev ast.Token) bool {
	if tok.Type != ast.TokenDot && tok.Type != ast.TokenScope {
		return false
	}
	switch prev.Type {
	case ast.TokenIdent, ast.TokenIvar, ast.TokenClassVar, ast.TokenSelf,
		ast.TokenRParen, ast.TokenRBracket, ast.TokenEnum:
		return true
	default:
		// A member name (the token after a preceding ".") can itself be the
		// receiver of a further "." or "::", as in "a.b\n  .c = values".
		return prev.Type != ast.TokenDot && prev.Type != ast.TokenScope &&
			isMemberNameToken(prev)
	}
}

// splatAssignmentTopLevelToken reports whether tok may appear at the top
// level of a destructuring-assignment left-hand side, between the leading
// "*" and the terminating "=". prev is the preceding depth-zero token, used
// to recognize member names: after a "." the token is a method name and after
// "::" it is a scope name, so each may be any name the real member or scope
// parser accepts (including reserved-word labels such as "end" in
// "record.end = values"). Bracketed sub-targets are validated by depth
// tracking in lineStartsSplatAssignment, so this only governs depth-zero
// tokens. "self" is included because "self.member" and "self[index]" are
// valid assignment targets, so a target list may legitimately begin one of
// its elements with "self".
func splatAssignmentTopLevelToken(tok, prev ast.Token) bool {
	switch prev.Type {
	case ast.TokenDot:
		// parseMemberExpression accepts any member-name token after ".".
		return isMemberNameToken(tok)
	case ast.TokenScope:
		// parseScopeExpression accepts only identifiers and enum names after "::".
		return tok.Type == ast.TokenIdent || tok.Type == ast.TokenEnum
	}
	switch tok.Type {
	case ast.TokenIdent, ast.TokenIvar, ast.TokenClassVar, ast.TokenSelf, ast.TokenComma, ast.TokenAsterisk, ast.TokenDot, ast.TokenScope, ast.TokenLParen, ast.TokenRParen, ast.TokenLBracket, ast.TokenRBracket:
		return true
	default:
		return false
	}
}

// signContinuesLine reports whether a leading `+` or `-` continues the
// previous line as a binary operator rather than beginning a new unary
// expression. A sign immediately adjacent to its operand at the start of a
// fresh line (such as `-b` or `+b`) starts a new statement, while a sign with
// an intervening space or a line break before its operand continues the prior
// line. The flush case matches Ruby, which also treats `a\n-b` as a new `-b`
// statement. The spaced case (`a\n- b`) is Vibescript's indented-continuation
// rule and intentionally differs from Ruby, which would parse it as the two
// statements `a` and `- b` rather than as subtraction.
func (p *parser) signContinuesLine(tok ast.Token) bool {
	if p.peekPeek.Type == ast.TokenEOF {
		return false
	}
	if p.peekPeek.Pos.Line > tok.Pos.Line {
		return true
	}
	return p.peekPeek.Pos.Line == tok.Pos.Line && p.peekPeek.Pos.Column > tok.End.Column
}

func (p *parser) parseIdentifier() ast.Expression {
	return &ast.Identifier{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseIntegerLiteral() ast.Expression {
	value, err := parseIntegerToken(p.curToken.Literal)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid integer literal")
		return nil
	}
	return &ast.IntegerLiteral{Value: value, Position: p.curToken.Pos}
}

// parseIntegerToken converts a lexer-produced integer literal into its value.
// The lexer strips underscore separators and validates digit sets, so the
// only remaining work is choosing the radix from any Ruby base prefix. Plain
// decimal literals are parsed in base 10 so a leading zero stays decimal
// rather than being read as octal.
func parseIntegerToken(literal string) (int64, error) {
	if len(literal) >= 2 && literal[0] == '0' {
		switch literal[1] {
		case 'd', 'D':
			return strconv.ParseInt(literal[2:], 10, 64)
		case 'x', 'X', 'b', 'B', 'o', 'O':
			return strconv.ParseInt(literal, 0, 64)
		}
	}
	return strconv.ParseInt(literal, 10, 64)
}

func (p *parser) parseFloatLiteral() ast.Expression {
	value, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		// An out-of-range exponent overflows to +/-Infinity, which Ruby
		// accepts as a literal value rather than a syntax error. ParseFloat
		// still returns the correct signed infinity alongside ErrRange, so
		// only a genuine syntax error (ErrSyntax) is rejected here.
		if !errors.Is(err, strconv.ErrRange) {
			p.addParseError(p.curToken.Pos, "invalid float literal")
			return nil
		}
	}
	return &ast.FloatLiteral{Value: value, Position: p.curToken.Pos}
}

func (p *parser) parseStringLiteral() ast.Expression {
	return &ast.StringLiteral{Value: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseInterpolatedStringLiteral() ast.Expression {
	parts, ok := p.parseInterpolatedStringParts(p.curToken.Literal, p.curToken.Pos)
	if !ok {
		return nil
	}
	return &ast.InterpolatedString{Parts: parts, Position: p.curToken.Pos}
}

func (p *parser) parseInterpolatedStringParts(raw string, pos ast.Position) ([]ast.StringPart, bool) {
	parts := []ast.StringPart{}
	textStart := 0
	for i := 0; i < len(raw); {
		if raw[i] == '#' && i+1 < len(raw) && raw[i+1] == '{' && !interpolationMarkerEscaped(raw, i) {
			if textStart < i {
				parts = append(parts, ast.StringText{Text: decodeDoubleQuotedText(raw[textStart:i])})
			}
			exprStart := i + 2
			exprEnd, ok := findStringInterpolationEnd(raw, exprStart)
			if !ok {
				p.addParseError(pos, "unterminated string interpolation")
				return nil, false
			}
			exprRaw := strings.TrimSpace(raw[exprStart:exprEnd])
			if exprRaw == "" {
				p.addParseError(pos, "empty string interpolation")
				return nil, false
			}
			expr, ok := p.parseStringInterpolationExpression(exprRaw, pos)
			if !ok {
				return nil, false
			}
			parts = append(parts, ast.StringExpr{Expr: expr})
			i = exprEnd + 1
			textStart = i
			continue
		}
		i++
	}
	if textStart < len(raw) {
		parts = append(parts, ast.StringText{Text: decodeDoubleQuotedText(raw[textStart:])})
	}
	return parts, true
}

func interpolationMarkerEscaped(raw string, hash int) bool {
	backslashes := 0
	for i := hash - 1; i >= 0 && raw[i] == '\\'; i-- {
		backslashes++
	}
	return backslashes%2 == 1
}

// findStringInterpolationEnd locates the byte index of the "}" that closes the
// interpolation whose body begins at start (just past the opening "#{"). It
// drives the lexer over the body so that every construct the language
// understands—double- and single-quoted strings, nested "#{...}"
// interpolations, and percent-array literals such as %w/%i/%W/%I—is consumed as
// a single unit. This means a "}" that appears inside one of those constructs
// (for example %W[#{%w[}]}]) does not prematurely close the interpolation, and
// a bare "%" remains the modulo operator wherever the lexer would treat it as
// one. It returns false when the body is never closed before the end of raw.
func findStringInterpolationEnd(raw string, start int) (int, bool) {
	if start < 0 || start > len(raw) {
		return 0, false
	}
	lex := newLexer(raw[start:])

	braceDepth := 0
	bracketDepth := 0
	parenDepth := 0
	for {
		tok := lex.NextToken()
		switch tok.Type {
		case ast.TokenEOF, ast.TokenIllegal:
			return 0, false
		case ast.TokenPercent:
			percentOffset := start + lex.currentOffset() - 1
			kind, _, endOffset, ok := scanPercentArrayLiteralAt(raw, percentOffset)
			if ok && interpolationPercentArrayArgumentScanCanAdvance(raw, endOffset) {
				lex.seek(endOffset-start, ast.Token{Type: percentArrayLiteralTokenType(kind)})
			}
		case ast.TokenLParen:
			parenDepth++
		case ast.TokenRParen:
			if parenDepth > 0 {
				parenDepth--
			}
		case ast.TokenLBracket:
			bracketDepth++
		case ast.TokenRBracket:
			if bracketDepth > 0 {
				bracketDepth--
			}
		case ast.TokenLBrace:
			braceDepth++
		case ast.TokenRBrace:
			if braceDepth > 0 {
				braceDepth--
				continue
			}
			if bracketDepth == 0 && parenDepth == 0 {
				// The lexer has consumed the closing "}"; currentOffset now
				// points at the rune after it, so the "}" itself sits one byte
				// back ("}" is always a single byte).
				return start + lex.currentOffset() - 1, true
			}
		}
	}
}

func interpolationPercentArrayArgumentScanCanAdvance(raw string, endOffset int) bool {
	if endOffset >= len(raw) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(raw[endOffset:])
	return r != '"' && r != '\''
}

func percentArrayLiteralTokenType(kind rune) ast.TokenType {
	switch kind {
	case 'w':
		return ast.TokenWords
	case 'i':
		return ast.TokenSymbols
	case 'W':
		return ast.TokenInterpWords
	case 'I':
		return ast.TokenInterpSymbols
	default:
		return ast.TokenIllegal
	}
}

func (p *parser) parseStringInterpolationExpression(raw string, pos ast.Position) (ast.Expression, bool) {
	exprParser := newParser(raw)
	// Inherit the enclosing local scopes so name-sensitive parsing (such as
	// percent-literal vs modulo disambiguation) resolves locals the same way
	// inside #{...} as it would inline. The copy keeps the sub-parser's scope
	// stack independent while sharing the (read-only) name sets.
	exprParser.localScopes = append([]localScope(nil), p.localScopes...)
	expr := exprParser.parseLineExpression(lowestPrec)
	if len(exprParser.errors) > 0 {
		p.addParseError(pos, fmt.Sprintf("invalid string interpolation: %s", parseErrorMessage(exprParser.errors[0])))
		return nil, false
	}
	if expr == nil {
		p.addParseError(pos, "invalid string interpolation")
		return nil, false
	}
	if exprParser.peekToken.Type != ast.TokenEOF {
		p.addParseError(pos, "string interpolation must contain a single expression")
		return nil, false
	}
	return expr, true
}

func parseErrorMessage(err error) string {
	var parseErr *parseError
	if errors.As(err, &parseErr) {
		return parseErr.Message()
	}
	return err.Error()
}

func decodeDoubleQuotedText(raw string) string {
	var sb strings.Builder
	for i := 0; i < len(raw); {
		r, size := utf8.DecodeRuneInString(raw[i:])
		if r != '\\' {
			sb.WriteRune(r)
			i += size
			continue
		}
		i += size
		if i >= len(raw) {
			sb.WriteRune('\\')
			break
		}
		next, nextSize := utf8.DecodeRuneInString(raw[i:])
		switch next {
		case '"', '\\':
			sb.WriteRune(next)
		case 'n':
			sb.WriteByte('\n')
		case 't':
			sb.WriteByte('\t')
		default:
			sb.WriteRune(next)
		}
		i += nextSize
	}
	return sb.String()
}

func (p *parser) parsePercentWordsLiteral() ast.Expression {
	entries := decodePercentLiteralEntries(p.curToken.Literal)
	elements := make([]ast.Expression, len(entries))
	for i, entry := range entries {
		elements[i] = &ast.StringLiteral{Value: entry, Position: p.curToken.Pos}
	}
	return &ast.ArrayLiteral{Elements: elements, Position: p.curToken.Pos}
}

func (p *parser) parsePercentArrayLiteralArgument() ast.Expression {
	pos := p.curToken.Pos
	offset, ok := sourceOffsetForPosition(p.l.input, p.curToken.Pos)
	if !ok {
		return nil
	}
	kind, entries, endOffset, ok := scanPercentArrayLiteralAt(p.l.input, offset)
	if !ok {
		return nil
	}
	end := sourcePositionForOffset(p.l.input, endOffset)
	elements := make([]ast.Expression, len(entries))
	litType := ast.TokenWords
	for i, entry := range entries {
		switch kind {
		case 'w':
			elements[i] = &ast.StringLiteral{Value: entry, Position: pos}
		case 'i':
			elements[i] = &ast.SymbolLiteral{Name: entry, Position: pos}
			litType = ast.TokenSymbols
		case 'W':
			element, ok := p.interpolatedWordElement(entry, pos)
			if !ok {
				return nil
			}
			elements[i] = element
			litType = ast.TokenInterpWords
		case 'I':
			element, ok := p.interpolatedSymbolElement(entry, pos)
			if !ok {
				return nil
			}
			elements[i] = element
			litType = ast.TokenInterpSymbols
		}
	}
	// The lexer already speculatively tokenized the literal's interior
	// (treating the leading % as modulo), so its lookahead — and the
	// bytes it has consumed — cannot be trusted past this point: a word
	// such as "#" would otherwise start a comment that swallows the
	// closing delimiter and following lines. Reposition the lexer to the
	// byte after the literal and rebuild the lookahead from there instead
	// of re-lexing the interior.
	p.reprimeAt(endOffset, ast.Token{Type: litType, Pos: pos, End: end})

	array := &ast.ArrayLiteral{Elements: elements, Position: pos}
	// Continue parsing so trailing postfixes (such as `[i]` or `.member`) and
	// operators bind to the literal, matching how other parenless arguments are
	// parsed through the normal expression continuation rather than returning
	// the bare array and leaving the postfix to apply to the whole call.
	lineLimited := p.lineLimitedExprs > 0
	limitLine := 0
	if lineLimited {
		limitLine = pos.Line
	}
	return p.continueExpressionParse(array, lowestPrec, limitLine, lineLimited)
}

func (p *parser) parsePercentSymbolsLiteral() ast.Expression {
	entries := decodePercentLiteralEntries(p.curToken.Literal)
	elements := make([]ast.Expression, len(entries))
	for i, entry := range entries {
		elements[i] = &ast.SymbolLiteral{Name: entry, Position: p.curToken.Pos}
	}
	return &ast.ArrayLiteral{Elements: elements, Position: p.curToken.Pos}
}

func (p *parser) parsePercentInterpWordsLiteral() ast.Expression {
	entries := decodePercentLiteralEntries(p.curToken.Literal)
	elements := make([]ast.Expression, 0, len(entries))
	for _, entry := range entries {
		element, ok := p.interpolatedWordElement(entry, p.curToken.Pos)
		if !ok {
			return nil
		}
		elements = append(elements, element)
	}
	return &ast.ArrayLiteral{Elements: elements, Position: p.curToken.Pos}
}

func (p *parser) parsePercentInterpSymbolsLiteral() ast.Expression {
	entries := decodePercentLiteralEntries(p.curToken.Literal)
	elements := make([]ast.Expression, 0, len(entries))
	for _, entry := range entries {
		element, ok := p.interpolatedSymbolElement(entry, p.curToken.Pos)
		if !ok {
			return nil
		}
		elements = append(elements, element)
	}
	return &ast.ArrayLiteral{Elements: elements, Position: p.curToken.Pos}
}

// interpolatedWordElement builds a single %W entry. Entries without an
// embedded expression collapse to a plain string literal so they match the
// AST produced by %w; entries with interpolation become an InterpolatedString.
func (p *parser) interpolatedWordElement(entry string, pos ast.Position) (ast.Expression, bool) {
	parts, ok := p.parseInterpolatedStringParts(entry, pos)
	if !ok {
		return nil, false
	}
	if text, plain := staticStringPart(parts); plain {
		return &ast.StringLiteral{Value: text, Position: pos}, true
	}
	return &ast.InterpolatedString{Parts: parts, Position: pos}, true
}

// interpolatedSymbolElement builds a single %I entry. Entries without an
// embedded expression collapse to a plain symbol literal so they match the
// AST produced by %i; entries with interpolation become an InterpolatedSymbol.
func (p *parser) interpolatedSymbolElement(entry string, pos ast.Position) (ast.Expression, bool) {
	parts, ok := p.parseInterpolatedStringParts(entry, pos)
	if !ok {
		return nil, false
	}
	if text, plain := staticStringPart(parts); plain {
		return &ast.SymbolLiteral{Name: text, Position: pos}, true
	}
	return &ast.InterpolatedSymbol{Parts: parts, Position: pos}, true
}

// staticStringPart returns the literal text and true when parts hold no
// embedded expression, so a %W/%I entry can collapse to a plain literal.
func staticStringPart(parts []ast.StringPart) (string, bool) {
	switch len(parts) {
	case 0:
		return "", true
	case 1:
		if text, ok := parts[0].(ast.StringText); ok {
			return text.Text, true
		}
	}
	return "", false
}

func scanPercentArrayLiteralAt(input string, start int) (rune, []string, int, bool) {
	if start < 0 || start >= len(input) || input[start] != '%' {
		return 0, nil, 0, false
	}
	idx := start + 1
	if idx >= len(input) {
		return 0, nil, 0, false
	}
	kind, width := utf8.DecodeRuneInString(input[idx:])
	if kind != 'w' && kind != 'i' && kind != 'W' && kind != 'I' {
		return 0, nil, 0, false
	}
	interpolating := kind == 'W' || kind == 'I'
	idx += width
	if idx >= len(input) {
		return 0, nil, 0, false
	}
	open, width := utf8.DecodeRuneInString(input[idx:])
	close, paired := percentLiteralClose(open)
	if close == 0 {
		return 0, nil, 0, false
	}
	idx += width

	depth := 1
	var raw strings.Builder
	for idx < len(input) {
		r, width := utf8.DecodeRuneInString(input[idx:])
		idx += width
		if r == '\\' {
			raw.WriteRune(r)
			if idx < len(input) {
				next, nextWidth := utf8.DecodeRuneInString(input[idx:])
				idx += nextWidth
				raw.WriteRune(next)
			}
			continue
		}
		// Skip over #{...} interpolation spans for the interpolating forms so a
		// delimiter inside an interpolation expression (including one nested in a
		// quoted string, e.g. %W[#{"]"}]) does not close the literal early. The
		// span is matched with the same string-aware logic used elsewhere. When
		// '#' is itself the closing delimiter it must close the literal instead of
		// being treated as interpolation, mirroring Ruby where %W#a #{b}# closes at
		// the first '#'.
		if interpolating && close != '#' && r == '#' && idx < len(input) && input[idx] == '{' {
			raw.WriteRune(r)
			raw.WriteByte('{')
			idx++
			end, ok := findStringInterpolationEnd(input, idx)
			if !ok {
				return 0, nil, 0, false
			}
			raw.WriteString(input[idx : end+1])
			idx = end + 1
			continue
		}
		if paired && r == open {
			depth++
		}
		if r == close {
			depth--
			if depth == 0 {
				if interpolating {
					return kind, splitInterpolatedPercentLiteralWords(raw.String()), idx, true
				}
				return kind, splitPercentLiteralWords(raw.String(), open, close), idx, true
			}
		}
		raw.WriteRune(r)
	}
	return 0, nil, 0, false
}

func sourceOffsetForPosition(input string, pos ast.Position) (int, bool) {
	line, column := 1, 1
	for idx, r := range input {
		if line == pos.Line && column == pos.Column {
			return idx, true
		}
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	if line == pos.Line && column == pos.Column {
		return len(input), true
	}
	return 0, false
}

func sourcePositionForOffset(input string, offset int) ast.Position {
	line, column := 1, 1
	for idx, r := range input {
		if idx >= offset {
			return ast.Position{Line: line, Column: column}
		}
		if r == '\n' {
			line++
			column = 1
		} else {
			column++
		}
	}
	return ast.Position{Line: line, Column: column}
}

func offsetHasLeadingWhitespace(input string, offset int) bool {
	if offset <= 0 || offset > len(input) {
		return false
	}
	prev, _ := utf8.DecodeLastRuneInString(input[:offset])
	return prev == ' ' || prev == '\t' || prev == '\r' || prev == '\n'
}

func (p *parser) parseBooleanLiteral() ast.Expression {
	return &ast.BoolLiteral{Value: p.curToken.Type == ast.TokenTrue, Position: p.curToken.Pos}
}

func (p *parser) parseNilLiteral() ast.Expression {
	return &ast.NilLiteral{Position: p.curToken.Pos}
}

func (p *parser) parseSymbolLiteral() ast.Expression {
	return &ast.SymbolLiteral{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseIvarLiteral() ast.Expression {
	if p.curToken.Literal == "" {
		p.errorExpected(p.curToken, "instance variable name")
		return nil
	}
	return &ast.IvarExpr{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseClassVarLiteral() ast.Expression {
	if p.curToken.Literal == "" {
		p.errorExpected(p.curToken, "class variable name")
		return nil
	}
	return &ast.ClassVarExpr{Name: p.curToken.Literal, Position: p.curToken.Pos}
}

func (p *parser) parseSelfLiteral() ast.Expression {
	return &ast.Identifier{Name: "self", Position: p.curToken.Pos}
}

func (p *parser) parseGroupedExpression() ast.Expression {
	p.nextToken()
	expr := p.parseExpression(lowestPrec)
	if !p.expectPeek(ast.TokenRParen) {
		return nil
	}
	return expr
}

func (p *parser) parsePrefixExpression() ast.Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	p.nextToken()
	right := p.parseExpression(precPrefix)
	if right == nil {
		return nil
	}
	return &ast.UnaryExpr{Operator: operator, Right: right, Position: pos}
}

func (p *parser) parseInfixExpression(left ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	operator := p.curToken.Type
	precedence := p.curPrecedence()
	p.nextToken()
	rightPrecedence := precedence
	if operator == ast.TokenPower {
		rightPrecedence--
	}
	right := p.parseExpression(rightPrecedence)
	if right == nil {
		return nil
	}
	return &ast.BinaryExpr{Left: left, Operator: operator, Right: right, Position: pos}
}

func (p *parser) parseConditionalExpression(condition ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	p.nextToken()
	consequent := p.parseExpression(lowestPrec)
	if consequent == nil {
		return nil
	}
	if !p.expectPeek(ast.TokenColon) {
		return nil
	}
	p.nextToken()
	alternate := p.parseExpression(precConditional - 1)
	if alternate == nil {
		return nil
	}
	return &ast.ConditionalExpr{
		Condition:  condition,
		Consequent: consequent,
		Alternate:  alternate,
		Position:   pos,
	}
}

func (p *parser) parseRangeExpression(left ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	exclusive := p.curToken.Type == ast.TokenRangeExcl
	precedence := p.curPrecedence()
	if prefixParserKind(p.peekToken.Type) == prefixParserNone {
		p.addParseErrorSpan(pos, tokenEnd(p.curToken), "range is missing end expression")
		return nil
	}
	p.nextToken()
	right := p.parseExpression(precedence)
	if right == nil {
		return nil
	}
	return &ast.RangeExpr{Start: left, End: right, Exclusive: exclusive, Position: pos}
}

func (p *parser) parseMemberExpression(object ast.Expression) ast.Expression {
	if object == nil {
		return nil
	}
	safe := p.curToken.Type == ast.TokenSafeNav
	p.nextToken()
	if !isMemberNameToken(p.curToken) {
		p.errorExpected(p.curToken, "member name")
		return nil
	}
	return &ast.MemberExpr{Object: object, Property: p.curToken.Literal, Safe: safe, Position: object.Pos()}
}

// isSafeMemberCallee reports whether a call's callee is a member access that
// used the safe-navigation operator (`receiver&.method`). Such calls propagate
// the safe flag so the runtime short-circuits to nil when the receiver is nil.
func isSafeMemberCallee(callee ast.Expression) bool {
	member, ok := callee.(*ast.MemberExpr)
	return ok && member.Safe
}

func (p *parser) parseScopeExpression(object ast.Expression) ast.Expression {
	if object == nil {
		return nil
	}
	p.nextToken()
	if p.curToken.Type != ast.TokenIdent && p.curToken.Type != ast.TokenEnum {
		p.errorExpected(p.curToken, "identifier")
		return nil
	}
	return &ast.ScopeExpr{Object: object, Property: p.curToken.Literal, Position: object.Pos()}
}

func (p *parser) parseIndexExpression(object ast.Expression) ast.Expression {
	pos := p.curToken.Pos
	if p.peekToken.Type == ast.TokenRBracket {
		p.addParseError(p.peekToken.Pos, "index expression requires at least one selector")
		return nil
	}
	p.nextToken()
	indices := []ast.Expression{}
	index := p.parseExpression(lowestPrec)
	if index == nil {
		return nil
	}
	indices = append(indices, index)
	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		next := p.parseExpression(lowestPrec)
		if next == nil {
			return nil
		}
		indices = append(indices, next)
	}
	if !p.expectPeek(ast.TokenRBracket) {
		return nil
	}
	return &ast.IndexExpr{Object: object, Indices: indices, Position: pos}
}

func isMemberNameToken(tok ast.Token) bool {
	if isLabelNameToken(tok) {
		return true
	}
	// The spaceship operator doubles as the `<=>` comparison method name.
	return tok.Type == ast.TokenSpaceship
}

func (p *parser) parseArrayLiteral() ast.Expression {
	pos := p.curToken.Pos
	elements := []ast.Expression{}

	if p.peekToken.Type == ast.TokenRBracket {
		p.nextToken()
		return &ast.ArrayLiteral{Elements: elements, Position: pos}
	}

	p.nextToken()
	elements = append(elements, p.parseExpression(lowestPrec))

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		if p.peekToken.Type == ast.TokenRBracket {
			break
		}
		p.nextToken()
		elements = append(elements, p.parseExpression(lowestPrec))
	}

	if !p.expectPeek(ast.TokenRBracket) {
		return nil
	}

	return &ast.ArrayLiteral{Elements: elements, Position: pos}
}

func (p *parser) parseHashLiteral() ast.Expression {
	pos := p.curToken.Pos
	pairs := []ast.HashPair{}

	if p.peekToken.Type == ast.TokenRBrace {
		p.nextToken()
		return &ast.HashLiteral{Pairs: pairs, Position: pos}
	}

	p.nextToken()
	if pair := p.parseHashPair(); pair.Key != nil {
		pairs = append(pairs, pair)
	}

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		if p.peekToken.Type == ast.TokenRBrace {
			break
		}
		p.nextToken()
		if pair := p.parseHashPair(); pair.Key != nil {
			pairs = append(pairs, pair)
		}
	}

	if !p.expectPeek(ast.TokenRBrace) {
		return nil
	}

	return &ast.HashLiteral{Pairs: pairs, Position: pos}
}

func (p *parser) parseHashPair() ast.HashPair {
	if p.peekToken.Type != ast.TokenColon {
		p.addParseError(p.curToken.Pos, invalidHashPairMessage)
		p.recoverHashPair()
		return ast.HashPair{}
	}

	var key ast.Expression
	// labelKey records a label-style key (name:) so its value may be omitted as
	// shorthand for the matching local variable.
	var labelKey *ast.SymbolLiteral
	switch {
	case isLabelNameToken(p.curToken):
		labelKey = &ast.SymbolLiteral{Name: p.curToken.Literal, Position: p.curToken.Pos}
		key = labelKey
	case p.curToken.Type == ast.TokenString:
		key = &ast.StringLiteral{Value: p.curToken.Literal, Position: p.curToken.Pos}
	default:
		p.addParseError(p.curToken.Pos, invalidHashPairMessage)
		p.recoverHashPair()
		return ast.HashPair{}
	}
	p.nextToken()
	if p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenRBrace || p.peekToken.Type == ast.TokenEOF {
		// Label keys support value omission: {name:} reads the local variable
		// `name`, matching call-site keyword shorthand (greet name:). Missing
		// locals fall through to the normal undefined-variable diagnostic at
		// evaluation time.
		if labelKey != nil {
			value := &ast.Identifier{Name: labelKey.Name, Position: labelKey.Position}
			return ast.HashPair{Key: key, Value: value}
		}
		p.addParseError(p.peekToken.Pos, fmt.Sprintf("missing value for hash key %s", hashKeyName(key)))
		return ast.HashPair{}
	}

	p.nextToken()
	value := p.parseExpression(lowestPrec)
	if value == nil {
		p.recoverHashPair()
		return ast.HashPair{}
	}
	switch p.peekToken.Type {
	case ast.TokenComma, ast.TokenRBrace, ast.TokenEOF:
		return ast.HashPair{Key: key, Value: value}
	default:
		p.addParseError(p.peekToken.Pos, invalidHashPairMessage)
		p.recoverHashPair()
		return ast.HashPair{}
	}
}

// recoverHashPair advances the parser past a malformed hash entry so the
// surrounding hash literal can resume cleanly. It positions peekToken at the
// next top-level "," or "}" (or EOF), skipping over any balanced parentheses,
// brackets, or braces so that removed syntax such as a hash rocket yields a
// single actionable error instead of cascading diagnostics.
func (p *parser) recoverHashPair() {
	nesting := 0
	// curToken can already be an opener when recovery begins (e.g. the rejected
	// entry starts with "{", "[", or "(" as in `{ {a: 1} => v }`). In that case
	// the cursor is already inside that delimiter, so seed nesting accordingly to
	// keep its matching closer from being mistaken for the outer hash boundary.
	switch p.curToken.Type {
	case ast.TokenLParen, ast.TokenLBracket, ast.TokenLBrace:
		nesting++
	}
	for p.peekToken.Type != ast.TokenEOF {
		if nesting == 0 && (p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenRBrace) {
			return
		}
		p.nextToken()
		switch p.curToken.Type {
		case ast.TokenLParen, ast.TokenLBracket, ast.TokenLBrace:
			nesting++
		case ast.TokenRParen, ast.TokenRBracket, ast.TokenRBrace:
			if nesting > 0 {
				nesting--
			}
		}
	}
}

const invalidHashPairMessage = `invalid hash pair: expected key like name: or "name":`

func hashKeyName(key ast.Expression) string {
	switch k := key.(type) {
	case *ast.SymbolLiteral:
		return k.Name
	case *ast.StringLiteral:
		return k.Value
	default:
		return "unknown"
	}
}

func (p *parser) parseBlockLiteral() *ast.BlockLiteral {
	pos := p.curToken.Pos
	params := []ast.Param{}
	stopToken := ast.TokenEnd
	stopName := "end"
	if p.curToken.Type == ast.TokenLBrace {
		stopToken = ast.TokenRBrace
		stopName = "}"
	}

	p.nextToken()
	if p.curToken.Type == ast.TokenPipe {
		var ok bool
		params, ok = p.parseBlockParameters()
		if !ok {
			return nil
		}
		p.nextToken()
	}

	p.pushLocalScope(params, false)
	body := p.parseBlock(stopToken)
	p.popLocalScope()
	if p.curToken.Type != stopToken {
		p.errorExpected(p.curToken, stopName)
	}

	return &ast.BlockLiteral{Params: params, Body: body, Position: pos}
}

func (p *parser) parseBlockParameters() ([]ast.Param, bool) {
	params := []ast.Param{}
	p.nextToken()
	if p.curToken.Type == ast.TokenPipe {
		return params, true
	}

	param, ok := p.parseBlockParameter()
	if !ok {
		return nil, false
	}
	params = append(params, param)

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type == ast.TokenPipe {
			p.addParseError(p.curToken.Pos, "trailing comma in block parameter list")
			return nil, false
		}
		param, ok := p.parseBlockParameter()
		if !ok {
			return nil, false
		}
		params = append(params, param)
	}

	if !p.expectPeek(ast.TokenPipe) {
		return nil, false
	}

	return params, true
}

func (p *parser) parseBlockParameter() (ast.Param, bool) {
	switch p.curToken.Type {
	case ast.TokenIdent:
		param := ast.Param{Name: p.curToken.Literal}
		if p.peekToken.Type == ast.TokenColon {
			p.nextToken()
			p.nextToken()
			param.Type = p.parseBlockParamType()
			if param.Type == nil {
				return ast.Param{}, false
			}
		}
		return param, true
	case ast.TokenLParen:
		return p.parseDestructuredBlockParameter(ast.TokenRParen, ")")
	case ast.TokenLBracket:
		return p.parseDestructuredBlockParameter(ast.TokenRBracket, "]")
	default:
		p.errorExpected(p.curToken, "block parameter")
		return ast.Param{}, false
	}
}

func (p *parser) parseDestructuredBlockParameter(stop ast.TokenType, stopName string) (ast.Param, bool) {
	target := p.parseNestedDestructureTarget(stop, stopName)
	if target == nil {
		return ast.Param{}, false
	}
	if !isBlockParameterTarget(target) {
		p.addParseError(target.Pos(), "invalid block parameter destructuring target")
		return ast.Param{}, false
	}
	return ast.Param{Target: target}, true
}

func isBlockParameterTarget(target ast.Expression) bool {
	switch t := target.(type) {
	case *ast.Identifier:
		return true
	case *ast.DestructureTarget:
		for _, element := range t.Elements {
			// A rest element with no target is an anonymous (discard) rest
			// ("*"), which is a valid block parameter that binds nothing.
			if element.Rest && element.Target == nil {
				continue
			}
			if !isBlockParameterTarget(element.Target) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (p *parser) parseBlockParamType() *ast.TypeExpr {
	first := p.parseTypeAtom()
	if first == nil {
		return nil
	}

	union := []*ast.TypeExpr{first}
	for p.peekToken.Type == ast.TokenPipe && p.blockParamUnionContinues() {
		p.nextToken()
		p.nextToken()
		next := p.parseTypeAtom()
		if next == nil {
			return nil
		}
		union = append(union, next)
	}

	if len(union) == 1 {
		return first
	}

	names := make([]string, len(union))
	for i, option := range union {
		names[i] = ast.FormatTypeExpr(option)
	}
	return &ast.TypeExpr{
		Name:     strings.Join(names, " | "),
		Kind:     ast.TypeUnion,
		Union:    union,
		Position: first.Position,
	}
}

func (p *parser) blockParamUnionContinues() bool {
	if p.peekToken.Type != ast.TokenPipe {
		return false
	}

	savedLexer := *p.l
	savedLexer.bracketStack = append([]bracketFrame(nil), p.l.bracketStack...)
	savedLexer.ternaryStack = append([]ternaryFrame(nil), p.l.ternaryStack...)
	savedCur := p.curToken
	savedPeek := p.peekToken
	savedPeekPeek := p.peekPeek
	savedErrors := len(p.errors)

	p.nextToken()
	p.nextToken()
	atom := p.parseTypeAtom()
	ok := atom != nil && (p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenPipe)

	p.l = &savedLexer
	p.curToken = savedCur
	p.peekToken = savedPeek
	p.peekPeek = savedPeekPeek
	p.errors = p.errors[:savedErrors]
	return ok
}

func (p *parser) parseCallExpression(function ast.Expression) ast.Expression {
	if function == nil {
		return nil
	}
	expr := &ast.CallExpr{Callee: function, Position: function.Pos(), Safe: isSafeMemberCallee(function)}
	args := []ast.Expression{}
	kwargs := []ast.KeywordArg{}

	if p.peekToken.Type == ast.TokenRParen {
		p.nextToken()
		expr.Args = args
		expr.KwArgs = kwargs
		return expr
	}

	p.nextToken()
	p.parseCallArgument(&args, &kwargs)

	for p.peekToken.Type == ast.TokenComma {
		p.nextToken()
		if p.peekToken.Type == ast.TokenRParen {
			break
		}
		p.nextToken()
		if len(kwargs) > 0 && (!isLabelNameToken(p.curToken) || p.peekToken.Type != ast.TokenColon) {
			p.addParseError(p.curToken.Pos, "positional arguments cannot follow keyword arguments")
		}
		p.parseCallArgument(&args, &kwargs)
	}

	if !p.expectPeek(ast.TokenRParen) {
		return nil
	}

	expr.Args = args
	expr.KwArgs = kwargs
	expr.Parenthesized = true
	// Mark keyword arguments as eligible to collapse into a positional options
	// hash. The runtime decides whether the collapse actually applies: plain
	// function calls (including a function value's `call` alias) collapse like
	// the parenless form, while parenthesized method and constructor calls stay
	// strict. The parser cannot distinguish a function value's `call` alias from
	// a method named `call`, so it defers that decision to the runtime.
	if len(kwargs) > 0 {
		expr.KeywordOptionsHash = true
	}
	if p.canAttachPeekBlock() {
		p.nextToken()
		expr.Block = p.parseBlockLiteral()
	}
	return expr
}

func (p *parser) parseParenlessCallExpression(function ast.Expression) ast.Expression {
	if function == nil {
		return nil
	}
	expr := &ast.CallExpr{Callee: function, Position: function.Pos(), Safe: isSafeMemberCallee(function)}
	args := []ast.Expression{}
	kwargs := []ast.KeywordArg{}
	keywordOptionsHash := false

	p.nextToken()
	p.parseParenlessCallArgument(&args, &kwargs, &keywordOptionsHash)

	for p.peekToken.Type == ast.TokenComma &&
		p.peekToken.Pos.Line == p.curToken.Pos.Line &&
		p.peekPeek.Pos.Line == p.curToken.Pos.Line &&
		(isParenlessArgumentStart(p.peekPeek.Type) || isLabelNameToken(p.peekPeek)) {
		p.nextToken()
		p.nextToken()
		if keywordOptionsHash && (!isLabelNameToken(p.curToken) || p.peekToken.Type != ast.TokenColon) {
			p.addParseError(p.curToken.Pos, "positional arguments cannot follow bare keyword arguments in parenless calls")
		}
		p.parseParenlessCallArgument(&args, &kwargs, &keywordOptionsHash)
	}

	expr.Args = args
	expr.KwArgs = kwargs
	expr.KeywordOptionsHash = keywordOptionsHash
	return expr
}

func (p *parser) parseTrailingBlockExpression(callee ast.Expression) ast.Expression {
	return p.callWithBlock(callee, p.parseBlockLiteral())
}

func (p *parser) callWithBlock(callee ast.Expression, block *ast.BlockLiteral) ast.Expression {
	if callee == nil {
		return nil
	}
	var call *ast.CallExpr
	if existing, ok := callee.(*ast.CallExpr); ok {
		call = existing
	} else {
		call = &ast.CallExpr{Callee: callee, Position: callee.Pos(), Safe: isSafeMemberCallee(callee)}
	}
	call.Block = block
	return call
}

func (p *parser) canAttachPeekBlock() bool {
	if p.lineLimitedExprs > 0 && p.peekStopsLineExpression() {
		return false
	}
	if p.peekToken.Type == ast.TokenDo {
		return true
	}
	return p.peekToken.Type == ast.TokenLBrace && p.peekToken.Pos.Line == p.curToken.Pos.Line
}

func (p *parser) parseCallArgument(args *[]ast.Expression, kwargs *[]ast.KeywordArg) {
	switch p.curToken.Type {
	case ast.TokenAsterisk:
		p.recoverUnsupportedCallExpansion("call splat is not supported; pass positional arguments explicitly")
		return
	case ast.TokenPower:
		p.recoverUnsupportedCallExpansion("keyword splat is not supported; pass keyword arguments explicitly")
		return
	}

	if p.curToken.Type == ast.TokenAmpersand {
		p.recoverUnsupportedAmpersandCallArgument()
		return
	}

	if isLabelNameToken(p.curToken) && p.peekToken.Type == ast.TokenColon {
		name := p.curToken.Literal
		pos := p.curToken.Pos
		p.nextToken()
		if p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenRParen {
			*kwargs = append(*kwargs, ast.KeywordArg{Name: name, Value: &ast.Identifier{Name: name, Position: pos}})
			return
		}
		p.nextToken()
		value := p.parseExpression(lowestPrec)
		if value == nil {
			return
		}
		*kwargs = append(*kwargs, ast.KeywordArg{Name: name, Value: value})
		return
	}

	expr := p.parseExpression(lowestPrec)
	if expr != nil {
		*args = append(*args, expr)
	}
}

func (p *parser) parseParenlessCallArgument(args *[]ast.Expression, kwargs *[]ast.KeywordArg, keywordOptionsHash *bool) {
	if p.curToken.Type == ast.TokenPercent {
		expr := p.parsePercentArrayLiteralArgument()
		if expr != nil {
			*args = append(*args, expr)
		}
		return
	}

	switch p.curToken.Type {
	case ast.TokenAsterisk:
		p.recoverUnsupportedCallExpansion("call splat is not supported; pass positional arguments explicitly")
		return
	case ast.TokenPower:
		p.recoverUnsupportedCallExpansion("keyword splat is not supported; pass keyword arguments explicitly")
		return
	}

	if p.curToken.Type == ast.TokenAmpersand {
		p.recoverUnsupportedAmpersandCallArgument()
		return
	}

	if isLabelNameToken(p.curToken) && p.peekToken.Type == ast.TokenColon {
		name := p.curToken.Literal
		pos := p.curToken.Pos
		p.nextToken()
		*keywordOptionsHash = true
		if p.parenlessKeywordArgumentCanUseShorthand() {
			*kwargs = append(*kwargs, ast.KeywordArg{Name: name, Value: &ast.Identifier{Name: name, Position: pos}})
			return
		}
		p.nextToken()
		value := p.parseLineExpression(lowestPrec)
		if value == nil {
			return
		}
		*kwargs = append(*kwargs, ast.KeywordArg{Name: name, Value: value})
		return
	}

	expr := p.parseLineExpression(lowestPrec)
	if expr != nil {
		*args = append(*args, expr)
	}
}

func (p *parser) parenlessKeywordArgumentCanUseShorthand() bool {
	if p.peekToken.Type == ast.TokenEOF || p.peekToken.Type == ast.TokenComma {
		return true
	}
	return p.peekToken.Pos.Line != p.curToken.Pos.Line
}

func (p *parser) recoverUnsupportedCallExpansion(message string) {
	p.addParseErrorSpan(p.curToken.Pos, tokenEnd(p.curToken), message)
	p.recoverUnsupportedCallArgument()
}

func (p *parser) recoverUnsupportedAmpersandCallArgument() {
	p.addParseErrorSpan(
		p.curToken.Pos,
		tokenEnd(p.curToken),
		"ampersand block forwarding and symbol-to-proc shorthand are not supported; use an explicit do/end or brace block",
	)
	p.recoverUnsupportedCallArgument()
}

func (p *parser) recoverUnsupportedCallArgument() {
	startLine := p.curToken.Pos.Line
	nesting := 0
	for p.peekToken.Type != ast.TokenEOF &&
		p.peekToken.Pos.Line == startLine {
		if nesting == 0 && (p.peekToken.Type == ast.TokenComma || p.peekToken.Type == ast.TokenRParen) {
			return
		}
		p.nextToken()
		switch p.curToken.Type {
		case ast.TokenLParen, ast.TokenLBracket, ast.TokenLBrace:
			nesting++
		case ast.TokenRParen, ast.TokenRBracket, ast.TokenRBrace:
			if nesting > 0 {
				nesting--
			}
		}
	}
}

// isLabelNameToken reports whether a token may appear immediately before a
// colon as a label, such as a hash key (`{rescue: 1}`) or a keyword argument
// (`call(begin: 1)`). Every reserved keyword that can precede a colon is
// allowed, mirroring Ruby, which treats keyword-shaped labels uniformly.
func isLabelNameToken(tok ast.Token) bool {
	switch tok.Type {
	case ast.TokenIdent,
		ast.TokenDef, ast.TokenClass, ast.TokenEnum, ast.TokenExport, ast.TokenSelf, ast.TokenPrivate, ast.TokenProperty, ast.TokenGetter, ast.TokenSetter,
		ast.TokenBegin, ast.TokenRescue, ast.TokenEnsure, ast.TokenRaise,
		ast.TokenEnd, ast.TokenReturn, ast.TokenYield, ast.TokenDo, ast.TokenThen, ast.TokenFor, ast.TokenWhile, ast.TokenUntil,
		ast.TokenBreak, ast.TokenNext, ast.TokenIn, ast.TokenIf, ast.TokenUnless, ast.TokenCase, ast.TokenWhen, ast.TokenElsif, ast.TokenElse,
		ast.TokenTrue, ast.TokenFalse, ast.TokenNil:
		return true
	case ast.TokenAnd, ast.TokenOr, ast.TokenNot:
		return isWordBooleanKeywordToken(tok)
	default:
		return false
	}
}

func isWordBooleanKeywordToken(tok ast.Token) bool {
	switch tok.Type {
	case ast.TokenAnd:
		return tok.Literal == "and"
	case ast.TokenOr:
		return tok.Literal == "or"
	case ast.TokenNot:
		return tok.Literal == "not"
	default:
		return false
	}
}

func (p *parser) parseIfExpression() ast.Expression {
	pos := p.curToken.Pos
	p.nextToken()
	condition := p.parseLineExpression(lowestPrec)
	if condition == nil {
		return nil
	}

	p.nextToken()
	p.consumeIfExpressionResultSeparator()
	consequent := p.parseExpressionWithBlock()
	if consequent == nil {
		return nil
	}
	p.nextToken()
	p.skipStatementSeparators()

	var elseifBranches []ast.IfExprBranch
	for p.curToken.Type == ast.TokenElsif {
		p.nextToken()
		cond := p.parseLineExpression(lowestPrec)
		if cond == nil {
			return nil
		}
		p.nextToken()
		p.consumeIfExpressionResultSeparator()
		result := p.parseExpressionWithBlock()
		if result == nil {
			return nil
		}
		elseifBranches = append(elseifBranches, ast.IfExprBranch{Condition: cond, Result: result})
		p.nextToken()
		p.skipStatementSeparators()
	}

	var alternate ast.Expression
	if p.curToken.Type == ast.TokenElse {
		p.nextToken()
		p.skipStatementSeparators()
		alternate = p.parseExpressionWithBlock()
		if alternate == nil {
			return nil
		}
		p.nextToken()
		p.skipStatementSeparators()
	}

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
		return nil
	}

	return &ast.IfExpr{
		Condition:  condition,
		Consequent: consequent,
		ElseIf:     elseifBranches,
		Alternate:  alternate,
		Position:   pos,
	}
}

func (p *parser) consumeIfExpressionResultSeparator() {
	if p.curToken.Type == ast.TokenThen {
		p.nextToken()
	}
	p.skipStatementSeparators()
}

func (p *parser) parseCaseExpression() ast.Expression {
	pos := p.curToken.Pos
	p.nextToken()
	p.skipStatementSeparators()
	var target ast.Expression
	if p.curToken.Type != ast.TokenWhen {
		target = p.parseLineExpression(lowestPrec)
		if target == nil {
			return nil
		}
		p.nextToken()
		p.skipStatementSeparators()
	}

	clauses := []ast.CaseWhenClause{}
	for p.curToken.Type == ast.TokenWhen {
		p.nextToken()
		values := []ast.Expression{}
		first := p.parseLineExpression(lowestPrec)
		if first == nil {
			return nil
		}
		values = append(values, first)
		for p.peekToken.Type == ast.TokenComma {
			p.nextToken()
			p.nextToken()
			value := p.parseLineExpression(lowestPrec)
			if value == nil {
				return nil
			}
			values = append(values, value)
		}

		p.nextToken()
		p.consumeCaseResultSeparator()
		result := p.parseExpressionWithBlock()
		if result == nil {
			return nil
		}
		clauses = append(clauses, ast.CaseWhenClause{Values: values, Result: result})
		p.nextToken()
		p.skipStatementSeparators()
	}

	if len(clauses) == 0 {
		p.errorExpected(p.curToken, "when")
		return nil
	}

	var elseExpr ast.Expression
	if p.curToken.Type == ast.TokenElse {
		p.nextToken()
		p.skipStatementSeparators()
		elseExpr = p.parseExpressionWithBlock()
		if elseExpr == nil {
			return nil
		}
		p.nextToken()
		p.skipStatementSeparators()
	}

	if p.curToken.Type != ast.TokenEnd {
		p.errorExpected(p.curToken, "end")
		return nil
	}

	return &ast.CaseExpr{Target: target, Clauses: clauses, ElseExpr: elseExpr, Position: pos}
}

func (p *parser) consumeCaseResultSeparator() {
	if p.curToken.Type == ast.TokenThen {
		p.nextToken()
	}
	p.skipStatementSeparators()
}

func (p *parser) parseYieldExpression() ast.Expression {
	pos := p.curToken.Pos
	var args []ast.Expression
	if p.peekToken.Type == ast.TokenLParen {
		p.nextToken()
		p.nextToken()
		if p.curToken.Type != ast.TokenRParen {
			args = append(args, p.parseExpression(lowestPrec))
			for p.peekToken.Type == ast.TokenComma {
				p.nextToken()
				if p.peekToken.Type == ast.TokenRParen {
					break
				}
				p.nextToken()
				args = append(args, p.parseExpression(lowestPrec))
			}
			if !p.expectPeek(ast.TokenRParen) {
				return nil
			}
		}
	} else if p.peekToken.Pos.Line == pos.Line && prefixParserKind(p.peekToken.Type) != prefixParserNone {
		p.nextToken()
		args = append(args, p.parseLineExpression(lowestPrec))
		for p.peekToken.Type == ast.TokenComma &&
			p.peekToken.Pos.Line == pos.Line &&
			p.peekPeek.Pos.Line == pos.Line &&
			prefixParserKind(p.peekPeek.Type) != prefixParserNone {
			p.nextToken()
			p.nextToken()
			args = append(args, p.parseLineExpression(lowestPrec))
		}
	}
	return &ast.YieldExpr{Args: args, Position: pos}
}
