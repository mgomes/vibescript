package runtime

// This file is a spelling gate for the cost of a newly allocated array. The
// bytes an array occupies beyond its elements are a property of the arrayData
// struct, and any projection that restates them by hand is a copy of that
// struct's size parked where the struct's author will never look. One such copy
// is how this file's PR began: arrayData was exactly a slice header, so
// estimatedValueBytes + estimatedSliceBaseBytes priced a new array by accident,
// and adding a field to the struct silently stopped paying for 8 bytes of every
// array in the program.
//
// Creating arraySlotBackingBytes fixed the sites that existed. It does not fix
// the next one: whoever adds a projection copies the nearest existing one, so
// as long as the raw sum can be written it will be written again -- which is
// how checkProjectedIntArrayBytesWithLive and projectRestWindow kept the old
// formula through the commit that introduced the helper, leaving two functions
// with explicit pre-allocation guarantees able to allocate a wrapper the quota
// had no room for.
//
// So the composition is made the only available spelling. estimatedValueBytes
// and estimatedSliceBaseBytes both have honest uses apart from each other and
// cannot be unexported; what is forbidden is adding them together, which is
// only ever an array's structure being priced without its wrapper. The gate
// works on source text through go/parser, the way walker_coverage_test.go does,
// and names the file, line, and function so the reader is pointed at the helper
// rather than at a rule.

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// arraySizeConstants are the two constants whose sum is an array's structure
// minus its wrapper. Neither is forbidden alone.
var arraySizeConstants = map[string]struct{}{
	"estimatedValueBytes":     {},
	"estimatedSliceBaseBytes": {},
}

// spellingExemptions maps a function name to why it may add the two together.
// An exemption is a claim that the sum is not an array being priced, and it has
// to say what it is instead.
var spellingExemptions = map[string]string{
	// Prices the stringCharSetSpec struct, not an array: a bool, an entry
	// slice, and a length. It is its own hand-written struct size and wants
	// deriving from the struct the way arrayData now is, but it over-counts
	// (96 bytes stated for a 40-byte struct) rather than under-counting, so
	// correcting it lowers a reservation and belongs to its own change.
	"stringCharSetArgsScratchBytes": "prices the stringCharSetSpec struct rather than an array",
}

// TestNewArrayCostHasOneSpelling fails when a projection prices a new array by
// adding estimatedValueBytes to estimatedSliceBaseBytes instead of calling
// arraySlotBackingBytes.
func TestNewArrayCostHasOneSpelling(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string
	scanned := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := goparser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		scanned++
		offenders = append(offenders, rawArraySizeSums(fset, file)...)
	}

	// A gate that scanned nothing passes for the wrong reason.
	if scanned == 0 {
		t.Fatalf("no non-test Go files were scanned, so this gate proved nothing")
	}

	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Fatalf("a new array's cost is spelled by hand at %d site(s):\n  %s\n\n"+
			"Adding estimatedValueBytes to estimatedSliceBaseBytes prices an array's Value and slice "+
			"header and nothing else, so it misses whatever arrayData carries beyond them -- which is "+
			"the under-count this gate exists to stop recurring. Call arraySlotBackingBytes for an "+
			"array, liveValueSliceBytes for a slot array held only on a Go stack, or add an entry to "+
			"spellingExemptions saying what else the sum prices",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// rawArraySizeSums returns one description per forbidden sum in file, skipping
// functions listed in spellingExemptions.
func rawArraySizeSums(fset *token.FileSet, file *goast.File) []string {
	var found []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*goast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, exempt := spellingExemptions[fn.Name.Name]; exempt {
			continue
		}
		goast.Inspect(fn.Body, func(node goast.Node) bool {
			if !addsBothArraySizeConstants(node) {
				return true
			}
			found = append(found, fmt.Sprintf("%s in %s", fset.Position(node.Pos()), fn.Name.Name))
			return true
		})
	}
	return found
}

// addsBothArraySizeConstants reports whether node adds the two constants
// together, whether written with + or through saturatingAdd, in either order.
func addsBothArraySizeConstants(node goast.Node) bool {
	switch expr := node.(type) {
	case *goast.BinaryExpr:
		if expr.Op != token.ADD {
			return false
		}
		return isDistinctArraySizePair(expr.X, expr.Y)
	case *goast.CallExpr:
		ident, ok := expr.Fun.(*goast.Ident)
		if !ok || ident.Name != "saturatingAdd" || len(expr.Args) != 2 {
			return false
		}
		return isDistinctArraySizePair(expr.Args[0], expr.Args[1])
	}
	return false
}

// isDistinctArraySizePair reports whether the two operands are the two
// constants, one each. Doubling either one is not this mistake.
func isDistinctArraySizePair(left, right goast.Expr) bool {
	leftName, leftOK := arraySizeConstantName(left)
	rightName, rightOK := arraySizeConstantName(right)
	return leftOK && rightOK && leftName != rightName
}

func arraySizeConstantName(expr goast.Expr) (string, bool) {
	ident, ok := expr.(*goast.Ident)
	if !ok {
		return "", false
	}
	_, isSizeConstant := arraySizeConstants[ident.Name]
	return ident.Name, isSizeConstant
}
