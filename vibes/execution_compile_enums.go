package vibes

import (
	"fmt"
	"strings"
	"unicode"
)

func compileEnumDef(stmt *EnumStmt) (*EnumDef, error) {
	if strings.HasSuffix(stmt.Name, "?") {
		return nil, fmt.Errorf("enum name %s must not end with '?'", stmt.Name)
	}
	if typ, _ := resolveType(stmt.Name); typ != TypeUnknown {
		return nil, fmt.Errorf("enum name %s conflicts with built-in type", stmt.Name)
	}
	enumDef := &EnumDef{
		Name:         stmt.Name,
		Members:      make(map[string]*EnumValueDef, len(stmt.Members)),
		MembersByKey: make(map[string]*EnumValueDef, len(stmt.Members)),
		Order:        make([]string, 0, len(stmt.Members)),
	}
	for i, member := range stmt.Members {
		symbol := enumMemberSymbol(member.Name)
		if _, exists := enumDef.Members[member.Name]; exists {
			return nil, fmt.Errorf("duplicate enum member %s.%s", stmt.Name, member.Name)
		}
		if prior, exists := enumDef.MembersByKey[symbol]; exists {
			return nil, fmt.Errorf("enum %s member %s conflicts with %s after symbol normalization", stmt.Name, member.Name, prior.Name)
		}
		value := &EnumValueDef{
			Enum:   enumDef,
			Name:   member.Name,
			Symbol: symbol,
			Index:  i,
		}
		enumDef.Members[member.Name] = value
		enumDef.MembersByKey[symbol] = value
		enumDef.Order = append(enumDef.Order, member.Name)
	}
	return enumDef, nil
}

func enumMemberSymbol(name string) string {
	if name == "" {
		return ""
	}

	var b strings.Builder
	runes := []rune(name)
	lastUnderscore := false
	for i, r := range runes {
		if r == '_' {
			if b.Len() > 0 && !lastUnderscore {
				b.WriteRune('_')
				lastUnderscore = true
			}
			continue
		}
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				var next rune
				if i+1 < len(runes) {
					next = runes[i+1]
				}
				if prev != '_' && (unicode.IsLower(prev) || unicode.IsDigit(prev) || (next != 0 && unicode.IsLower(next))) {
					b.WriteRune('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			lastUnderscore = false
			continue
		}
		b.WriteRune(unicode.ToLower(r))
		lastUnderscore = false
	}
	return b.String()
}
