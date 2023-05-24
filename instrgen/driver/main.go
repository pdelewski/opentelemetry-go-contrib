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
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	alib "go.opentelemetry.io/contrib/instrgen/lib"
	"go/ast"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
)

type UiInject struct {
	Entrypoint string
	Funcset    []string
}

type UiBuild struct {
	BuildArgs string
}

type UiRun struct {
	OtelServiceName            string
	OtelTracesExporter         string
	OtelExporterOtlpEndpoint   string
	OtelExporterZipkinEndpoint string
}

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

func makeAnalysis(projectPath string, packagePattern string, debug bool, instrgenLog *bufio.Writer) *alib.PackageAnalysis {
	var rootFunctions []alib.FuncDescriptor

	interfaces := alib.FindInterfaces(projectPath, packagePattern, instrgenLog)
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint", instrgenLog)...)
	funcDecls := alib.FindFuncDecls(projectPath, packagePattern, interfaces, instrgenLog)
	backwardCallGraph := alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces, instrgenLog)
	fmt.Fprintln(instrgenLog, "\n\tchild parent")
	for k, v := range backwardCallGraph {
		fmt.Fprint(instrgenLog, "\n\t", k)
		fmt.Fprint(instrgenLog, " ", v)
	}
	fmt.Fprintln(instrgenLog, "")
	selectedFunctions := make(map[string]bool)
	for k, _ := range funcDecls {
		selectedFunctions[k.TypeHash()] = true
	}
	analysis := &alib.PackageAnalysis{
		ProjectPath:       projectPath,
		PackagePattern:    packagePattern,
		RootFunctions:     rootFunctions,
		FuncDecls:         funcDecls,
		Callgraph:         backwardCallGraph,
		Interfaces:        interfaces,
		SelectedFunctions: selectedFunctions,
		InstrgenLog:       instrgenLog,
		Debug:             debug}
	return analysis
}

// Prune.
func Prune(projectPath string, packagePattern string, debug bool, instrgenLog *bufio.Writer) ([]*ast.File, error) {
	analysis := makeAnalysis(projectPath, packagePattern, debug, instrgenLog)
	return analysis.Execute(&alib.OtelPruner{}, otelPrunerPassSuffix)
}

func makeCallGraph(projectPath string, packagePattern string, instrgenLog *bufio.Writer) map[alib.FuncDescriptor][]alib.FuncDescriptor {
	var funcDecls map[alib.FuncDescriptor]bool
	var backwardCallGraph map[alib.FuncDescriptor][]alib.FuncDescriptor

	interfaces := alib.FindInterfaces(projectPath, packagePattern, instrgenLog)
	funcDecls = alib.FindFuncDecls(projectPath, packagePattern, interfaces, instrgenLog)
	backwardCallGraph = alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces, instrgenLog)
	return backwardCallGraph
}

func makeRootFunctions(projectPath string, packagePattern string, instrgenLog *bufio.Writer) []alib.FuncDescriptor {
	var rootFunctions []alib.FuncDescriptor
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint", instrgenLog)...)
	return rootFunctions
}

func dumpCallGraph(callGraph map[alib.FuncDescriptor][]alib.FuncDescriptor, instrgenLog *bufio.Writer) {
	fmt.Fprintln(instrgenLog, "\n\tchild parent")
	for k, v := range callGraph {
		fmt.Fprint(instrgenLog, "\n\t", k)
		fmt.Fprint(instrgenLog, " ", v)
	}
}

