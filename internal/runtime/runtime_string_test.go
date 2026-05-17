package runtime

import (
	"testing"
)

func TestStringHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      ["  hello  ".strip(), "hi".upcase(), "BYE".downcase(), "a b c".split()]
    end

    def split_custom()
      "a,b,c".split(",")
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindArray {
		t.Fatalf("expected array, got %v", result.Kind())
	}
	arr := result.Array()
	if len(arr) != 4 {
		t.Fatalf("unexpected length: %d", len(arr))
	}
	if arr[0].String() != "hello" {
		t.Fatalf("strip mismatch: %s", arr[0].String())
	}
	if arr[1].String() != "HI" {
		t.Fatalf("upcase mismatch: %s", arr[1].String())
	}
	if arr[2].String() != "bye" {
		t.Fatalf("downcase mismatch: %s", arr[2].String())
	}
	compareArrays(t, arr[3], []Value{NewString("a"), NewString("b"), NewString("c")})

	customSplit := callFunc(t, script, "split_custom", nil)
	compareArrays(t, customSplit, []Value{NewString("a"), NewString("b"), NewString("c")})
}

func TestStringPredicatesAndLength(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      {
        empty_true: "".empty?,
        empty_false: "hello".empty?,
        starts_true: "hello".start_with?("he"),
        starts_false: "hello".start_with?("lo"),
        ends_true: "hello".end_with?("lo"),
        ends_false: "hello".end_with?("he"),
        length_alias: "héllo".length,
        size: "héllo".size
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["empty_true"].Bool() {
		t.Fatalf("expected empty_true to be true")
	}
	if got["empty_false"].Bool() {
		t.Fatalf("expected empty_false to be false")
	}
	if !got["starts_true"].Bool() {
		t.Fatalf("expected starts_true to be true")
	}
	if got["starts_false"].Bool() {
		t.Fatalf("expected starts_false to be false")
	}
	if !got["ends_true"].Bool() {
		t.Fatalf("expected ends_true to be true")
	}
	if got["ends_false"].Bool() {
		t.Fatalf("expected ends_false to be false")
	}
	if got["length_alias"].Int() != 5 {
		t.Fatalf("length mismatch: %v", got["length_alias"])
	}
	if got["size"].Int() != 5 {
		t.Fatalf("size mismatch: %v", got["size"])
	}
}

func TestStringBoundaryHelpers(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      {
        lstrip: "  hello\t".lstrip,
        rstrip: "\thello  ".rstrip,
        chomp_nl: "line\n".chomp,
        chomp_none: "line".chomp,
        chomp_custom: "path///".chomp("/"),
        chomp_empty_sep: "line\n\n".chomp(""),
        delete_prefix_hit: "unhappy".delete_prefix("un"),
        delete_prefix_miss: "happy".delete_prefix("un"),
        delete_suffix_hit: "report.csv".delete_suffix(".csv"),
        delete_suffix_miss: "report.csv".delete_suffix(".txt")
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	wantStrings := map[string]string{
		"lstrip":             "hello\t",
		"rstrip":             "\thello",
		"chomp_nl":           "line",
		"chomp_none":         "line",
		"chomp_custom":       "path//",
		"chomp_empty_sep":    "line",
		"delete_prefix_hit":  "happy",
		"delete_prefix_miss": "happy",
		"delete_suffix_hit":  "report",
		"delete_suffix_miss": "report.csv",
	}
	for key, want := range wantStrings {
		if got[key].String() != want {
			t.Fatalf("%s mismatch: %q, want %q", key, got[key].String(), want)
		}
	}
}

func TestStringSearchAndSlice(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      text = "héllo hello"
      {
        include_true: text.include?("llo"),
        include_false: text.include?("zzz"),
        index_hit: text.index("llo"),
        index_offset_hit: text.index("llo", 6),
        index_miss: text.index("zzz"),
        rindex_hit: text.rindex("llo"),
        rindex_offset_hit: text.rindex("llo", 4),
        rindex_miss: text.rindex("zzz"),
        slice_char: text.slice(1),
        slice_range: text.slice(1, 4),
        slice_oob: text.slice(99),
        slice_negative_len: text.slice(1, -1),
        slice_huge_len: text.slice(1, 9223372036854775807)
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if !got["include_true"].Bool() {
		t.Fatalf("include_true mismatch")
	}
	if got["include_false"].Bool() {
		t.Fatalf("include_false mismatch")
	}
	if got["index_hit"].Int() != 2 {
		t.Fatalf("index_hit mismatch: %v", got["index_hit"])
	}
	if got["index_offset_hit"].Int() != 8 {
		t.Fatalf("index_offset_hit mismatch: %v", got["index_offset_hit"])
	}
	if got["index_miss"].Kind() != KindNil {
		t.Fatalf("index_miss expected nil, got %v", got["index_miss"])
	}
	if got["rindex_hit"].Int() != 8 {
		t.Fatalf("rindex_hit mismatch: %v", got["rindex_hit"])
	}
	if got["rindex_offset_hit"].Int() != 2 {
		t.Fatalf("rindex_offset_hit mismatch: %v", got["rindex_offset_hit"])
	}
	if got["rindex_miss"].Kind() != KindNil {
		t.Fatalf("rindex_miss expected nil, got %v", got["rindex_miss"])
	}
	if got["slice_char"].String() != "é" {
		t.Fatalf("slice_char mismatch: %q", got["slice_char"].String())
	}
	if got["slice_range"].String() != "éllo" {
		t.Fatalf("slice_range mismatch: %q", got["slice_range"].String())
	}
	if got["slice_oob"].Kind() != KindNil {
		t.Fatalf("slice_oob expected nil, got %v", got["slice_oob"])
	}
	if got["slice_negative_len"].Kind() != KindNil {
		t.Fatalf("slice_negative_len expected nil, got %v", got["slice_negative_len"])
	}
	if got["slice_huge_len"].String() != "éllo hello" {
		t.Fatalf("slice_huge_len mismatch: %q", got["slice_huge_len"].String())
	}
}

func TestStringTransforms(t *testing.T) {
	t.Parallel()
	script := compileScript(t, `
    def helpers()
      original = "  hello  "
      ids = "ID-12 ID-34"
      template_context = {
        user: { name: "Alex", score: 42 },
        id: :p_1,
        missing_nil: nil
      }
      {
        bytesize: "hé".bytesize,
        ord: "hé".ord,
        chr: "hé".chr,
        chr_empty: "".chr,
        capitalize: "hÉLLo wORLD".capitalize,
        capitalize_bang: "hÉLLo wORLD".capitalize!,
        capitalize_bang_nochange: "Hello".capitalize!,
        swapcase: "Hello VIBE".swapcase,
        swapcase_bang: "Hello VIBE".swapcase!,
        upcase_bang_nochange: "HELLO".upcase!,
        reverse: "héllo".reverse,
        reverse_bang: "héllo".reverse!,
        sub_one: "bananas".sub("na", "NA"),
        sub_bang: "bananas".sub!("na", "NA"),
        sub_bang_nochange: "bananas".sub!("zz", "NA"),
        sub_miss: "bananas".sub("zz", "NA"),
        sub_regex: ids.sub("ID-[0-9]+", "X", regex: true),
        sub_regex_capture: ids.sub("ID-([0-9]+)", "X-$1", regex: true),
        sub_regex_boundary_short: "ba".sub("\\Ba", "X", regex: true),
        sub_regex_boundary: "foo".sub("\\Boo", "X", regex: true),
        sub_regex_boundary_full: "xfooy".sub("\\Bfoo\\B", "X", regex: true),
        gsub_all: "bananas".gsub("na", "NA"),
        gsub_bang: "bananas".gsub!("na", "NA"),
        gsub_bang_nochange: "bananas".gsub!("zz", "NA"),
        gsub_regex: ids.gsub("ID-[0-9]+", "X", regex: true),
        match: ids.match("ID-([0-9]+)"),
        match_optional_nil: "ID".match("(ID)(-([0-9]+))?"),
        match_miss: ids.match("ZZZ"),
        scan: ids.scan("ID-[0-9]+"),
        clear: "hello".clear,
        concat: "he".concat("llo", "!"),
        concat_noop: "hello".concat,
        replace: "old".replace("new"),
        strip_bang: original.strip!,
        strip_bang_nochange: "hello".strip!,
        squish: "  hello \n\t world  ".squish,
        squish_bang: "  hello \n\t world  ".squish!,
        squish_bang_nochange: "hello world".squish!,
        template_basic: "Hello {{name}}".template({ name: "Alex" }),
        template_nested: "Player {{user.name}} scored {{user.score}}".template(template_context),
        template_symbol: "ID={{id}}".template(template_context),
        template_nil: "Value={{missing_nil}}".template(template_context),
        template_missing_passthrough: "Hello {{missing}}".template({ name: "Alex" }),
        template_spacing: "Hello {{ name }}".template({ name: "Alex" }),
        template_multiple: "{{name}}/{{name}}".template({ name: "Alex" }),
        original_unchanged: original
      }
    end
    `)

	result := callFunc(t, script, "helpers", nil)
	if result.Kind() != KindHash {
		t.Fatalf("expected hash, got %v", result.Kind())
	}
	got := result.Hash()
	if got["bytesize"].Int() != 3 {
		t.Fatalf("bytesize mismatch: %v", got["bytesize"])
	}
	if got["ord"].Int() != 104 {
		t.Fatalf("ord mismatch: %v", got["ord"])
	}
	if got["chr"].String() != "h" {
		t.Fatalf("chr mismatch: %q", got["chr"].String())
	}
	if got["chr_empty"].Kind() != KindNil {
		t.Fatalf("chr_empty expected nil, got %v", got["chr_empty"])
	}

	stringChecks := map[string]string{
		"capitalize":                   "Héllo world",
		"capitalize_bang":              "Héllo world",
		"swapcase":                     "hELLO vibe",
		"swapcase_bang":                "hELLO vibe",
		"reverse":                      "olléh",
		"reverse_bang":                 "olléh",
		"sub_one":                      "baNAnas",
		"sub_bang":                     "baNAnas",
		"sub_miss":                     "bananas",
		"sub_regex":                    "X ID-34",
		"sub_regex_capture":            "X-12 ID-34",
		"sub_regex_boundary_short":     "bX",
		"sub_regex_boundary":           "fX",
		"sub_regex_boundary_full":      "xXy",
		"gsub_all":                     "baNANAs",
		"gsub_bang":                    "baNANAs",
		"gsub_regex":                   "X X",
		"clear":                        "",
		"concat":                       "hello!",
		"concat_noop":                  "hello",
		"replace":                      "new",
		"strip_bang":                   "hello",
		"squish":                       "hello world",
		"squish_bang":                  "hello world",
		"template_basic":               "Hello Alex",
		"template_nested":              "Player Alex scored 42",
		"template_symbol":              "ID=p_1",
		"template_nil":                 "Value=",
		"template_missing_passthrough": "Hello {{missing}}",
		"template_spacing":             "Hello Alex",
		"template_multiple":            "Alex/Alex",
		"original_unchanged":           "  hello  ",
	}
	for key, want := range stringChecks {
		if got[key].String() != want {
			t.Fatalf("%s mismatch: %q, want %q", key, got[key].String(), want)
		}
	}

	nilChecks := []string{
		"capitalize_bang_nochange",
		"upcase_bang_nochange",
		"sub_bang_nochange",
		"gsub_bang_nochange",
		"strip_bang_nochange",
		"squish_bang_nochange",
	}
	for _, key := range nilChecks {
		if got[key].Kind() != KindNil {
			t.Fatalf("%s expected nil, got %v", key, got[key])
		}
	}

	compareArrays(t, got["match"], []Value{NewString("ID-12"), NewString("12")})
	compareArrays(t, got["match_optional_nil"], []Value{NewString("ID"), NewString("ID"), NewNil(), NewNil()})
	if got["match_miss"].Kind() != KindNil {
		t.Fatalf("match_miss expected nil, got %v", got["match_miss"])
	}
	compareArrays(t, got["scan"], []Value{NewString("ID-12"), NewString("ID-34")})
}
