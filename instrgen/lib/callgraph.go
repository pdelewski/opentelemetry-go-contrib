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
	"bufio"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"strings"

	"golang.org/x/tools/go/packages"
)

// FuncDescriptor stores an information about
// id, type and if function requires custom instrumentation.
type FuncDescriptor struct {
	Id              string
	DeclType        string
	CustomInjection bool
}

// Function TypeHash. Each function is itentified by its
// id and type.
func (fd FuncDescriptor) TypeHash() string {
	return fd.Id + ":" + fd.DeclType
}

// LoadMode. Tells about needed information during analysis.
const LoadMode packages.LoadMode = packages.NeedName |
	packages.NeedTypes |
	packages.NeedSyntax |
	packages.NeedTypesInfo |
	packages.NeedFiles

func getPkgs(projectPath string, packagePattern string, fset *token.FileSet, instrgenLog *bufio.Writer) ([]*packages.Package, error) {
	cfg := &packages.Config{Fset: fset, Mode: LoadMode, Dir: projectPath}
	pkgs, err := packages.Load(cfg, packagePattern)
	var packageSet []*packages.Package
	if err != nil {
		return nil, err
	}
	for _, pkg := range pkgs {
		fmt.Fprintln(instrgenLog, "\t", pkg)
		packageSet = append(packageSet, pkg)
	}
	return packageSet, nil
}

// FindRootFunctions looks for all root functions eg. entry points.
// Currently an entry point is a function that contains call of function
// passed as functionLabel paramaterer.
func FindRootFunctions(projectPath string, packagePattern string, functionLabel string, instrgenLog *bufio.Writer) []FuncDescriptor {
	fset := token.NewFileSet()
	pkgs, _ := getPkgs(projectPath, packagePattern, fset, instrgenLog)
	var currentFun FuncDescriptor
	var rootFunctions []FuncDescriptor
	for _, pkg := range pkgs {
		for _, node := range pkg.Syntax {
			ast.Inspect(node, func(n ast.Node) bool {
				switch xNode := n.(type) {
				case *ast.CallExpr:
					selector, ok := xNode.Fun.(*ast.SelectorExpr)
					if ok {
						if selector.Sel.Name == functionLabel {
							rootFunctions = append(rootFunctions, currentFun)
						}
					}
				case *ast.FuncDecl:
					if pkg.TypesInfo.Defs[xNode.Name] != nil {
						funId := pkg.TypesInfo.Defs[xNode.Name].Pkg().Path() + "." + pkg.TypesInfo.Defs[xNode.Name].Name()
						currentFun = FuncDescriptor{funId, pkg.TypesInfo.Defs[xNode.Name].Type().String(), false}
						fmt.Fprintln(instrgenLog, "\t\t\tFuncDecl:", funId, pkg.TypesInfo.Defs[xNode.Name].Type().String())
					}
				}
				return true
			})
		}
	}
	instrgenLog.Flush()
	return rootFunctions
}

