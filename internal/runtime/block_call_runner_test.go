package runtime

import "testing"

func TestBlockCanReuseEnvRejectsBlocksThatCreateClosures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body []Statement
		want bool
	}{
		{
			name: "plain expressions",
			body: []Statement{
				&AssignStmt{
					Target: &Identifier{Name: "total"},
					Value: &BinaryExpr{
						Left:  &Identifier{Name: "value"},
						Right: &IntegerLiteral{Value: 1},
					},
				},
			},
			want: true,
		},
		{
			name: "nested block literal",
			body: []Statement{
				&ExprStmt{
					Expr: &BlockLiteral{
						Body: []Statement{
							&ExprStmt{Expr: &Identifier{Name: "value"}},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "call with trailing block",
			body: []Statement{
				&ExprStmt{
					Expr: &CallExpr{
						Callee: &Identifier{Name: "map"},
						Block:  &BlockLiteral{},
					},
				},
			},
			want: false,
		},
		{
			name: "function definition",
			body: []Statement{
				&FunctionStmt{Name: "inner"},
			},
			want: false,
		},
		{
			name: "class definition",
			body: []Statement{
				&ClassStmt{Name: "Inner"},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := blockCanReuseEnv(&Block{Body: tt.body})
			if got != tt.want {
				t.Fatalf("blockCanReuseEnv() = %t, want %t", got, tt.want)
			}
		})
	}
}
