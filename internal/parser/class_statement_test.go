package parser

import (
	"strings"
	"testing"
)

func TestParserRejectsSingletonClassSyntax(t *testing.T) {
	t.Parallel()
	_, errs := Parse(`
class << self
  def build
    1
  end
end
`)
	if len(errs) == 0 {
		t.Fatal("expected parse error for class << self")
	}
	if !strings.Contains(errs[0].Error(), "class << self definitions are not supported; use def self.name") {
		t.Fatalf("unexpected parse error: %v", errs[0])
	}
}
