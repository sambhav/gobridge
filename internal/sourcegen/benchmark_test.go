package sourcegen

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// Compare the exact parser modes on a declaration-heavy package. This isolates
// parsing, not disk I/O, formatting, or Go compilation/linking.
func BenchmarkParseDeclarations(b *testing.B) {
	var source strings.Builder
	source.WriteString("package example\n")
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&source, "//gobridge:export\nfunc Operation%d(value int) int { local := value + 1; return local }\n", i)
	}
	text := source.String()
	for _, entry := range []struct {
		name string
		mode parser.Mode
	}{{"resolved", parser.ParseComments}, {"declarations", parser.ParseComments | parser.SkipObjectResolution}} {
		b.Run(entry.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := parser.ParseFile(token.NewFileSet(), "library.go", text, entry.mode); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
