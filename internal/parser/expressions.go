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

		if p.peekToken.Type == ast.TokenAmpersand && p.peekPeek.Type == ast.TokenDot {
			p.recoverUnsupportedSafeNavigation()
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

func (p *parser) recoverUnsupportedSafeNavigation() {
	start := p.peekToken
	end := p.peekPeek.End
	if end == (ast.Position{}) {
		end = tokenEnd(start)
	}
	p.addParseErrorSpan(start.Pos, end, "safe navigation is not supported; use an explicit nil check")

	p.nextToken()
	p.nextToken()
	recoveryLine := start.Pos.Line
	if p.peekToken.Type != ast.TokenEOF && p.peekToken.Pos.Line > recoveryLine {
		recoveryLine = p.peekToken.Pos.Line
	}
	nesting := 0
	for p.peekToken.Type != ast.TokenEOF {
		if nesting == 0 && isSafeNavigationRecoveryStop(p.peekToken.Type) {
			return
		}
		if nesting == 0 && p.peekToken.Pos.Line > recoveryLine && !p.lineLimitedContinuationToken(p.peekToken) {
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

func isSafeNavigationRecoveryStop(tt ast.TokenType) bool {
	switch tt {
	case ast.TokenComma, ast.TokenRParen, ast.TokenRBracket, ast.TokenRBrace:
		return true
	default:
		return false
	}
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
	return isParenlessArgumentStart(p.peekToken.Type)
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
	case ast.TokenLParen, ast.TokenLBracket, ast.TokenLBrace, ast.TokenMinus:
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
	case ast.TokenBang, ast.TokenNot, ast.TokenMinus:
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
		ast.TokenEQ, ast.TokenNotEQ, ast.TokenLT, ast.TokenLTE, ast.TokenGT, ast.TokenGTE,
		ast.TokenSpaceship, ast.TokenAnd, ast.TokenOr:
		return infixParserInfixExpression
	case ast.TokenQuestion:
		return infixParserConditionalExpression
	case ast.TokenRange, ast.TokenRangeExcl:
		return infixParserRangeExpression
	case ast.TokenLParen:
		return infixParserCallExpression
	case ast.TokenDot:
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
	case ast.TokenDot, ast.TokenScope, ast.TokenPlus, ast.TokenSlash, ast.TokenAsterisk, ast.TokenPower, ast.TokenPercent, ast.TokenRange, ast.TokenRangeExcl, ast.TokenEQ, ast.TokenNotEQ, ast.TokenLT, ast.TokenLTE, ast.TokenGT, ast.TokenGTE, ast.TokenSpaceship, ast.TokenAnd, ast.TokenOr, ast.TokenQuestion:
		return true
	case ast.TokenMinus:
		return p.minusContinuesLine(tok)
	default:
		return false
	}
}

func (p *parser) minusContinuesLine(tok ast.Token) bool {
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
	value, err := strconv.ParseInt(p.curToken.Literal, 10, 64)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid integer literal")
		return nil
	}
	return &ast.IntegerLiteral{Value: value, Position: p.curToken.Pos}
}

func (p *parser) parseFloatLiteral() ast.Expression {
	value, err := strconv.ParseFloat(p.curToken.Literal, 64)
	if err != nil {
		p.addParseError(p.curToken.Pos, "invalid float literal")
		return nil
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

func skipEscapedByte(raw string, i int) int {
	if i+1 >= len(raw) {
		return len(raw)
	}
	return i + 2
}

func findStringInterpolationEnd(raw string, start int) (int, bool) {
	depth := 1
	for i := start; i < len(raw); {
		switch raw[i] {
		case '\\':
			i = skipEscapedByte(raw, i)
		case '\'', '"':
			next, ok := skipQuotedInterpolationString(raw, i)
			if !ok {
				return 0, false
			}
			i = next
		case '{':
			depth++
			i++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
			i++
		default:
			i++
		}
	}
	return 0, false
}

func skipQuotedInterpolationString(raw string, start int) (int, bool) {
	quote := raw[start]
	for i := start + 1; i < len(raw); {
		switch raw[i] {
		case '\\':
			i = skipEscapedByte(raw, i)
		case quote:
			return i + 1, true
		default:
			i++
		}
	}
	return 0, false
}

func (p *parser) parseStringInterpolationExpression(raw string, pos ast.Position) (ast.Expression, bool) {
	exprParser := newParser(raw)
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
	return &ast.ArrayLiteral{Elements: elements, Position: pos}
}

func (p *parser) parsePercentSymbolsLiteral() ast.Expression {
	entries := decodePercentLiteralEntries(p.curToken.Literal)
	elements := make([]ast.Expression, len(entries))
	for i, entry := range entries {
		elements[i] = &ast.SymbolLiteral{Name: entry, Position: p.curToken.Pos}
	}
	return &ast.ArrayLiteral{Elements: elements, Position: p.curToken.Pos}
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
	if kind != 'w' && kind != 'i' {
		return 0, nil, 0, false
	}
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
		if paired && r == open {
			depth++
		}
		if r == close {
			depth--
			if depth == 0 {
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
	p.nextToken()
	if !isMemberNameToken(p.curToken) {
		p.errorExpected(p.curToken, "member name")
		return nil
	}
	return &ast.MemberExpr{Object: object, Property: p.curToken.Literal, Position: object.Pos()}
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
	p.nextToken()
	index := p.parseExpression(lowestPrec)
	if index == nil {
		return nil
	}
	if !p.expectPeek(ast.TokenRBracket) {
		return nil
	}
	return &ast.IndexExpr{Object: object, Index: index, Position: pos}
}

func isMemberNameToken(tok ast.Token) bool {
	if isLabelNameToken(tok) {
		return true
	}
	switch tok.Type {
	case ast.TokenExport, ast.TokenBegin, ast.TokenRescue, ast.TokenEnsure, ast.TokenRaise, ast.TokenSpaceship:
		return true
	default:
		return false
	}
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
	if p.peekToken.Type == ast.TokenColon {
		return p.parseColonHashPair()
	}
	return p.parseHashRocketPair()
}

func (p *parser) parseColonHashPair() ast.HashPair {
	var key ast.Expression
	switch {
	case isLabelNameToken(p.curToken):
		key = &ast.SymbolLiteral{Name: p.curToken.Literal, Position: p.curToken.Pos}
	case p.curToken.Type == ast.TokenString:
		key = &ast.StringLiteral{Value: p.curToken.Literal, Position: p.curToken.Pos}
	default:
		p.addParseError(p.curToken.Pos, invalidHashPairMessage)
		return ast.HashPair{}
	}
	p.nextToken()
	return p.parseHashPairValue(key)
}

func (p *parser) parseHashRocketPair() ast.HashPair {
	key := p.parseExpression(lowestPrec)
	if key == nil {
		return ast.HashPair{}
	}
	if p.peekToken.Type != ast.TokenArrow {
		p.addParseError(p.curToken.Pos, invalidHashPairMessage)
		p.recoverHashPairRemainder()
		return ast.HashPair{}
	}
	p.nextToken()
	return p.parseHashPairValue(key)
}

const invalidHashPairMessage = `invalid hash pair: expected key like name:, "name":, :name =>, or expression =>`

func (p *parser) parseHashPairValue(key ast.Expression) ast.HashPair {
	p.nextToken()
	if p.curToken.Type == ast.TokenComma || p.curToken.Type == ast.TokenRBrace {
		p.addParseError(p.curToken.Pos, fmt.Sprintf("missing value for hash key %s", hashKeyName(key)))
		return ast.HashPair{}
	}

	value := p.parseExpression(lowestPrec)
	if value == nil {
		return ast.HashPair{}
	}
	return ast.HashPair{Key: key, Value: value}
}

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

func (p *parser) recoverHashPairRemainder() {
	nesting := 0
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
	expr := &ast.CallExpr{Callee: function, Position: function.Pos()}
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
		p.parseCallArgument(&args, &kwargs)
	}

	if !p.expectPeek(ast.TokenRParen) {
		return nil
	}

	expr.Args = args
	expr.KwArgs = kwargs
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
	expr := &ast.CallExpr{Callee: function, Position: function.Pos()}
	args := []ast.Expression{}
	kwargs := []ast.KeywordArg{}
	bareKeywordArgs := false

	p.nextToken()
	p.parseParenlessCallArgument(&args, &kwargs, &bareKeywordArgs)

	for p.peekToken.Type == ast.TokenComma &&
		p.peekToken.Pos.Line == p.curToken.Pos.Line &&
		p.peekPeek.Pos.Line == p.curToken.Pos.Line &&
		isParenlessArgumentStart(p.peekPeek.Type) {
		p.nextToken()
		p.nextToken()
		if bareKeywordArgs && (!isLabelNameToken(p.curToken) || p.peekToken.Type != ast.TokenColon) {
			p.addParseError(p.curToken.Pos, "positional arguments cannot follow bare keyword arguments in parenless calls")
		}
		p.parseParenlessCallArgument(&args, &kwargs, &bareKeywordArgs)
	}

	expr.Args = args
	expr.KwArgs = kwargs
	expr.BareKeywordArgs = bareKeywordArgs
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
		call = &ast.CallExpr{Callee: callee, Position: callee.Pos()}
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

func (p *parser) parseParenlessCallArgument(args *[]ast.Expression, kwargs *[]ast.KeywordArg, bareKeywordArgs *bool) {
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
		*bareKeywordArgs = true
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

func isLabelNameToken(tok ast.Token) bool {
	switch tok.Type {
	case ast.TokenIdent,
		ast.TokenDef, ast.TokenClass, ast.TokenEnum, ast.TokenSelf, ast.TokenPrivate, ast.TokenProperty, ast.TokenGetter, ast.TokenSetter,
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