// GetMostInnerAstIdent takes most inner identifier used for
// function call. For a.b.foo(), `b` will be the most inner identifier.
func GetMostInnerAstIdent(inSel *ast.SelectorExpr) *ast.Ident {
	var l []*ast.Ident
	var e ast.Expr
	e = inSel
	for e != nil {
		if _, ok := e.(*ast.Ident); ok {
			l = append(l, e.(*ast.Ident))
			break
		} else if _, ok := e.(*ast.SelectorExpr); ok {
			l = append(l, e.(*ast.SelectorExpr).Sel)
			e = e.(*ast.SelectorExpr).X
		} else if _, ok := e.(*ast.CallExpr); ok {
			e = e.(*ast.CallExpr).Fun
		} else if _, ok := e.(*ast.IndexExpr); ok {
			e = e.(*ast.IndexExpr).X
		} else if _, ok := e.(*ast.UnaryExpr); ok {
			e = e.(*ast.UnaryExpr).X
		} else if _, ok := e.(*ast.ParenExpr); ok {
			e = e.(*ast.ParenExpr).X
		} else if _, ok := e.(*ast.SliceExpr); ok {
			e = e.(*ast.SliceExpr).X
		} else if _, ok := e.(*ast.IndexListExpr); ok {
			e = e.(*ast.IndexListExpr).X
		} else if _, ok := e.(*ast.StarExpr); ok {
			e = e.(*ast.StarExpr).X
		} else if _, ok := e.(*ast.TypeAssertExpr); ok {
			e = e.(*ast.TypeAssertExpr).X
		} else if _, ok := e.(*ast.CompositeLit); ok {
			// TODO dummy implementation
			if len(e.(*ast.CompositeLit).Elts) == 0 {
				e = e.(*ast.CompositeLit).Type
			} else {
				e = e.(*ast.CompositeLit).Elts[0]
			}
		} else if _, ok := e.(*ast.KeyValueExpr); ok {
			e = e.(*ast.KeyValueExpr).Value
		} else {
			// TODO this is uncaught expression
			panic("uncaught expression")
		}
	}
	if len(l) < 2 {
		panic("selector list should have at least 2 elems")
	}
	// caller or receiver is always
	// at position 1, function is at 0
	return l[1]
}

// GetPkgPathFromRecvInterface builds package path taking
// receiver interface into account.
func GetPkgPathFromRecvInterface(pkg *packages.Package,
	pkgs []*packages.Package, funDeclNode *ast.FuncDecl, interfaces map[string]bool) string {
	var pkgPath string
	for _, v := range funDeclNode.Recv.List {
		for _, dependentpkg := range pkgs {
			for _, defs := range dependentpkg.TypesInfo.Defs {
				if defs == nil {
					continue
				}
				if _, ok := defs.Type().Underlying().(*types.Interface); !ok {
					continue
				}
				if len(v.Names) == 0 || pkg.TypesInfo.Defs[v.Names[0]] == nil {
					continue
				}
				funType := pkg.TypesInfo.Defs[v.Names[0]].Type()

				if types.Implements(funType, defs.Type().Underlying().(*types.Interface)) {
					interfaceExists := interfaces[defs.Type().String()]
					if interfaceExists {
						pkgPath = defs.Type().String()
					}
					break
				}
			}
		}
	}
	return pkgPath
}

// GetPkgPathFromFunctionRecv build package path taking function receiver parameters.
func GetPkgPathFromFunctionRecv(pkg *packages.Package,
	pkgs []*packages.Package, funDeclNode *ast.FuncDecl, interfaces map[string]bool) string {
	pkgPath := GetPkgPathFromRecvInterface(pkg, pkgs, funDeclNode, interfaces)
	if len(pkgPath) != 0 {
		return pkgPath
	}
	for _, v := range funDeclNode.Recv.List {
		if len(v.Names) == 0 {
			continue
		}
		funType := pkg.TypesInfo.Defs[v.Names[0]].Type()
		pkgPath = funType.String()
		// We don't care if that's pointer, remove it from
		// type id
		if _, ok := funType.(*types.Pointer); ok {
			pkgPath = strings.TrimPrefix(pkgPath, "*")
		}
		// We don't care if called via index, remove it from
		// type id
		if _, ok := funType.(*types.Slice); ok {
			pkgPath = strings.TrimPrefix(pkgPath, "[]")
		}
	}

	return pkgPath
}

