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

package main

import (
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/types"
	"golang.org/x/tools/go/loader"
	"log"
	"os"
	"path/filepath"
	"sync"

	alib "go.opentelemetry.io/contrib/instrgen/lib"
)

func usage() error {
	fmt.Println("\nusage driver --command [path to go project] [package pattern]")
	fmt.Println("\tcommand:")
	fmt.Println("\t\tinject                                 (injects open telemetry calls into project code)")
	fmt.Println("\t\tinject-dump-ir                         (injects open telemetry calls into project code and intermediate passes)")
	fmt.Println("\t\tprune                                  (prune open telemetry calls")
	fmt.Println("\t\tdumpcfg                                (dumps control flow graph)")
	fmt.Println("\t\trootfunctions                          (dumps root functions)")
	return nil
}

func LoadProgram(projectPath string, ginfo *types.Info) (*loader.Program, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	conf := loader.Config{ParserMode: parser.ParseComments}
	conf.Build = &build.Default
	conf.Build.CgoEnabled = false
	conf.Build.Dir = filepath.Join(cwd, projectPath)
	conf.Import(projectPath)
	var mutex = &sync.RWMutex{}
	conf.AfterTypeCheck = func(info *loader.PackageInfo, files []*ast.File) {
		for k, v := range info.Defs {
			mutex.Lock()
			ginfo.Defs[k] = v
			mutex.Unlock()
		}
		for k, v := range info.Uses {
			mutex.Lock()
			ginfo.Uses[k] = v
			mutex.Unlock()
		}
		for k, v := range info.Selections {
			mutex.Lock()
			ginfo.Selections[k] = v
			mutex.Unlock()
		}
	}
	return conf.Load()

}

func makeAnalysis(projectPath string, packagePattern string, prog *loader.Program, ginfo *types.Info, debug bool) *alib.PackageAnalysis {
	var rootFunctions []alib.FuncDescriptor
	interfaces := alib.GetInterfaces(ginfo.Defs)
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(prog, ginfo, interfaces, packagePattern)...)
	funcDecls := alib.FindFuncDecls(prog, ginfo, interfaces, packagePattern)
	alib.DumpFuncDecls(funcDecls)
	backwardCallGraph := alib.BuildCallGraph(prog, ginfo, interfaces, funcDecls, packagePattern)
	fmt.Println("\n\tchild parent")
	for k, v := range backwardCallGraph {
		fmt.Print("\n\t", k)
		fmt.Print(" ", v)
	}
	fmt.Println("")
	analysis := &alib.PackageAnalysis{
		ProjectPath:    projectPath,
		PackagePattern: packagePattern,
		RootFunctions:  rootFunctions,
		FuncDecls:      funcDecls,
		Callgraph:      backwardCallGraph,
		Interfaces:     interfaces,
		GInfo:          ginfo,
		Prog:           prog,
		Debug:          debug}
	return analysis
}

// Prune.
func Prune(projectPath string, packagePattern string, prog *loader.Program, ginfo *types.Info, debug bool) ([]*ast.File, error) {
	analysis := makeAnalysis(projectPath, packagePattern, prog, ginfo, debug)
	return analysis.Execute(&alib.OtelPruner{}, otelPrunerPassSuffix)
}

func makeCallGraph(packagePattern string, prog *loader.Program, ginfo *types.Info) map[alib.FuncDescriptor][]alib.FuncDescriptor {
	var funcDecls map[alib.FuncDescriptor]bool
	var backwardCallGraph map[alib.FuncDescriptor][]alib.FuncDescriptor

	interfaces := alib.GetInterfaces(ginfo.Defs)
	funcDecls = alib.FindFuncDecls(prog, ginfo, interfaces, packagePattern)
	backwardCallGraph = alib.BuildCallGraph(prog, ginfo, interfaces, funcDecls, packagePattern)

	return backwardCallGraph
}

func makeRootFunctions(prog *loader.Program, ginfo *types.Info, packagePattern string) []alib.FuncDescriptor {
	var rootFunctions []alib.FuncDescriptor
	interfaces := alib.GetInterfaces(ginfo.Defs)
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(prog, ginfo, interfaces, packagePattern)...)
	return rootFunctions
}

func dumpCallGraph(callGraph map[alib.FuncDescriptor][]alib.FuncDescriptor) {
	fmt.Println("\n\tchild parent")
	for k, v := range callGraph {
		fmt.Print("\n\t", k)
		fmt.Print(" ", v)
	}
}

func dumpRootFunctions(rootFunctions []alib.FuncDescriptor) {
	fmt.Println("rootfunctions:")
	for _, fun := range rootFunctions {
		fmt.Println("\t" + fun.TypeHash())
	}
}

func isDirectory(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	return fileInfo.IsDir(), err
}

// Parsing algorithm works as follows. It goes through all function
// decls and infer function bodies to find call to AutotelEntryPoint
// A parent function of this call will become root of instrumentation
// Each function call from this place will be instrumented automatically.
func executeCommand(command string, projectPath string, packagePattern string) error {
	isDir, err := isDirectory(projectPath)
	if !isDir {
		_ = usage()
		return errors.New("[path to go project] argument must be directory")
	}
	if err != nil {
		return err
	}
	ginfo := &types.Info{
		Defs:       make(map[*ast.Ident]types.Object),
		Uses:       make(map[*ast.Ident]types.Object),
		Selections: make(map[*ast.SelectorExpr]*types.Selection),
	}

	prog, err := LoadProgram(projectPath, ginfo)
	if err != nil {
		fmt.Println(err)
		return err
	}
	switch command {
	case "--inject":
		_, err := Prune(projectPath, packagePattern, prog, ginfo, false)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPath, packagePattern, prog, ginfo, false)
		err = ExecutePasses(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--inject-dump-ir":
		_, err := Prune(projectPath, packagePattern, prog, ginfo, true)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPath, packagePattern, prog, ginfo, true)
		err = ExecutePassesDumpIr(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--dumpcfg":
		backwardCallGraph := makeCallGraph(packagePattern, prog, ginfo)
		dumpCallGraph(backwardCallGraph)
		return nil
	case "--rootfunctions":
		rootFunctions := makeRootFunctions(prog, ginfo, packagePattern)
		dumpRootFunctions(rootFunctions)
		return nil
	case "--prune":
		_, err := Prune(projectPath, packagePattern, prog, ginfo, false)
		if err != nil {
			return err
		}
		return nil
	default:
		return errors.New("unknown command")
	}
}

func checkArgs(args []string) error {
	if len(args) != 4 {
		_ = usage()
		return errors.New("wrong arguments")
	}
	return nil
}

func main() {
	fmt.Println("autotel compiler")
	err := checkArgs(os.Args)
	if err != nil {
		return
	}
	err = executeCommand(os.Args[1], os.Args[2], os.Args[3])
	if err != nil {
		log.Fatal(err)
	}
}
