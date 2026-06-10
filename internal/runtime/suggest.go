package runtime

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Suggestion thresholds. A candidate is suggested when its Levenshtein
// distance from the missing name is at most suggestMaxDistance; names of
// suggestShortNameRunes runes or fewer tighten the cap to one edit so short
// identifiers do not attract noisy matches. Case-only mismatches are always
// accepted and rank ahead of everything else.
const (
	suggestMaxDistance    = 2
	suggestShortNameRunes = 4
	suggestMaxResults     = 3
)

// didYouMean renders a suffix such as ` (did you mean "x"?)` listing the
// candidates that closely match name, or an empty string when nothing is
// close. It is intended for lookup-failure error paths only.
func didYouMean(name string, candidates []string) string {
	matches := suggestNames(name, candidates)
	if len(matches) == 0 {
		return ""
	}
	quoted := make([]string, len(matches))
	for i, match := range matches {
		quoted[i] = fmt.Sprintf("%q", match)
	}
	switch len(quoted) {
	case 1:
		return fmt.Sprintf(" (did you mean %s?)", quoted[0])
	case 2:
		return fmt.Sprintf(" (did you mean %s or %s?)", quoted[0], quoted[1])
	default:
		return fmt.Sprintf(" (did you mean %s, or %s?)", strings.Join(quoted[:len(quoted)-1], ", "), quoted[len(quoted)-1])
	}
}

// suggestNames returns up to suggestMaxResults candidates that closely match
// name, ordered by edit distance and then lexicographically. Duplicate
// candidates are considered once.
func suggestNames(name string, candidates []string) []string {
	if name == "" {
		return nil
	}
	limit := suggestMaxDistance
	if len([]rune(name)) <= suggestShortNameRunes {
		limit = 1
	}

	type ranked struct {
		name string
		rank int
	}
	matches := make([]ranked, 0, suggestMaxResults)
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" || candidate == name {
			continue
		}
		if _, dup := seen[candidate]; dup {
			continue
		}
		seen[candidate] = struct{}{}
		rank, ok := suggestionRank(name, candidate, limit)
		if !ok {
			continue
		}
		matches = append(matches, ranked{name: candidate, rank: rank})
	}
	slices.SortFunc(matches, func(a, b ranked) int {
		if a.rank != b.rank {
			return cmp.Compare(a.rank, b.rank)
		}
		return cmp.Compare(a.name, b.name)
	})
	if len(matches) > suggestMaxResults {
		matches = matches[:suggestMaxResults]
	}
	names := make([]string, len(matches))
	for i, match := range matches {
		names[i] = match.name
	}
	return names
}

// suggestionRank scores candidate against name. Case-only mismatches rank
// zero so they sort ahead of genuine edits; everything else ranks by its
// Levenshtein distance when that distance is within limit.
func suggestionRank(name, candidate string, limit int) (int, bool) {
	if strings.EqualFold(name, candidate) {
		return 0, true
	}
	distance, ok := levenshteinWithin(name, candidate, limit)
	if !ok {
		return 0, false
	}
	return distance, true
}

// levenshteinWithin computes the Levenshtein distance between a and b over
// runes, abandoning early once the distance is guaranteed to exceed limit.
func levenshteinWithin(a, b string, limit int) (int, bool) {
	short := []rune(a)
	long := []rune(b)
	if len(short) > len(long) {
		short, long = long, short
	}
	if len(long)-len(short) > limit {
		return 0, false
	}

	prev := make([]int, len(short)+1)
	curr := make([]int, len(short)+1)
	for i := range prev {
		prev[i] = i
	}
	for j, longRune := range long {
		curr[0] = j + 1
		rowMin := curr[0]
		for i, shortRune := range short {
			cost := 1
			if shortRune == longRune {
				cost = 0
			}
			curr[i+1] = min(prev[i+1]+1, curr[i]+1, prev[i]+cost)
			if curr[i+1] < rowMin {
				rowMin = curr[i+1]
			}
		}
		if rowMin > limit {
			return 0, false
		}
		prev, curr = curr, prev
	}
	if prev[len(short)] > limit {
		return 0, false
	}
	return prev[len(short)], true
}