// GetSelectorPkgPath builds packages path according to selector expr.
func GetSelectorPkgPath(sel *ast.SelectorExpr, pkg *packages.Package, pkgPath string) string {
	caller := GetMostInnerAstIdent(sel)
	if caller != nil && pkg.TypesInfo.Uses[caller] != nil {
		if !strings.Contains(pkg.TypesInfo.Uses[caller].Type().String(), "invalid") {
			pkgPath = pkg.TypesInfo.Uses[caller].Type().String()
			// We don't care if that's pointer, remove it from
			// type id
			if _, ok := pkg.TypesInfo.Uses[caller].Type().(*types.Pointer); ok {
				pkgPath = strings.TrimPrefix(pkgPath, "*")
			}
			// We don't care if called via index, remove it from
			// type id
			if _, ok := pkg.TypesInfo.Uses[caller].Type().(*types.Slice); ok {
				pkgPath = strings.TrimPrefix(pkgPath, "[]")
			}
		}
	}
	return pkgPath
}

// GetPkgNameFromUsesTable gets package name from uses table.
func GetPkgNameFromUsesTable(pkg *packages.Package, ident *ast.Ident) string {
	var pkgPath string
	if pkg.TypesInfo.Uses[ident].Pkg() != nil {
		pkgPath = pkg.TypesInfo.Uses[ident].Pkg().Path()
	}
	return pkgPath
}

// GetPkgNameFromDefsTable gets package name from uses table.
func GetPkgNameFromDefsTable(pkg *packages.Package, ident *ast.Ident) string {
	var pkgPath string
	if pkg.TypesInfo.Defs[ident] == nil {
		return pkgPath
	}
	if pkg.TypesInfo.Defs[ident].Pkg() != nil {
		pkgPath = pkg.TypesInfo.Defs[ident].Pkg().Path()
	}
	return pkgPath
}

// GetPkgPathForFunction builds package path, delegates work to
// other helper functions defined above.
func GetPkgPathForFunction(pkg *packages.Package,
	pkgs []*packages.Package, funDecl *ast.FuncDecl, interfaces map[string]bool) string {
	if funDecl.Recv != nil {
		return GetPkgPathFromFunctionRecv(pkg, pkgs, funDecl, interfaces)
	}
	return GetPkgNameFromDefsTable(pkg, funDecl.Name)
}

// BuildCallGraph builds an information about flow graph
// in the following form child->parent.
func BuildCallGraph(
	projectPath string,
	packagePattern string,
	funcDecls map[FuncDescriptor]bool,
	interfaces map[string]bool, instrgenLog *bufio.Writer) map[FuncDescriptor][]FuncDescriptor {
	fset := token.NewFileSet()
	pkgs, _ := getPkgs(projectPath, packagePattern, fset, instrgenLog)
	fmt.Fprint(instrgenLog, "BuildCallGraph")
	currentFun := FuncDescriptor{"nil", "", false}
	backwardCallGraph := make(map[FuncDescriptor][]FuncDescriptor)
	for _, pkg := range pkgs {
		fmt.Fprintln(instrgenLog, "\t", pkg)
		for _, node := range pkg.Syntax {
			fmt.Fprintln(instrgenLog, "\t\t", fset.File(node.Pos()).Name())
			ast.Inspect(node, func(n ast.Node) bool {
				switch xNode := n.(type) {
				case *ast.CallExpr:
					if id, ok := xNode.Fun.(*ast.Ident); ok {
						pkgPath := GetPkgNameFromUsesTable(pkg, id)
						funId := pkgPath + "." + pkg.TypesInfo.Uses[id].Name()
						fmt.Fprintln(instrgenLog, "\t\t\tFuncCall:", funId, pkg.TypesInfo.Uses[id].Type().String(),
							" @called : ",
							fset.File(node.Pos()).Name())
						fun := FuncDescriptor{funId, pkg.TypesInfo.Uses[id].Type().String(), false}
						if !Contains(backwardCallGraph[fun], currentFun) {
							if funcDecls[fun] {
								backwardCallGraph[fun] = append(backwardCallGraph[fun], currentFun)
							}
						}
					}
					if sel, ok := xNode.Fun.(*ast.SelectorExpr); ok {
						if pkg.TypesInfo.Uses[sel.Sel] != nil {
							pkgPath := GetPkgNameFromUsesTable(pkg, sel.Sel)
							if sel.X != nil {
								pkgPath = GetSelectorPkgPath(sel, pkg, pkgPath)
							}
							funId := pkgPath + "." + pkg.TypesInfo.Uses[sel.Sel].Name()
							fmt.Fprintln(instrgenLog, "\t\t\tFuncCall via selector:", funId, pkg.TypesInfo.Uses[sel.Sel].Type().String(),
								" @called : ",
								fset.File(node.Pos()).Name())
							fun := FuncDescriptor{funId, pkg.TypesInfo.Uses[sel.Sel].Type().String(), false}
							if !Contains(backwardCallGraph[fun], currentFun) {
								if funcDecls[fun] {
									backwardCallGraph[fun] = append(backwardCallGraph[fun], currentFun)
								}
							}
						}
					}
				case *ast.FuncDecl:
					if pkg.TypesInfo.Defs[xNode.Name] != nil {
						pkgPath := GetPkgPathForFunction(pkg, pkgs, xNode, interfaces)
						funId := pkgPath + "." + pkg.TypesInfo.Defs[xNode.Name].Name()
						funcDecls[FuncDescriptor{funId, pkg.TypesInfo.Defs[xNode.Name].Type().String(), false}] = true
						currentFun = FuncDescriptor{funId, pkg.TypesInfo.Defs[xNode.Name].Type().String(), false}
						fmt.Fprintln(instrgenLog, "\t\t\tFuncDecl:", funId, pkg.TypesInfo.Defs[xNode.Name].Type().String())
					}
				}
				return true
			})
		}
	}
	instrgenLog.Flush()
	return backwardCallGraph
}

