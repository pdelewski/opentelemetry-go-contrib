package rewriters

import (
	"go/ast"
	"go/token"
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
		}

		return true
	})
}

func (RuntimeRewriter) WriteExtraFiles(pkg string, filePath string, destPath string) {

}