func dumpRootFunctions(rootFunctions []alib.FuncDescriptor, instrgenLog *bufio.Writer) {
	fmt.Fprintln(instrgenLog, "rootfunctions:")
	for _, fun := range rootFunctions {
		fmt.Fprintln(instrgenLog, "\t"+fun.TypeHash())
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
func executeCommand(command string, projectPath string, packagePattern string, instrgenLog *bufio.Writer) error {
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
		_, err := Prune(projectPath, packagePattern, false, instrgenLog)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPath, packagePattern, false, instrgenLog)
		err = ExecutePasses(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--inject-dump-ir":
		_, err := Prune(projectPath, packagePattern, true, instrgenLog)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPath, packagePattern, true, instrgenLog)
		err = ExecutePassesDumpIr(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--dumpcfg":
		backwardCallGraph := makeCallGraph(projectPath, packagePattern, instrgenLog)
		dumpCallGraph(backwardCallGraph, instrgenLog)
		return nil
	case "--rootfunctions":
		rootFunctions := makeRootFunctions(projectPath, packagePattern, instrgenLog)
		dumpRootFunctions(rootFunctions, instrgenLog)
		return nil
	case "--prune":
		_, err := Prune(projectPath, packagePattern, false, instrgenLog)
		if err != nil {
			return err
		}
		fmt.Println("\tprune done")
		return nil
	case "--generatecfg":
		backwardCallGraph := makeCallGraph(projectPath, packagePattern, instrgenLog)
		alib.GenerateForwardCfg(backwardCallGraph, "cfg")
		return nil
	case "--server":
		server(projectPath, packagePattern, instrgenLog)
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

func reqInject(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
	fmt.Fprintln(instrgenLog, "inject")
	var bodyBytes []byte
	var err error

	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			fmt.Fprintf(instrgenLog, "Body reading error: %v", err)
			return
		}
		defer r.Body.Close()
	}
	var uiReq UiInject
	json.Unmarshal([]byte(bodyBytes), &uiReq)
	selectedFunctions := make(map[string]bool)
	for _, selectedFun := range uiReq.Funcset {
		selectedFunctions[selectedFun] = true
	}
	fmt.Fprintln(instrgenLog, "JsonBody : ", uiReq)
	entryPointFunSignature := strings.Split(uiReq.Entrypoint, ":")
	if len(entryPointFunSignature) < 1 {
		log.Fatal("lack of entry point function")
		return
	}
	rootFuncs := make([]alib.FuncDescriptor, 1)
	rootFuncs[0] = alib.FuncDescriptor{entryPointFunSignature[0], entryPointFunSignature[1], false}

	_, err = Prune(projectPath, packagePattern, false, instrgenLog)
	if err != nil {
		log.Fatal(err)
	}
	var rootFunctions []alib.FuncDescriptor
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint", instrgenLog)...)
	interfaces := alib.FindInterfaces(projectPath, packagePattern, instrgenLog)
	funcDecls := alib.FindFuncDecls(projectPath, packagePattern, interfaces, instrgenLog)
	backwardCallGraph := alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces, instrgenLog)
	fmt.Fprintln(instrgenLog, "\n\tchild parent")
	for k, v := range backwardCallGraph {
		fmt.Fprint(instrgenLog, "\n\t", k)
		fmt.Fprint(instrgenLog, " ", v)
	}
	fmt.Fprintln(instrgenLog, "")
	analysis := &alib.PackageAnalysis{
		ProjectPath:       projectPath,
		PackagePattern:    packagePattern,
		RootFunctions:     rootFunctions,
		FuncDecls:         funcDecls,
		Callgraph:         backwardCallGraph,
		Interfaces:        interfaces,
		SelectedFunctions: selectedFunctions,
		InstrgenLog:       instrgenLog,
		Debug:             false}
	err = ExecutePasses(analysis)
	if err != nil {
		log.Fatal(err)
	}
	{
		// reload
		var rootFunctions []alib.FuncDescriptor
		rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint", instrgenLog)...)
		interfaces := alib.FindInterfaces(projectPath, packagePattern, instrgenLog)
		funcDecls := alib.FindFuncDecls(projectPath, packagePattern, interfaces, instrgenLog)
		backwardCallGraph := alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces, instrgenLog)
		alib.GenerateForwardCfg(backwardCallGraph, "./static/index.html")
		w.WriteHeader(200)
	}
	fmt.Fprintln(instrgenLog, "\tinstrumentation done")
	instrgenLog.Flush()
}

func reqPrune(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
	fmt.Fprintln(instrgenLog, "prune")
	err := executeCommand("--prune", projectPath, packagePattern, instrgenLog)
	if err != nil {
		log.Fatal(err)
	}
	// reload
	var rootFunctions []alib.FuncDescriptor
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPath, packagePattern, "AutotelEntryPoint", instrgenLog)...)
	interfaces := alib.FindInterfaces(projectPath, packagePattern, instrgenLog)
	funcDecls := alib.FindFuncDecls(projectPath, packagePattern, interfaces, instrgenLog)
	backwardCallGraph := alib.BuildCallGraph(projectPath, packagePattern, funcDecls, interfaces, instrgenLog)

	alib.GenerateForwardCfg(backwardCallGraph, "./static/index.html")

	w.WriteHeader(200)
	fmt.Fprintln(instrgenLog, "\tprune done")
	instrgenLog.Flush()
}