// FindFuncDecls looks for all function declarations.
func FindFuncDecls(projectPath string, packagePattern string, interfaces map[string]bool, instrgenLog *bufio.Writer) map[FuncDescriptor]bool {
	fset := token.NewFileSet()
	pkgs, _ := getPkgs(projectPath, packagePattern, fset, instrgenLog)
	fmt.Fprintln(instrgenLog, "FindFuncDecls")
	funcDecls := make(map[FuncDescriptor]bool)
	for _, pkg := range pkgs {
		fmt.Fprintln(instrgenLog, "\t", pkg)
		for _, node := range pkg.Syntax {
			fmt.Fprintln(instrgenLog, "\t\t", fset.File(node.Pos()).Name())
			ast.Inspect(node, func(n ast.Node) bool {
				if funDeclNode, ok := n.(*ast.FuncDecl); ok {
					pkgPath := GetPkgPathForFunction(pkg, pkgs, funDeclNode, interfaces)
					if pkg.TypesInfo.Defs[funDeclNode.Name] != nil {
						funId := pkgPath + "." + pkg.TypesInfo.Defs[funDeclNode.Name].Name()
						fmt.Fprintln(instrgenLog, "\t\t\tFuncDecl:", funId, pkg.TypesInfo.Defs[funDeclNode.Name].Type().String())
						funcDecls[FuncDescriptor{funId, pkg.TypesInfo.Defs[funDeclNode.Name].Type().String(), false}] = true
					}
				}
				return true
			})
		}
	}
	instrgenLog.Flush()
	return funcDecls
}

// FindInterfaces looks for all interfaces.
func FindInterfaces(projectPath string, packagePattern string, instrgenLog *bufio.Writer) map[string]bool {
	fset := token.NewFileSet()
	pkgs, _ := getPkgs(projectPath, packagePattern, fset, instrgenLog)
	fmt.Fprintln(instrgenLog, "FindInterfaces")
	interaceTable := make(map[string]bool)
	for _, pkg := range pkgs {
		fmt.Fprintln(instrgenLog, "\t", pkg)
		for _, node := range pkg.Syntax {
			fmt.Fprintln(instrgenLog, "\t\t", fset.File(node.Pos()).Name())
			ast.Inspect(node, func(n ast.Node) bool {
				if typeSpecNode, ok := n.(*ast.TypeSpec); ok {
					if _, ok := typeSpecNode.Type.(*ast.InterfaceType); ok {
						fmt.Fprintln(instrgenLog, "\t\t\tInterface:", pkg.TypesInfo.Defs[typeSpecNode.Name].Type().String())
						interaceTable[pkg.TypesInfo.Defs[typeSpecNode.Name].Type().String()] = true
					}
				}
				return true
			})
		}
	}
	instrgenLog.Flush()
	return interaceTable
}

