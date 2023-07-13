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
	"golang.org/x/tools/go/loader"
	"strings"
)

// FuncDescriptor stores an information about
// id, type and if function requires custom instrumentation.
type FuncDescriptor struct {
	PackageName  string
	Receiver     string
	FunctionName string
	FuncType     string
	FileName     string
	Line         int
}

type InterfaceImplMapping = map[string][]*types.Var

// FuncsInfo stores an information about
// function declarations
type FuncsInfo struct {
	FuncDecls            map[FuncDescriptor]bool
	InterfaceImplMapping InterfaceImplMapping
}

func makeFuncsInfo() FuncsInfo {
	return FuncsInfo{
		FuncDecls:            make(map[FuncDescriptor]bool),
		InterfaceImplMapping: make(map[string][]*types.Var),
	}
}

func (fd FuncDescriptor) Id() string {
	recvStr := fd.Receiver
	return fd.PackageName + recvStr + "." + fd.FunctionName + "." + fd.FuncType
}

func (fd FuncDescriptor) TypeHash() string {
	recvStr := fd.Receiver
	return fd.PackageName + recvStr + "." + fd.FunctionName + "." + fd.FuncType
}

func GetInterfaces(defs map[*ast.Ident]types.Object) map[string]types.Object {
	interfaces := make(map[string]types.Object)
	for id, obj := range defs {
		if obj == nil || obj.Type() == nil {
			continue
		}
		if _, ok := obj.(*types.TypeName); !ok {
			continue
		}
		if types.IsInterface(obj.Type()) {
			interfaces[id.Name] = obj
		}
	}
	return interfaces
}

func isAny(obj types.Object) bool {
	return obj.Type().String() == "any" || obj.Type().Underlying().String() == "any"
}

func getInterfaceNameForReceiver(interfaces map[string]types.Object, recv *types.Var) []types.Object {
	var interfacesMap []types.Object
	for _, obj := range interfaces {
		if t, ok := obj.Type().Underlying().(*types.Interface); ok {
			if types.Implements(recv.Type(), t) && !isAny(obj) {
				interfacesMap = append(interfacesMap, obj)
			}
		}
	}
	return interfacesMap
}

func findRootFunctions(prog *loader.Program, file *ast.File, ginfo *types.Info, interfaces map[string]types.Object, functionLabel string, rootFunctions *[]FuncDescriptor) {
	var parentFunc FuncDescriptor
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			position := prog.Fset.Position(ginfo.Defs[node.Name].Pos())
			ftype := ginfo.Defs[node.Name].Type()
			signature := ftype.(*types.Signature)
			receiver := signature.Recv()
			var receiverStr string
			if receiver != nil {
				receiverStr = receiver.Type().String()
			}
			parentFunc = FuncDescriptor{ginfo.Defs[node.Name].Pkg().String(),
				receiverStr, node.Name.String(), ftype.String(),
				position.Filename, position.Line}

		case *ast.CallExpr:
			selector, ok := node.Fun.(*ast.SelectorExpr)
			if ok {
				if selector.Sel.Name == functionLabel {
					*rootFunctions = append(*rootFunctions, parentFunc)
				}
			}
		}
		return true
	})
}

func findFuncDecls(prog *loader.Program, file *ast.File,
	ginfo *types.Info, interfaces map[string]types.Object,
	funcsInfo FuncsInfo) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			ftype := ginfo.Defs[node.Name].Type()
			signature := ftype.(*types.Signature)
			receiver := signature.Recv()

			var interfaceObjs []types.Object

			var receiverStr string
			if receiver != nil {
				receiverStr = receiver.Type().String()
				interfaceObjs = getInterfaceNameForReceiver(interfaces, receiver)
			}
			for _, interfaceObj := range interfaceObjs {
				funcsInfo.InterfaceImplMapping[interfaceObj.Type().String()] = append(funcsInfo.InterfaceImplMapping[interfaceObj.Type().String()], receiver)
			}
			position := prog.Fset.Position(node.Name.Pos())
			funcDecl := FuncDescriptor{ginfo.Defs[node.Name].Pkg().String(),
				receiverStr, node.Name.String(), ftype.String(),
				position.Filename, position.Line}
			funcsInfo.FuncDecls[funcDecl] = true
		}
		return true
	})
}

func DumpFuncDecls(funcDecls map[FuncDescriptor]bool) {
	fmt.Println("FuncDecls")
	for fun, _ := range funcDecls {
		fmt.Println(fun)
	}
}

func addFuncCallToCallGraph(funcCall FuncDescriptor, currentFun FuncDescriptor,
	funcDecls map[FuncDescriptor]bool, backwardCallGraph map[FuncDescriptor][]FuncDescriptor) {
	if !Contains(backwardCallGraph[funcCall], currentFun) {
		if _, ok := funcDecls[funcCall]; ok {
			backwardCallGraph[funcCall] = append(backwardCallGraph[funcCall], currentFun)
		}
	}
}

