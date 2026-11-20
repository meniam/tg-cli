package redact

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

// TestAudit06_GuardDetector runs the guard's detector on small sources, so the
// guard cannot go blind on a pattern without a failing test.
func TestAudit06_GuardDetector(t *testing.T) {
	cases := []struct {
		name string
		body string // statements of a function with err error; the last assigns sink
		want bool
	}{
		{"err.Error()", `sink = err.Error()`, true},
		{"Sprintf with %v", `sink = fmt.Sprintf("failed: %v", err)`, true},
		{"Sprintf with %w", `sink = fmt.Sprintf("failed: %w", err)`, true},
		{"Sprint", `sink = fmt.Sprint(err)`, true},
		{"Sprintln", `sink = fmt.Sprintln("x", err)`, true},
		{"message built in a variable", `msg := fmt.Sprintf("failed: %v", err); sink = msg`, true},
		{"message extended in a variable", `msg := "failed: "; msg += err.Error(); sink = msg`, true},
		{"Sprintf of a variable format", `format := "failed: %v"; sink = fmt.Sprintf(format, err)`, true},
		{"plain text", `sink = "invalid chat"`, false},
		{"Sprintf without an error", `sink = fmt.Sprintf("limit %d", 5)`, false},
		{"Sprintf with a verb for a string", `sink = fmt.Sprintf("name %v", "x")`, false},
		{"variable of plain text", `msg := "invalid"; sink = msg`, false},
		{"Error on a non-error", `type t struct{}; sink = fmt.Sprint(t{})`, false},
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil) // shared: it caches the standard library
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\nimport \"fmt\"\nvar _ = fmt.Sprint\nvar sink any\nfunc f(err error) {\n" + tc.body + "\n}\n"
			file, err := parser.ParseFile(fset, "p.go", src, 0)
			if err != nil {
				t.Fatal(err)
			}
			info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
			conf := types.Config{Importer: imp}
			if _, err := conf.Check("p", fset, []*ast.File{file}, info); err != nil {
				t.Fatal(err)
			}
			fn := file.Decls[len(file.Decls)-1].(*ast.FuncDecl)
			last := fn.Body.List[len(fn.Body.List)-1].(*ast.AssignStmt)
			if got := audit06GuardEmbedsError(info, last.Rhs[0], fn.Body); got != tc.want {
				t.Fatalf("audit06GuardEmbedsError = %v, want %v", got, tc.want)
			}
		})
	}
}
