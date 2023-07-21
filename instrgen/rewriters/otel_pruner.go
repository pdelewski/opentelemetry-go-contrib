package rewriters

import (
	"go/ast"
	"go/token"
	"golang.org/x/tools/go/ast/astutil"
	"os"
	"strings"
)

func removeStmt(slice []ast.Stmt, s int) []ast.Stmt {
	return append(slice[:s], slice[s+1:]...)
}

func removeField(slice []*ast.Field, s int) []*ast.Field {
	return append(slice[:s], slice[s+1:]...)
}

func removeExpr(slice []ast.Expr, s int) []ast.Expr {
	return append(slice[:s], slice[s+1:]...)
}

func inspectFuncContent(fType *ast.FuncType, fBody *ast.BlockStmt) {
	for index := 0; index < len(fType.Params.List); index++ {
		param := fType.Params.List[index]
		for _, ident := range param.Names {
			if strings.Contains(ident.Name, "__atel_") {
				fType.Params.List = removeField(fType.Params.List, index)
				index--
			}
		}
	}
	for index := 0; index < len(fBody.List); index++ {
		stmt := fBody.List[index]
		switch bodyStmt := stmt.(type) {
		case *ast.AssignStmt:
			if ident, ok := bodyStmt.Lhs[0].(*ast.Ident); ok {
				if strings.Contains(ident.Name, "__atel_") {
					fBody.List = removeStmt(fBody.List, index)
					index--
				}
			}
			if ident, ok := bodyStmt.Rhs[0].(*ast.Ident); ok {
				if strings.Contains(ident.Name, "__atel_") {
					fBody.List = removeStmt(fBody.List, index)
					index--
				}
			}
		case *ast.ExprStmt:
			if call, ok := bodyStmt.X.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if strings.Contains(sel.Sel.Name, "SetTracerProvider") {
						fBody.List = removeStmt(fBody.List, index)
						index--
					}
					if strings.Contains(sel.Sel.Name, "InstrgenSetTls") {
						fBody.List = removeStmt(fBody.List, index)
						index--
					}
				}
			}
		case *ast.DeferStmt:
			if sel, ok := bodyStmt.Call.Fun.(*ast.SelectorExpr); ok {
				if strings.Contains(sel.Sel.Name, "Shutdown") {
					if ident, ok := sel.X.(*ast.Ident); ok {
						if strings.Contains(ident.Name, "rtlib") {
							fBody.List = removeStmt(fBody.List, index)
							index--
						}
					}
				}
				if ident, ok := sel.X.(*ast.Ident); ok {
					if strings.Contains(ident.Name, "__atel_") {
						fBody.List = removeStmt(fBody.List, index)
						index--
					}
				}
			}
		}
	}
}

type OtelPruner struct {
	FilePattern string
	Replace     bool
}

func (OtelPruner) Id() string {
	return "Pruner"
}

func (pruner OtelPruner) Inject(pkg string, filepath string) bool {
	return strings.Contains(filepath, pruner.FilePattern)
}

func (pruner OtelPruner) ReplaceSource(pkg string, filePath string) bool {
	return pruner.Replace
}

func (OtelPruner) Rewrite(pkg string, file *ast.File, fset *token.FileSet, trace *os.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.FuncDecl:
			if x.Body != nil {
				inspectFuncContent(x.Type, x.Body)
			}
		case *ast.CallExpr:
			for argIndex := 0; argIndex < len(x.Args); argIndex++ {
				if ident, ok := x.Args[argIndex].(*ast.Ident); ok {
					if strings.Contains(ident.Name, "__atel_") {
						x.Args = removeExpr(x.Args, argIndex)
						argIndex--
					}
				}
			}
			for argIndex := 0; argIndex < len(x.Args); argIndex++ {
				if c, ok := x.Args[argIndex].(*ast.CallExpr); ok {
					if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
						if ident, ok := sel.X.(*ast.Ident); ok {
							if strings.Contains(ident.Name, "__atel_") {
								x.Args = removeExpr(x.Args, argIndex)
								argIndex--
							}
						}
					}
				}
			}
		case *ast.FuncLit:
			inspectFuncContent(x.Type, x.Body)
		case *ast.TypeSpec:
			iface, ok := x.Type.(*ast.InterfaceType)
			if !ok {
				return true
			}
			for _, method := range iface.Methods.List {
				funcType, ok := method.Type.(*ast.FuncType)
				if !ok {
					continue
				}
				for argIndex := 0; argIndex < len(funcType.Params.List); argIndex++ {
					for _, ident := range funcType.Params.List[argIndex].Names {
						if strings.Contains(ident.Name, "__atel_") {
							funcType.Params.List = removeField(funcType.Params.List, argIndex)
							argIndex--
						}
					}
				}
			}
		}
		return true
	})
	astutil.DeleteNamedImport(fset, file, "__atel_context", "context")
	astutil.DeleteNamedImport(fset, file, "__atel_otel", "go.opentelemetry.io/otel")
	astutil.DeleteNamedImport(fset, file, "__atel_runtime", "runtime")

}

func (OtelPruner) WriteExtraFiles(pkg string, destPath string) []string {
	return nil
}
