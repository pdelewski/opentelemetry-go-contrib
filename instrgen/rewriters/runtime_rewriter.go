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
		if funDeclNode, ok := n.(*ast.FuncDecl); ok {
			_ = funDeclNode
			trace.WriteString("RuntimeRewriter Package:" + pkg + " FuncDecl:" + fset.Position(funDeclNode.Pos()).String() + ":" + file.Name.Name + "." + funDeclNode.Name.String())
			trace.WriteString("\n")

		}
		return true
	})
}

func (RuntimeRewriter) WriteExtraFiles(pkg string, filePath string, destPath string) {

}