// InferRootFunctionsFromGraph tries to infer entry points from passed call graph.
func InferRootFunctionsFromGraph(callgraph map[FuncDescriptor][]FuncDescriptor) []FuncDescriptor {
	var allFunctions map[FuncDescriptor]bool
	var rootFunctions []FuncDescriptor
	allFunctions = make(map[FuncDescriptor]bool)
	for k, v := range callgraph {
		allFunctions[k] = true
		for _, childFun := range v {
			allFunctions[childFun] = true
		}
	}
	for k := range allFunctions {
		_, exists := callgraph[k]
		if !exists {
			rootFunctions = append(rootFunctions, k)
		}
	}
	return rootFunctions
}

func genTablePreamble(out *os.File, head []byte) {
	out.WriteString("<html>\n")
	out.WriteString("<link rel=\"stylesheet\" href=\"./default.min.css\">\n")
	out.Write(head)
	out.WriteString("\n<body>")
	out.WriteString("\n<div class=\"topnav\">")
	out.WriteString("\n<h2>Instrgen</h2>")
	out.WriteString("\n</div>")
	out.WriteString("\n<div class=\"left\">")
	out.WriteString("\n<h1>CallGraph</h1>")
	out.WriteString("\n&nbsp;<label for=\"entrypointlabel\">EntryPoint:</label>")
	out.WriteString("\n<input type=\"text\" id=\"entrypoint\" name=\"EntryPoint\" required size=\"100\">")
	out.WriteString("\n<table class=\"table table-striped\">")
}

func genTableEpilogue(out *os.File) {
	out.WriteString("\n</table>")
	out.WriteString("\n</div>")

	out.WriteString("\n<div class=\"right\">")
	out.WriteString("\n<h1>Toolbox</h1>")
	out.WriteString("\n&nbsp;&nbsp;<button id=\"selectall\" type=\"button\" class=\"btn\" onclick=\"selectall_clicked()\">Select All</button><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<button id=\"unselectall\" type=\"button\" class=\"btn\" onclick=\"unselectall_clicked()\">Unselect All</button><br><br>")

	out.WriteString("\n&nbsp;&nbsp;<button id=\"inject\" type=\"button\" class=\"btn\" onclick=\"inject_clicked(entrypoint.value)\">Inject</button><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<button id=\"prune\" type=\"button\" class=\"btn\" onclick=\"prune_clicked()\">Prune</button><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<label for=\"buildargslabel\">Build cmd:</label>")
	out.WriteString("\n&nbsp;<input type=\"text\" id=\"buildargs\" class=\"input\" name=\"Build cmd\" value=\"go build .\" required size=\"40\">")
	out.WriteString("\n&nbsp;<button id=\"build\" type=\"button\" class=\"btn\" onclick=\"build_clicked(this.id, buildargs.value)\">Build</button><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<label for=\"envvarslabel\">Start settings</label><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<label for=\"otelservicenamelabel\">OTEL_SERVICE_NAME:</label>")
	out.WriteString("\n&nbsp;&nbsp;&nbsp;&nbsp;<input type=\"text\" id=\"otelservicename\" class=\"input\" name=\"otelservicename\" value=\"instrgen\" required size=\"40\"><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<label for=\"oteltracesexporterlabel\">OTEL_TRACES_EXPORTER:</label>")
	out.WriteString("\n&nbsp;<input type=\"text\" id=\"oteltracesexporter\" class=\"input\" name=\"oteltracesexporter\" value=\"otlp\" required size=\"40\"><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<label for=\"otelexporterendpointlabel\">OTEL_EXPORTER_OTLP_ENDPOINT:</label>")
	out.WriteString("\n&nbsp;&nbsp;<input type=\"text\" id=\"otelexporterendpoint\" class=\"input\" name=\"otelexporterendpoint\" value=\"localhost:4317\" required size=\"30\"><br><br>")
	out.WriteString("\n&nbsp;&nbsp;<label for=\"zipkinexporterendpointlabel\">OTEL_EXPORTER_ZIPKIN_ENDPOINT:</label>")
	out.WriteString("\n<input type=\"text\" id=\"zipkinexporterendpoint\" class=\"input\" name=\"zipkinexporterendpoint\" value=\"http://localhost:9411/api/v2/spans\" required size=\"30\"><br><br>")

	out.WriteString("\n&nbsp;&nbsp;<button id=\"run\" type=\"button\" class=\"btn\" onclick=\"run_clicked(this.id, otelservicename.value, oteltracesexporter.value, otelexporterendpoint.value, zipkinexporterendpoint.value)\">Run</button><br><br>")
	out.WriteString("\n</div>")

	out.WriteString("\n<div id=\"terminaldiv\" class=\"bottom\">")
	out.WriteString("\n<h3>Terminal</h3>")
	out.WriteString("\n<textarea cols=\"2\" rows=\"12\" id=\"terminal\"></textarea>")
	out.WriteString("\n</div>")

	out.WriteString("\n</body>")
	out.WriteString("\n</html>")
}

