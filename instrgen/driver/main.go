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
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"

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
	fmt.Println("\t\tgeneratecfg                            (gencfg)")
	fmt.Println("\t\tserver                                 (ui)")
	return nil
}

func makeAnalysis(projectPath string, packagePattern string, debug bool) *alib.PackageAnalysis {
	var rootFunctions []alib.FuncDescriptor

	interfaces := alib.FindInterfaces(projectPath, packagePattern)
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint")...)
	funcDecls := alib.FindFuncDecls(projectPath, packagePattern, interfaces)
	backwardCallGraph := alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces)
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
		Debug:          debug}
	return analysis
}

// Prune.
func Prune(projectPath string, packagePattern string, debug bool) ([]*ast.File, error) {
	analysis := makeAnalysis(projectPath, packagePattern, debug)
	return analysis.Execute(&alib.OtelPruner{}, otelPrunerPassSuffix)
}

func makeCallGraph(projectPath string, packagePattern string) map[alib.FuncDescriptor][]alib.FuncDescriptor {
	var funcDecls map[alib.FuncDescriptor]bool
	var backwardCallGraph map[alib.FuncDescriptor][]alib.FuncDescriptor

	interfaces := alib.FindInterfaces(projectPath, packagePattern)
	funcDecls = alib.FindFuncDecls(projectPath, packagePattern, interfaces)
	backwardCallGraph = alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces)
	return backwardCallGraph
}

func makeRootFunctions(projectPath string, packagePattern string) []alib.FuncDescriptor {
	var rootFunctions []alib.FuncDescriptor
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint")...)
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
	switch command {
	case "--inject":
		_, err := Prune(projectPath, packagePattern, false)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPath, packagePattern, false)
		err = ExecutePasses(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--inject-dump-ir":
		_, err := Prune(projectPath, packagePattern, true)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPath, packagePattern, true)
		err = ExecutePassesDumpIr(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--dumpcfg":
		backwardCallGraph := makeCallGraph(projectPath, packagePattern)
		dumpCallGraph(backwardCallGraph)
		return nil
	case "--rootfunctions":
		rootFunctions := makeRootFunctions(projectPath, packagePattern)
		dumpRootFunctions(rootFunctions)
		return nil
	case "--prune":
		_, err := Prune(projectPath, packagePattern, false)
		if err != nil {
			return err
		}
		return nil
	case "--generatecfg":
		backwardCallGraph := makeCallGraph(projectPath, packagePattern)
		alib.GenerateForwardCfg(backwardCallGraph, "cfg")
		return nil
	case "--server":
		server(projectPath, packagePattern)
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

func reqInject(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request) {
	fmt.Println("inject")
	var bodyBytes []byte
	var err error

	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			fmt.Printf("Body reading error: %v", err)
			return
		}
		defer r.Body.Close()
	}
	entryPointFun := string(bodyBytes)
	entryPointFunSignature := strings.Split(entryPointFun, ":")
	if len(entryPointFunSignature) < 1 {
		log.Fatal("lack of entry point function")
		return
	}
	rootFuncs := make([]alib.FuncDescriptor, 1)
	rootFuncs[0] = alib.FuncDescriptor{entryPointFunSignature[0], entryPointFunSignature[1], false}
	fmt.Println(rootFuncs[0])

	_, err = Prune(projectPath, packagePattern, false)
	if err != nil {
		log.Fatal(err)
	}
	var rootFunctions []alib.FuncDescriptor
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint")...)
	interfaces := alib.FindInterfaces(projectPath, packagePattern)
	funcDecls := alib.FindFuncDecls(projectPath, packagePattern, interfaces)
	backwardCallGraph := alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces)
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
		Debug:          false}
	err = ExecutePasses(analysis)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("\tinstrumentation done")
}

func reqPrune(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request) {
	fmt.Println("prune")
	err := executeCommand("--prune", projectPath, packagePattern)
	if err != nil {
		log.Fatal(err)
	}
}

func reqBuild(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request) {
	fmt.Println("build")
	cmd := exec.Command("go", "build", ".")

	cmd.Dir = projectPath
	err := cmd.Run()

	if err != nil {
		log.Fatal(err)
	}
}

func reqRun(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request) {
	fmt.Println("run")
}

func server(projectPath string, packagePattern string) {
	backwardCallGraph := makeCallGraph(projectPath, packagePattern)
	alib.GenerateForwardCfg(backwardCallGraph, "./static/index.html")

	http.HandleFunc("/inject", func(w http.ResponseWriter, r *http.Request) {
		reqInject(projectPath, packagePattern, w, r)
	})
	http.HandleFunc("/prune", func(w http.ResponseWriter, r *http.Request) {
		reqPrune(projectPath, packagePattern, w, r)
	})
	http.HandleFunc("/build", func(w http.ResponseWriter, r *http.Request) {
		reqBuild(projectPath, packagePattern, w, r)
	})
	http.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		reqRun(projectPath, packagePattern, w, r)
	})
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	http.ListenAndServe(":8090", nil)

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
