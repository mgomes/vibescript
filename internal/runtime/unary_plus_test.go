package runtime

import "testing"

func TestUnaryPlus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
		want   Value
	}{
		{
			name: "integer literal",
			source: `
				def run
					+1
				end`,
			want: NewInt(1),
		},
		{
			name: "negative integer literal",
			source: `
				def run
					+(-3)
				end`,
			want: NewInt(-3),
		},
		{
			name: "float literal",
			source: `
				def run
					+1.5
				end`,
			want: NewFloat(1.5),
		},
		{
			name: "string literal",
			source: `
				def run
					+"x"
				end`,
			want: NewString("x"),
		},
		{
			name: "parenthesized expression",
			source: `
				def run
					+(3 + 4)
				end`,
			want: NewInt(7),
		},
		{
			name: "variable",
			source: `
				def run
					x = 5
					+x
				end`,
			want: NewInt(5),
		},
		{
			name: "string variable",
			source: `
				def run
					s = "hi"
					+s
				end`,
			want: NewString("hi"),
		},
	}

	for _, tt := range tests {
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

func TestUnaryPlusUnsupportedReceiver(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source string
	}{
		{
			name: "boolean",
			source: `
				def run
					+true
				end`,
		},
		{
			name: "nil",
			source: `
				def run
					+nil
				end`,
		},
		{
			name: "array",
			source: `
				def run
					+[1, 2]
				end`,
		},
		{
			name: "hash",
			source: `
				def run
					+({a: 1})
				end`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			script := compileScript(t, tt.source)
			requireCallErrorContains(t, script, "run", nil, CallOptions{}, "unsupported unary + operand")
		})
	}
}