func GenerateForwardCfg(backwardCallgraph map[FuncDescriptor][]FuncDescriptor, path string) {
	out, err := os.Create(path)
	defer out.Close()
	if err != nil {
		return
	}
	rootFunctions := InferRootFunctionsFromGraph(backwardCallgraph)
	head, _ := os.ReadFile("./static/head.html")

	cfg := ReverseCfg(backwardCallgraph)
	genTablePreamble(out, head)

	for k, _ := range cfg {
		for _, v := range rootFunctions {
			if v.TypeHash() != k.TypeHash() {
				continue
			}
			visited := make(map[FuncDescriptor]bool)
			depth := 1
			GenerateCfgHelper(cfg, k, out, visited, depth)
		}
	}
	genTableEpilogue(out)
}

func GenerateCfgHelper(
	callGraph map[FuncDescriptor][]FuncDescriptor,
	current FuncDescriptor,
	out *os.File,
	visited map[FuncDescriptor]bool, depth int) {

	out.WriteString("\n<tr>")
	out.WriteString("\n<td>")
	for i := 0; i < depth-1; i++ {
		out.WriteString("&nbsp;&nbsp;")
	}
	out.WriteString("\n    <input type=\"checkbox\" id=\"" + current.TypeHash() + "\"" + " name=\"funcselector\" onchange=\"clicked()\" />")
	out.WriteString(current.TypeHash())
	out.WriteString("\n</td>")
	out.WriteString("\n</tr>")

	value, ok := callGraph[current]
	if ok {
		for _, child := range value {
			exists := visited[child]
			if exists {
				continue
			}
			visited[child] = true
			depth = depth + 1
			GenerateCfgHelper(callGraph, child, out, visited, depth)
			depth = depth - 1
		}
	}
}

func ReverseCfg(backwardCallgraph map[FuncDescriptor][]FuncDescriptor) map[FuncDescriptor][]FuncDescriptor {
	cfg := make(map[FuncDescriptor][]FuncDescriptor)
	for k, children := range backwardCallgraph {
		for _, childFun := range children {
			cfg[childFun] = append(cfg[childFun], k)
		}
	}
	return cfg
}
