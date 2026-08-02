package runtime

import (
	"context"
	"fmt"
	"testing"
)

// The #1129 sweep shapes, sized to show the complexity class: under a memory
// quota each doubling of n used to roughly quadruple the wall clock (2.45s at
// n=2000 for the array-of-hashes build), while unmetered runs were linear.
func benchmarkGrowthShape(b *testing.B, src string, quota int) {
	for _, n := range []int{250, 500, 1000, 2000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			script := compileScriptWithConfig(b, Config{MemoryQuotaBytes: quota, StepQuota: Unlimited}, src)
			b.ResetTimer()
			for range b.N {
				if _, err := script.Call(context.Background(), "run", []Value{NewInt(int64(n))}, CallOptions{}); err != nil {
					b.Fatalf("run(%d): %v", n, err)
				}
			}
		})
	}
}

const arrayOfHashesBuildSource = "def run(n)\n  a = []\n  i = 0\n  while i < n\n    a << {id: i, name: \"x\"}\n    i = i + 1\n  end\n  a.length\nend"

const hashReadLoopSource = "def run(n)\n  h = {}\n  i = 0\n  while i < n\n    h[i.to_s] = i\n    i = i + 1\n  end\n  t = 0\n  i = 0\n  while i < n\n    t = t + h[i.to_s]\n    i = i + 1\n  end\n  t\nend"

const arrayReadLoopSource = "def run(n)\n  a = []\n  i = 0\n  while i < n\n    a << i\n    i = i + 1\n  end\n  t = 0\n  i = 0\n  while i < n\n    t = t + a[i]\n    i = i + 1\n  end\n  t\nend"

func BenchmarkArrayOfHashesBuildUnderQuota(b *testing.B) {
	benchmarkGrowthShape(b, arrayOfHashesBuildSource, 64<<20)
}

func BenchmarkArrayOfHashesBuildUnquotaed(b *testing.B) {
	benchmarkGrowthShape(b, arrayOfHashesBuildSource, Unlimited)
}

func BenchmarkHashReadLoopUnderQuota(b *testing.B) {
	benchmarkGrowthShape(b, hashReadLoopSource, 64<<20)
}

func BenchmarkArrayReadLoopUnderQuota(b *testing.B) {
	benchmarkGrowthShape(b, arrayReadLoopSource, 64<<20)
}