func buildCallGraph(prog *loader.Program, file *ast.File, ginfo *types.Info,
	funcsInfo FuncsInfo,
	backwardCallGraph map[FuncDescriptor][]FuncDescriptor) {
	currentFun := FuncDescriptor{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			ftype := ginfo.Defs[node.Name].Type()
			signature := ftype.(*types.Signature)
			recv := signature.Recv()
			var recvStr string
			if recv != nil {
				recvStr = recv.Type().String()
			}
			position := prog.Fset.Position(ginfo.Defs[node.Name].Pos())
			currentFun = FuncDescriptor{ginfo.Defs[node.Name].Pkg().String(),
				recvStr, node.Name.String(), ftype.String(),
				position.Filename, position.Line}
		case *ast.CallExpr:
			switch node := node.Fun.(type) {
			case *ast.Ident:
				ftype := ginfo.Uses[node].Type()
				pkg := ""
				if ginfo.Uses[node].Pkg() != nil {
					pkg = ginfo.Uses[node].Pkg().String()
				}
				position := prog.Fset.Position(ginfo.Uses[node].Pos())
				if ftype != nil {
					funcCall := FuncDescriptor{pkg, "", node.Name, ftype.String(),
						position.Filename, position.Line}
					addFuncCallToCallGraph(funcCall, currentFun, funcsInfo.FuncDecls, backwardCallGraph)
				}
			case *ast.SelectorExpr:
				obj := ginfo.Selections[node]
				if obj != nil {
					recv := obj.Recv()
					// sel.Sel is function ident
					ftype := ginfo.Uses[node.Sel]
					var ftypeStr string
					if ftype != nil {
						ftypeStr = ftype.Type().String()
					}
					pkg := ""
					if obj.Obj().Pkg() != nil {
						pkg = obj.Obj().Pkg().String()
					}
					position := prog.Fset.Position(ginfo.Uses[node.Sel].Pos())
					funcCall := FuncDescriptor{pkg, recv.String(), obj.Obj().Name(), ftypeStr,
						position.Filename, position.Line}
					for _, impl := range funcsInfo.InterfaceImplMapping[recv.String()] {
						implFuncCall := FuncDescriptor{impl.Pkg().String(), impl.Type().String(),
							obj.Obj().Name(), ftypeStr,
							position.Filename, position.Line}
						addFuncCallToCallGraph(implFuncCall, currentFun, funcsInfo.FuncDecls, backwardCallGraph)
					}
					addFuncCallToCallGraph(funcCall, currentFun, funcsInfo.FuncDecls, backwardCallGraph)
				}
			}
		}
		return true

	})
}

func DumpFuncCalls(prog *loader.Program, file *ast.File, ginfo *types.Info) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.CallExpr:
			switch node := node.Fun.(type) {
			case *ast.Ident:
				position := prog.Fset.Position(ginfo.Uses[node].Pos())
				ftype := ginfo.Uses[node].Type()
				if ftype != nil {
					funcCall := FuncDescriptor{ginfo.Defs[node].Pkg().String(), "", node.Name,
						ftype.String(), position.Filename, position.Line}
					fmt.Println("FuncCall:", funcCall)
				}
			case *ast.SelectorExpr:
				obj := ginfo.Selections[node]
				if obj != nil {
					recv := obj.Recv()
					var ftypeStr string
					// sel.Sel is function ident
					ftype := ginfo.Uses[node.Sel]

					if ftype != nil {
						ftypeStr = ftype.Type().String()
					}
					var recvStr string
					if len(recv.String()) > 0 {
						recvStr = "." + recv.String()
					}
					position := prog.Fset.Position(ginfo.Uses[node.Sel].Pos())
					funcCall := FuncDescriptor{obj.Obj().Pkg().String(), recvStr,
						obj.Obj().Name(), ftypeStr, position.Filename, position.Line}
					fmt.Println("FuncCall:", funcCall)
				}

			}
		}
		return true

	})
}

func DumpCallGraph(backwardCallGraph map[FuncDescriptor][]FuncDescriptor) {
	fmt.Println("\n\tchild parent")
	for k, v := range backwardCallGraph {
		fmt.Print("\n\t", k.Id())
		fmt.Print(" ", v)
	}
	fmt.Print("\n")
}

func FindRootFunctions(prog *loader.Program, ginfo *types.Info, interfaces map[string]types.Object, allowedPathPattern string) []FuncDescriptor {
	var rootFunctions []FuncDescriptor
	for _, pkg := range prog.AllPackages {

		//fmt.Printf("Package path %q\n", pkg.Pkg.Path())
		for _, file := range pkg.Files {
			if allowedPathPattern != "" && !strings.Contains(prog.Fset.Position(file.Name.Pos()).String(), allowedPathPattern) {
				continue
			}
			//fmt.Println(prog.Fset.Position(file.Name.Pos()).String())
			findRootFunctions(prog, file, ginfo, interfaces, "AutotelEntryPoint", &rootFunctions)
		}
	}
	return rootFunctions
}

func FindFuncDecls(prog *loader.Program, ginfo *types.Info, interfaces map[string]types.Object, allowedPathPattern string) FuncsInfo {
	funcsInfo := makeFuncsInfo()
	for _, pkg := range prog.AllPackages {

		//fmt.Printf("Package path %q\n", pkg.Pkg.Path())
		for _, file := range pkg.Files {
			if allowedPathPattern != "" && !strings.Contains(prog.Fset.Position(file.Name.Pos()).String(), allowedPathPattern) {
				continue
			}
			//fmt.Println(prog.Fset.Position(file.Name.Pos()).String())
			findFuncDecls(prog, file, ginfo, interfaces, funcsInfo)
		}
	}
	return funcsInfo
}

func BuildCallGraph(prog *loader.Program, ginfo *types.Info,
	funcsInfo FuncsInfo, allowedPathPattern string) map[FuncDescriptor][]FuncDescriptor {
	backwardCallGraph := make(map[FuncDescriptor][]FuncDescriptor)
	for _, pkg := range prog.AllPackages {
		//fmt.Printf("Package path %q\n", pkg.Pkg.Path())
		for _, file := range pkg.Files {
			if allowedPathPattern != "" && !strings.Contains(prog.Fset.Position(file.Name.Pos()).String(), allowedPathPattern) {
				continue
			}
			//fmt.Println(prog.Fset.Position(file.Name.Pos()).String())
			buildCallGraph(prog, file, ginfo, funcsInfo, backwardCallGraph)
		}
	}
	return backwardCallGraph
}
