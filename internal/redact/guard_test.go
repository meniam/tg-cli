package redact

import (
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"golang.org/x/tools/go/packages"
)

// Existing validation messages pass through the RPC redaction boundary.
var guardExceptions = map[string]string{
	"daemon/read.go:29":         "chat reference validation",
	"daemon/notes.go:18":        "note validation",
	"daemon/notes.go:44":        "note validation",
	"daemon/notes.go:258":       "constant store error",
	"tgclient/discovery.go:424": "link validation",
}

var errorVerb = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[vw]`)

func TestRPCErrorMessagesDoNotEmbedErrors(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &packages.Config{Dir: root, Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		t.Fatal("cannot type-check packages for RPC error guard")
	}
	errorType := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				var message ast.Expr
				switch v := n.(type) {
				case *ast.CallExpr:
					sel, ok := v.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "NewError" || len(v.Args) < 2 {
						break
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok {
						break
					}
					p, ok := pkg.TypesInfo.Uses[id].(*types.PkgName)
					if ok && p.Imported().Path() == "tg-cli/internal/rpc" {
						message = v.Args[1]
					}
				case *ast.CompositeLit:
					sel, ok := v.Type.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Error" {
						break
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok {
						break
					}
					p, ok := pkg.TypesInfo.Uses[id].(*types.PkgName)
					if !ok || p.Imported().Path() != "tg-cli/internal/rpc" {
						break
					}
					for _, elt := range v.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Message" {
								message = kv.Value
							}
						}
					}
				}
				if message == nil {
					return true
				}
				unsafe := false
				ast.Inspect(message, func(x ast.Node) bool {
					call, ok := x.(*ast.CallExpr)
					if !ok {
						return true
					}
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Error" {
						if typ := pkg.TypesInfo.TypeOf(sel.X); typ != nil && types.Implements(typ, errorType) {
							unsafe = true
						}
					}
					sel, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Sprintf" || len(call.Args) < 2 {
						return true
					}
					id, ok := sel.X.(*ast.Ident)
					if !ok {
						return true
					}
					imported, ok := pkg.TypesInfo.Uses[id].(*types.PkgName)
					if !ok || imported.Imported().Path() != "fmt" {
						return true
					}
					lit, ok := call.Args[0].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						return true
					}
					format, err := strconv.Unquote(lit.Value)
					if err != nil || !errorVerb.MatchString(format) {
						return true
					}
					for _, arg := range call.Args[1:] {
						if typ := pkg.TypesInfo.TypeOf(arg); typ != nil && types.Implements(typ, errorType) {
							unsafe = true
						}
					}
					return true
				})
				pos := pkg.Fset.Position(message.Pos())
				rel, err := filepath.Rel(root, pos.Filename)
				if err != nil {
					t.Error(err)
					return true
				}
				key := filepath.ToSlash(rel) + ":" + strconv.Itoa(pos.Line)
				if unsafe && guardExceptions[key] == "" {
					t.Errorf("%s: avoid embedding error text in RPC messages", pos)
				}
				return true
			})
		}
	}
}
