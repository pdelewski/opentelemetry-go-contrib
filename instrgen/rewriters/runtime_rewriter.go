package rewriters

import (
	"go/ast"
	"go/token"
	"log"
	"os"
)

type RuntimeRewriter struct {
	ProjectPath    string
	PackagePattern string
}

func (RuntimeRewriter) Id() string {
	return "runtime"
}

func (RuntimeRewriter) Inject(pkg string, filepath string) bool {

	return pkg == "runtime"
}

func (RuntimeRewriter) ReplaceSource(pkg string, filePath string) bool {
	return false
}

func (RuntimeRewriter) Rewrite(pkg string, file *ast.File, fset *token.FileSet, trace *os.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.TypeSpec:
			if n.Name != nil && n.Name.Name != "g" {
				return false
			}
			st, ok := n.Type.(*ast.StructType)
			if !ok {
				return false
			}

			s1 := &ast.Field{
				Names: []*ast.Ident{
					&ast.Ident{
						Name: "_tls_instrgen",
					},
				},
				Type: &ast.Ident{
					Name: "interface{}",
				},
			}
			st.Fields.List = append(st.Fields.List, s1)
		case *ast.FuncDecl:
			if n.Name.Name != "newproc1" {
				return false
			}
			if len(n.Type.Results.List) != 1 {
				return false
			}
			if len(n.Type.Params.List) != 3 {
				return false
			}
			deferStmt := &ast.DeferStmt{
				Defer: 27,
				Call: &ast.CallExpr{
					Fun: &ast.FuncLit{
						Type: &ast.FuncType{
							Func:   33,
							Params: &ast.FieldList{},
						},
						Body: &ast.BlockStmt{
							List: []ast.Stmt{
								&ast.AssignStmt{
									Lhs: []ast.Expr{
										&ast.SelectorExpr{
											X: &ast.Ident{
												Name: "instrgen_result",
											},
											Sel: &ast.Ident{
												Name: "_tls_instrgen",
											},
										},
									},
									Tok: token.ASSIGN,
									Rhs: []ast.Expr{
										&ast.SelectorExpr{
											X: &ast.Ident{
												Name: "callergp",
											},
											Sel: &ast.Ident{
												Name: "_tls_instrgen",
											},
										},
									},
								},
							},
						},
					},
					Lparen:   94,
					Ellipsis: 0,
				},
			}

			n.Body.List = append([]ast.Stmt{deferStmt}, n.Body.List...)
			n.Type.Results.List[0].Names = append(n.Type.Results.List[0].Names, &ast.Ident{
				Name: "instrgen_result",
			})
		}

		return true
	})
}

func (RuntimeRewriter) WriteExtraFiles(pkg string, destPath string) {
	ctx_propagation := `package runtime

func InstrgenGetTls interface{} {
        return getg().m.curg._tls_instrgen
}

func InstrgenSetTls(tls interface{}) {
        getg().m.curg._tls_instrgen = tls
}
`
	f, err := os.Create(destPath + "/" + "instrgen_tls.go")
	if err != nil {
		log.Fatal(err)
		return
	}
	f.WriteString(ctx_propagation)
}
