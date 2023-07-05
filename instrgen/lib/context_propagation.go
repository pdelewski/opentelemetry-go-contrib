// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package lib // import "go.opentelemetry.io/contrib/instrgen/lib"

import (
	"fmt"
	"go/ast"
	"go/types"
)

func isFunPartOfCallGraph(fun FuncDescriptor, callgraph map[FuncDescriptor][]FuncDescriptor) bool {
	// TODO this is not optimap o(n)
	for k, v := range callgraph {
		if k.TypeHash() == fun.TypeHash() {
			return true
		}
		for _, e := range v {
			if fun.TypeHash() == e.TypeHash() {
				return true
			}
		}
	}
	return false
}

// ContextPropagationPass.
type ContextPropagationPass struct {
}

// Execute.
func (pass *ContextPropagationPass) Execute(
	node *ast.File,
	analysis *PackageAnalysis) []Import {
	var imports []Import
	addImports := false
	// below variable is used
	// when callexpr is inside var decl
	// instead of functiondecl
	currentFun := FuncDescriptor{}
	emitEmptyContext := func(callExpr *ast.CallExpr, fun FuncDescriptor, ctxArg *ast.Ident) {
		addImports = true
		if currentFun != (FuncDescriptor{}) {
			visited := map[FuncDescriptor]bool{}
			if isPath(analysis.Callgraph, currentFun, analysis.RootFunctions[0], visited) {
				callExpr.Args = append([]ast.Expr{ctxArg}, callExpr.Args...)
			} else {
				contextTodo := &ast.CallExpr{
					Fun: &ast.SelectorExpr{
						X: &ast.Ident{
							Name: "__atel_context",
						},
						Sel: &ast.Ident{
							Name: "TODO",
						},
					},
					Lparen:   62,
					Ellipsis: 0,
				}
				callExpr.Args = append([]ast.Expr{contextTodo}, callExpr.Args...)
			}
			return
		}
		contextTodo := &ast.CallExpr{
			Fun: &ast.SelectorExpr{
				X: &ast.Ident{
					Name: "__atel_context",
				},
				Sel: &ast.Ident{
					Name: "TODO",
				},
			},
			Lparen:   62,
			Ellipsis: 0,
		}
		callExpr.Args = append([]ast.Expr{contextTodo}, callExpr.Args...)
	}
	emitCallExpr := func(ident *ast.Ident, n ast.Node, ctxArg *ast.Ident) {
		if callExpr, ok := n.(*ast.CallExpr); ok {
			if analysis.GInfo.Uses[ident] == nil {
				return
			}
			ftype := analysis.GInfo.Uses[ident].Type()
			if ftype == nil {
				return
			}
			funcCall := FuncDescriptor{node.Name.Name, "", ident.Name, ftype.String()}
			found := analysis.FuncDecls[funcCall]

			// inject context parameter only
			// to these functions for which function decl
			// exists

			if found {
				visited := map[FuncDescriptor]bool{}
				if isPath(analysis.Callgraph, funcCall, analysis.RootFunctions[0], visited) {
					fmt.Println("\t\t\tContextPropagation FuncCall:", funcCall, ftype)
					emitEmptyContext(callExpr, funcCall, ctxArg)
				}
			}
		}
	}
	ast.Inspect(node, func(n ast.Node) bool {
		ctxArg := &ast.Ident{
			Name: "__atel_child_tracing_ctx",
		}
		ctxField := &ast.Field{
			Names: []*ast.Ident{
				{
					Name: "__atel_tracing_ctx",
				},
			},
			Type: &ast.SelectorExpr{
				X: &ast.Ident{
					Name: "__atel_context",
				},
				Sel: &ast.Ident{
					Name: "Context",
				},
			},
		}
		switch xNode := n.(type) {
		case *ast.FuncDecl:
			if analysis.GInfo.Defs[xNode.Name] == nil {
				return false
			}
			ftype := analysis.GInfo.Defs[xNode.Name].Type()
			signature := ftype.(*types.Signature)
			recv := signature.Recv()

			var recvStr string
			if recv != nil {
				recvStr = "." + recv.Type().String()
			}
			fun := FuncDescriptor{node.Name.Name, recvStr, xNode.Name.String(), ftype.String()}
			currentFun = fun
			// inject context only
			// functions available in the call graph
			if !isFunPartOfCallGraph(fun, analysis.Callgraph) {
				break
			}
			if Contains(analysis.RootFunctions, fun) {
				break
			}

			visited := map[FuncDescriptor]bool{}

			if isPath(analysis.Callgraph, fun, analysis.RootFunctions[0], visited) {
				fmt.Println("\t\t\tContextPropagation FuncDecl:", fun,
					ftype)
				addImports = true
				xNode.Type.Params.List = append([]*ast.Field{ctxField}, xNode.Type.Params.List...)
			}
		case *ast.CallExpr:
			if ident, ok := xNode.Fun.(*ast.Ident); ok {
				emitCallExpr(ident, n, ctxArg)
			}

			if sel, ok := xNode.Fun.(*ast.SelectorExpr); ok {
				obj := analysis.GInfo.Selections[sel]
				if obj != nil {
					recv := obj.Recv()
					var ftypeStr string
					// sel.Sel is function ident
					ftype := analysis.GInfo.Uses[sel.Sel]

					if ftype != nil {
						ftypeStr = ftype.Type().String()
					}
					var recvStr string
					if len(recv.String()) > 0 {
						recvStr = "." + recv.String()
					}
					funcCall := FuncDescriptor{node.Name.Name, recvStr, obj.Obj().Name(), ftypeStr}
					found := analysis.FuncDecls[funcCall]

					// inject context parameter only
					// to these functions for which function decl
					// exists

					if found {
						visited := map[FuncDescriptor]bool{}
						if isPath(analysis.Callgraph, funcCall, analysis.RootFunctions[0], visited) {
							fmt.Println("\t\t\tContextPropagation FuncCall:", funcCall, ftype)
							emitEmptyContext(xNode, funcCall, ctxArg)
						}
					}
				}
			}

		case *ast.TypeSpec:
			/*
				iname := xNode.Name
				iface, ok := xNode.Type.(*ast.InterfaceType)
				if !ok {
					return true
				}
				for _, method := range iface.Methods.List {
					funcType, ok := method.Type.(*ast.FuncType)
					if !ok {
						return true
					}
					visited := map[FuncDescriptor]bool{}
					pkgPath := GetPkgNameFromDefsTable(pkg, method.Names[0])
					funId := pkgPath + "." + iname.Name + "." + pkg.TypesInfo.Defs[method.Names[0]].Name()
					fun := FuncDescriptor{
						Id:       funId,
						DeclType: pkg.TypesInfo.Defs[method.Names[0]].Type().String()}
					if isPath(analysis.Callgraph, fun, analysis.RootFunctions[0], visited) {
						fmt.Println("\t\t\tContext Propagation InterfaceType", fun.Id, fun.DeclType)
						addImports = true
						funcType.Params.List = append([]*ast.Field{ctxField}, funcType.Params.List...)
					}
				}
			*/
		}
		return true
	})
	if addImports {
		imports = append(imports, Import{"__atel_context", "context", Add})
	}
	return imports
}
