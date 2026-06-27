package runtime

import "testing"

func TestNumericBasePrefixLiteralsEvaluate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name: "hex literal",
			source: `
				def run
					0x10
				end`,
			want: NewInt(16),
		},
		{
			name: "binary literal",
			source: `
				def run
					0b1010
				end`,
			want: NewInt(10),
		},
		{
			name: "octal literal",
			source: `
				def run
					0o12
				end`,
			want: NewInt(10),
		},
		{
			name: "decimal prefix literal",
			source: `
				def run
					0d12
				end`,
			want: NewInt(12),
		},
		{
			name: "hex with underscores",
			source: `
				def run
					0xDEAD_BEEF
				end`,
			want: NewInt(3735928559),
		},
		{
			name: "based literals participate in arithmetic",
			source: `
				def run
					0x10 + 0b10 + 0o10
				end`,
			want: NewInt(26),
		},
		{
			name: "leading zero stays decimal",
			source: `
				def run
					010
				end`,
			want: NewInt(10),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			got := callFunc(t, script, "run", nil)
			if !got.Equal(tt.want) {
				t.Fatalf("run() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNumericBasePrefixLiteralsReject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "hex without digits",
			source: `
				def run
					0x
				end`,
		},
		{
			name: "octal out of range digit",
			source: `
				def run
					0o8
				end`,
		},
		{
			name: "hex stray letter",
			source: `
				def run
					0x1g
				end`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requireCompileErrorContainsDefault(t, tt.source, "invalid numeric literal")
		})
	}
}
