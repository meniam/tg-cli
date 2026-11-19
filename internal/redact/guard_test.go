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

// guardException allows a function to embed error text in an RPC message. The
// key is the package directory and the function ("daemon:parseChat"),
// not a line number, so edits above it do not move or hide an exception. The
// count is the number of messages it covers: a new unsafe message in the same
// function is a failure, and so is an exception that nothing uses any more.
type guardException struct {
	reason string
	count  int
}

// Existing validation messages pass through the RPC redaction boundary.
var guardExceptions = map[string]guardException{
	"daemon:parseChat":                        {"chat reference validation", 1},
	"daemon:storeError":                       {"note validation", 1},
	"daemon:(*daemon).resolveStoredReference": {"note validation", 1},
	"daemon:(*daemon).search":                 {"constant store error", 1},
	"tgclient:invalidCursor":                  {"fmt.Sprint of a constant error", 1},
	"tgclient:linkForChat":                    {"link validation", 1},
}

var errorVerb = regexp.MustCompile(`%[-+# 0]*[0-9]*(?:\.[0-9]+)?[vw]`)

// guardEmbedsError reports whether the message expression can carry the text of
// an error: err.Error(), fmt.Sprint*(..., err) or a message built in a variable
// above, in the body of the same function.
func guardEmbedsError(info *types.Info, message ast.Expr, body *ast.BlockStmt) bool {
	errorType := types.Universe.Lookup("error").Type().Underlying().(*types.Interface)
	isError := func(e ast.Expr) bool {
		typ := info.TypeOf(e)
		return typ != nil && types.Implements(typ, errorType)
	}
	var embeds func(expr ast.Node, seen map[types.Object]bool) bool
	embeds = func(expr ast.Node, seen map[types.Object]bool) bool {
		found := false
		ast.Inspect(expr, func(x ast.Node) bool {
			switch v := x.(type) {
			case *ast.Ident:
				obj := info.ObjectOf(v)
				if _, isVar := obj.(*types.Var); !isVar || seen[obj] || body == nil || obj.Parent() == obj.Pkg().Scope() {
					return true
				}
				seen[obj] = true
				ast.Inspect(body, func(y ast.Node) bool {
					assign, ok := y.(*ast.AssignStmt)
					if !ok || len(assign.Lhs) != len(assign.Rhs) {
						return true
					}
					for i, lhs := range assign.Lhs {
						if id, ok := lhs.(*ast.Ident); ok && info.ObjectOf(id) == obj && embeds(assign.Rhs[i], seen) {
							found = true
						}
					}
					return true
				})
			case *ast.CallExpr:
				sel, ok := v.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if sel.Sel.Name == "Error" && len(v.Args) == 0 && isError(sel.X) {
					found = true
					return true
				}
				id, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				imported, ok := info.Uses[id].(*types.PkgName)
				if !ok || imported.Imported().Path() != "fmt" {
					return true
				}
				switch sel.Sel.Name {
				case "Sprint", "Sprintln":
					for _, arg := range v.Args {
						if isError(arg) {
							found = true
						}
					}
				case "Sprintf":
					if len(v.Args) < 2 {
						return true
					}
					// A format that is not a literal is judged by its arguments alone.
					if lit, ok := v.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						format, err := strconv.Unquote(lit.Value)
						if err != nil || !errorVerb.MatchString(format) {
							return true
						}
					}
					for _, arg := range v.Args[1:] {
						if isError(arg) {
							found = true
						}
					}
				}
			}
			return true
		})
		return found
	}
	return embeds(message, map[types.Object]bool{})
}

// guardFuncName names a function the way guardExceptions keys do: "name" or
// "(*Type).name".
func guardFuncName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	switch recv := fd.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := recv.X.(*ast.Ident); ok {
			return "(*" + id.Name + ")." + fd.Name.Name
		}
	case *ast.Ident:
		return recv.Name + "." + fd.Name.Name
	}
	return fd.Name.Name
}

// guardMessage returns the message expression of an rpc.NewError call or an
// rpc.Error literal, or nil for any other node.
func guardMessage(info *types.Info, n ast.Node) ast.Expr {
	rpcPkg := func(x ast.Expr) bool {
		id, ok := x.(*ast.Ident)
		if !ok {
			return false
		}
		p, ok := info.Uses[id].(*types.PkgName)
		return ok && p.Imported().Path() == "tg-cli/internal/rpc"
	}
	switch v := n.(type) {
	case *ast.CallExpr:
		sel, ok := v.Fun.(*ast.SelectorExpr)
		if ok && sel.Sel.Name == "NewError" && len(v.Args) >= 2 && rpcPkg(sel.X) {
			return v.Args[1]
		}
	case *ast.CompositeLit:
		sel, ok := v.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Error" || !rpcPkg(sel.X) {
			return nil
		}
		for _, elt := range v.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Message" {
					return kv.Value
				}
			}
		}
	}
	return nil
}

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
	used := map[string]int{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				pos := pkg.Fset.Position(fd.Pos())
				rel, err := filepath.Rel(root, filepath.Dir(pos.Filename))
				if err != nil {
					t.Fatal(err)
				}
				key := filepath.ToSlash(rel) + ":" + guardFuncName(fd)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					message := guardMessage(pkg.TypesInfo, n)
					if message == nil || !guardEmbedsError(pkg.TypesInfo, message, fd.Body) {
						return true
					}
					used[key]++
					if _, allowed := guardExceptions[key]; !allowed {
						t.Errorf("%s: avoid embedding error text in RPC messages", pkg.Fset.Position(message.Pos()))
					}
					return true
				})
			}
		}
	}
	for key, exception := range guardExceptions {
		if used[key] != exception.count {
			t.Errorf("exception %s (%s) covers %d unsafe messages, expected %d: remove or update it", key, exception.reason, used[key], exception.count)
		}
	}
}
