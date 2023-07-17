package rewriters

import (
	"go/ast"
	"go/token"
	"os"
)

type BasicRewriter struct {
	ProjectPath    string
	PackagePattern string
}

func (BasicRewriter) Id() string {
	return "Basic"
}

func (BasicRewriter) Inject(pkg string, filepath string) bool {

	return true
}

func (BasicRewriter) ReplaceSource(pkg string, filePath string) bool {
	return false
}

func (BasicRewriter) Rewrite(pkg string, file *ast.File, fset *token.FileSet, trace *os.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		if funDeclNode, ok := n.(*ast.FuncDecl); ok {
			trace.WriteString("Basic Package:" + pkg + " FuncDecl:" + fset.Position(funDeclNode.Pos()).String() + ":" + file.Name.Name + "." + funDeclNode.Name.String())
			trace.WriteString("\n")
		}
		return true
	})
}

func (BasicRewriter) WriteExtraFiles(pkg string, filePath string, destPath string) {

}