func reqBuild(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
	fmt.Fprintln(instrgenLog, "build")
	var bodyBytes []byte
	var err error

	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			fmt.Fprintf(instrgenLog, "Body reading error: %v", err)
			return
		}
		defer r.Body.Close()
	}
	var uiReq UiBuild
	json.Unmarshal([]byte(bodyBytes), &uiReq)
	buildArgs := strings.Split(uiReq.BuildArgs, " ")
	fmt.Fprintln(instrgenLog, buildArgs)
	cmd := exec.Command(buildArgs[0])
	cmd.Args = append(cmd.Args, buildArgs[1:]...)
	cmd.Dir = projectPath
	err = cmd.Run()

	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintln(instrgenLog, "build succeeded")
	instrgenLog.Flush()
}

func takeExeName(value string, a string) string {
	// Get substring after a string.
	pos := strings.LastIndex(value, a)
	if pos == -1 {
		return value
	}
	adjustedPos := pos + len(a)
	if adjustedPos >= len(value) {
		return ""
	}
	return value[adjustedPos:len(value)]
}

func reqRun(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
	fmt.Fprintln(instrgenLog, "run")
	var bodyBytes []byte
	var err error

	if r.Body != nil {
		bodyBytes, err = io.ReadAll(r.Body)
		if err != nil {
			fmt.Fprintf(instrgenLog, "Body reading error: %v", err)
			return
		}
		defer r.Body.Close()
	}
	var uiReq UiRun
	json.Unmarshal([]byte(bodyBytes), &uiReq)
	fmt.Fprintln(instrgenLog, uiReq)

	cmd := exec.Command("go", "list", "-m")
	cmd.Dir = projectPath
	output, _ := cmd.CombinedOutput()

	execName := "./" + takeExeName(string(output), "/")
	execName = strings.Replace(execName, "\n", "", -1)
	fmt.Fprintln(instrgenLog, execName)
	runCmd := exec.Command(execName)
	runCmd.Dir = projectPath

	runCmd.Env = os.Environ()
	runCmd.Env = append(runCmd.Env, "OTEL_SERVICE_NAME="+uiReq.OtelServiceName)
	runCmd.Env = append(runCmd.Env, "OTEL_TRACES_EXPORTER="+uiReq.OtelTracesExporter)
	runCmd.Env = append(runCmd.Env, "OTEL_EXPORTER_OTLP_ENDPOINT="+uiReq.OtelExporterOtlpEndpoint)
	runCmd.Env = append(runCmd.Env, "OTEL_EXPORTER_ZIPKIN_ENDPOINT="+uiReq.OtelExporterZipkinEndpoint)
	err = runCmd.Run()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Fprintln(instrgenLog, "run succeeded")
	instrgenLog.Flush()
}

func reqTerminal(projectPath string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
	//fmt.Fprintln(instrgenLog, "reqTerminal")
	w.WriteHeader(200)

}

func server(projectPath string, packagePattern string, instrgenLog *bufio.Writer) {
	backwardCallGraph := makeCallGraph(projectPath, packagePattern, instrgenLog)
	alib.GenerateForwardCfg(backwardCallGraph, "./static/index.html")

	http.HandleFunc("/inject", func(w http.ResponseWriter, r *http.Request) {
		reqInject(projectPath, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/prune", func(w http.ResponseWriter, r *http.Request) {
		reqPrune(projectPath, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/build", func(w http.ResponseWriter, r *http.Request) {
		reqBuild(projectPath, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		reqRun(projectPath, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/terminal", func(w http.ResponseWriter, r *http.Request) {
		reqTerminal(projectPath, packagePattern, w, r, instrgenLog)
	})
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	http.ListenAndServe(":8090", nil)

}

func main() {
	fmt.Println("autotel compiler")
	logFile, err := os.Create("instrgen.log")
	instrgenWriter := bufio.NewWriter(logFile)
	err = checkArgs(os.Args)
	if err != nil {
		return
	}
	err = executeCommand(os.Args[1], os.Args[2], os.Args[3], instrgenWriter)
	if err != nil {
		log.Fatal(err)
	}
}
