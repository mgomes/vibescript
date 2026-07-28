package runtime

import (
	"fmt"
	"strings"
	"time"
)

// Time has two formatting methods with different format languages: format
// takes a Go reference layout, strftime takes Ruby percent directives. Passing
// one language to the other used to return the format string as data.
//
// The strftime direction is the dangerous one, because the Go reference layout
// *is* a date: t.strftime("2006-01-02") answered "2006-01-02", which survives a
// visual check, a String type check, an ISO-8601 regex, and a length check. A
// report could emit the same twenty-year-old date on every row and look
// entirely plausible.
//
// The two directions are detected differently because the two format
// languages fail differently. A percent directive is a positive signal, so the
// format direction can simply ask the strftime renderer whether it recognizes
// one. A Go layout has no marker at all -- every character is either a
// reference component or literal text -- so asking Go's formatter whether it
// changes the string reports ordinary text as a layout. That direction matches
// distinctive reference fragments instead, and then confirms with Go.

// goLayoutSignatures are Go reference-time fragments distinctive enough to
// identify a layout on sight. Every layout that spells out a date or a time of
// day contains one, and none of them is plausible as literal text inside a
// format string.
//
// Matching these rather than asking whether Go's formatter changes the string
// is deliberate. Go reads a bare digit as a reference component, so "Section
// 3" formats to "Section 2" and "2026 Report" to "27 Report" -- both would be
// reported as Go layouts, and the advice to use format instead would be wrong.
// Requiring a signature trades a couple of exotic layouts ("01/02" alone) for
// never misdiagnosing ordinary text.
var goLayoutSignatures = []string{"2006", "15:04", "01/02", "02/01", "01-02", "02-01"}

// checkStrftimeGivenGoLayout reports an error when a strftime format carries
// no percent directive but does carry a Go reference layout, which means a Go
// layout arrived at the percent formatter.
//
// A format with no percent directive renders the same text for every receiver,
// so it is never a meaningful strftime call. Requiring one keeps this check
// off every correct format's path for the cost of a single scan.
func checkStrftimeGivenGoLayout(t time.Time, format string) error {
	if strings.ContainsRune(format, '%') {
		return nil
	}
	if !containsGoLayoutSignature(format) {
		return nil
	}
	// Confirm Go actually reads it as a layout before blaming one.
	if t.Format(format) == format {
		return nil
	}
	return fmt.Errorf("time.strftime expects a percent format such as \"%%Y-%%m-%%d\"; %q is a Go layout, use format for that", format)
}

// containsGoLayoutSignature reports whether s carries a distinctive Go
// reference-time fragment.
func containsGoLayoutSignature(s string) bool {
	for _, signature := range goLayoutSignatures {
		if strings.Contains(s, signature) {
			return true
		}
	}
	return false
}

// checkFormatGivenStrftime reports an error when a Go layout contains a
// percent directive that the strftime renderer recognizes, which means a
// percent format arrived at the Go formatter.
//
// Go layouts treat an unrecognized percent as literal text, so a layout with
// no percent at all cannot be a strftime format and skips the check.
//
// Detection is syntactic rather than by rendering. Rendering honors a
// directive's requested width, so classifying `t.format("%1000000000N")` would
// allocate about a gigabyte purely to decide -- with no memory limit set, that
// exhausts the process, where Time#format previously treated the input as a
// tiny literal.
func checkFormatGivenStrftime(layout string) error {
	if !strings.ContainsRune(layout, '%') {
		return nil
	}
	if !containsRecognizedStrftimeDirective(layout) {
		return nil
	}
	return fmt.Errorf("time.format expects a Go layout such as \"2006-01-02\"; %q is a strftime format, use strftime for that", layout)
}

// strftimeDirectiveLetters are the directive bytes the renderer recognizes.
// Anything else after a percent is emitted verbatim, as in Ruby, so it does
// not make a string a strftime format.
const strftimeDirectiveLetters = "%AbBCdDFhHIjklLmMnNpPrRStTuwxXyYzZ"

// containsRecognizedStrftimeDirective reports whether s carries a percent
// directive the renderer would act on, without rendering anything.
func containsRecognizedStrftimeDirective(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '%' {
			continue
		}
		token, ok := scanStrftimeDirective(s, i)
		if !ok {
			return false
		}
		// A literal %% renders a percent sign rather than a field, so it does
		// not make the string a strftime format on its own.
		if token.directive != '%' && strings.IndexByte(strftimeDirectiveLetters, token.directive) >= 0 {
			return true
		}
		i += len(token.source) - 1
	}
	return false
}
