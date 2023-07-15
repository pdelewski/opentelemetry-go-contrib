package rewriters

import (
	"go/ast"
	"go/token"
	"os"
)

func inspectFuncs(pkg string, file *ast.File, fset *token.FileSet, trace *os.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		if funDeclNode, ok := n.(*ast.FuncDecl); ok {

			trace.WriteString("Package:" + pkg + " FuncDecl:" + fset.Position(funDeclNode.Pos()).String() + file.Name.Name + "." + funDeclNode.Name.String())
			trace.WriteString("\n")
		}
		return true
	})
}

type CommonRewriter struct {
}

func (CommonRewriter) Inject(pkg string, filepath string) bool {

	return true
}

func (CommonRewriter) ReplaceSource(pkg string, filePath string) bool {
	return false
}

func (CommonRewriter) Rewrite(pkg string, file *ast.File, fset *token.FileSet, trace *os.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		if funDeclNode, ok := n.(*ast.FuncDecl); ok {
			trace.WriteString("Package:" + pkg + " FuncDecl:" + fset.Position(funDeclNode.Pos()).String() + ":" + file.Name.Name + "." + funDeclNode.Name.String())
			trace.WriteString("\n")
		}
		return true
	})
}

func (CommonRewriter) WriteExtraFiles(pkg string, filePath string, destPath string) {

}
