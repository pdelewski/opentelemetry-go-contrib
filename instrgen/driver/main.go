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

type UIPrune struct {
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

func makeAnalysis(projectPaths []string, packagePattern string, debug bool, instrgenLog *bufio.Writer) *alib.PackageAnalysis {
	var rootFunctions []alib.FuncDescriptor

	interfaces := alib.FindInterfaces(projectPaths, packagePattern, instrgenLog)
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPaths, packagePattern, "AutotelEntryPoint", instrgenLog)...)
	funcDecls := alib.FindFuncDecls(projectPaths, packagePattern, interfaces, instrgenLog)
	backwardCallGraph := alib.BuildCallGraph(projectPaths, packagePattern, funcDecls, interfaces, instrgenLog)
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
		ProjectPaths:      projectPaths,
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
func Prune(projectPaths []string, packagePattern string, debug bool, instrgenLog *bufio.Writer) ([]*ast.File, error) {
	analysis := makeAnalysis(projectPaths, packagePattern, debug, instrgenLog)
	return analysis.Execute(&alib.OtelPruner{}, otelPrunerPassSuffix)
}

func makeCallGraph(projectPaths []string, packagePattern string, instrgenLog *bufio.Writer) map[alib.FuncDescriptor][]alib.FuncDescriptor {
	var funcDecls map[alib.FuncDescriptor]bool
	var backwardCallGraph map[alib.FuncDescriptor][]alib.FuncDescriptor

	interfaces := alib.FindInterfaces(projectPaths, packagePattern, instrgenLog)
	funcDecls = alib.FindFuncDecls(projectPaths, packagePattern, interfaces, instrgenLog)
	backwardCallGraph = alib.BuildCallGraph(projectPaths, packagePattern, funcDecls, interfaces, instrgenLog)
	return backwardCallGraph
}

func makeRootFunctions(projectPaths []string, packagePattern string, instrgenLog *bufio.Writer) []alib.FuncDescriptor {
	var rootFunctions []alib.FuncDescriptor
	rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPaths, packagePattern, "AutotelEntryPoint", instrgenLog)...)
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
func executeCommand(command string, projectPaths []string, packagePattern string, instrgenLog *bufio.Writer) error {
	for _, projectPath := range projectPaths {
		isDir, err := isDirectory(projectPath)
		if !isDir {
			_ = usage()
			return errors.New("[path to go project] argument must be directory")
		}
		if err != nil {
			return err
		}
	}
	switch command {
	case "--inject":
		_, err := Prune(projectPaths, packagePattern, false, instrgenLog)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPaths, packagePattern, false, instrgenLog)
		err = ExecutePasses(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--inject-dump-ir":
		_, err := Prune(projectPaths, packagePattern, true, instrgenLog)
		if err != nil {
			return err
		}
		analysis := makeAnalysis(projectPaths, packagePattern, true, instrgenLog)
		err = ExecutePassesDumpIr(analysis)
		if err != nil {
			return err
		}
		fmt.Println("\tinstrumentation done")
		return nil
	case "--dumpcfg":
		backwardCallGraph := makeCallGraph(projectPaths, packagePattern, instrgenLog)
		dumpCallGraph(backwardCallGraph, instrgenLog)
		return nil
	case "--rootfunctions":
		rootFunctions := makeRootFunctions(projectPaths, packagePattern, instrgenLog)
		dumpRootFunctions(rootFunctions, instrgenLog)
		return nil
	case "--prune":
		_, err := Prune(projectPaths, packagePattern, false, instrgenLog)
		if err != nil {
			return err
		}
		fmt.Println("\tprune done")
		return nil
	case "--generatecfg":
		backwardCallGraph := makeCallGraph(projectPaths, packagePattern, instrgenLog)
		alib.GenerateForwardCfg(backwardCallGraph, "cfg")
		return nil
	case "--server":
		server(projectPaths, packagePattern, instrgenLog)
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

func reqInject(projectPaths []string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
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
	rootFunctions := make([]alib.FuncDescriptor, 1)
	rootFunctions[0] = alib.FuncDescriptor{entryPointFunSignature[0], entryPointFunSignature[1], false}
	// prune
	{
		interfaces := alib.FindInterfaces(projectPaths, packagePattern, instrgenLog)
		funcDecls := alib.FindFuncDecls(projectPaths, packagePattern, interfaces, instrgenLog)
		backwardCallGraph := alib.BuildCallGraph(projectPaths, packagePattern, funcDecls, interfaces, instrgenLog)
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
			ProjectPaths:      projectPaths,
			PackagePattern:    packagePattern,
			RootFunctions:     rootFunctions,
			FuncDecls:         funcDecls,
			Callgraph:         backwardCallGraph,
			Interfaces:        interfaces,
			SelectedFunctions: selectedFunctions,
			InstrgenLog:       instrgenLog,
			Debug:             false}
		_, err = analysis.Execute(&alib.OtelPruner{}, otelPrunerPassSuffix)
		if err != nil {
			log.Fatal(err)
		}
	}

	interfaces := alib.FindInterfaces(projectPaths, packagePattern, instrgenLog)
	funcDecls := alib.FindFuncDecls(projectPaths, packagePattern, interfaces, instrgenLog)
	backwardCallGraph := alib.BuildCallGraph(projectPaths, packagePattern, funcDecls, interfaces, instrgenLog)
	fmt.Fprintln(instrgenLog, "\n\tchild parent")
	for k, v := range backwardCallGraph {
		fmt.Fprint(instrgenLog, "\n\t", k)
		fmt.Fprint(instrgenLog, " ", v)
	}
	fmt.Fprintln(instrgenLog, "")
	analysis := &alib.PackageAnalysis{
		ProjectPaths:      projectPaths,
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
		rootFunctions = append(rootFunctions, alib.FindRootFunctions(projectPaths, packagePattern, "AutotelEntryPoint", instrgenLog)...)
		interfaces := alib.FindInterfaces(projectPaths, packagePattern, instrgenLog)
		funcDecls := alib.FindFuncDecls(projectPaths, packagePattern, interfaces, instrgenLog)
		backwardCallGraph := alib.BuildCallGraph(projectPaths, packagePattern, funcDecls, interfaces, instrgenLog)
		alib.GenerateForwardCfg(backwardCallGraph, "./static/index.html")
		w.WriteHeader(200)
	}
	fmt.Fprintln(instrgenLog, "\tinstrumentation done")
	instrgenLog.Flush()
}

func reqPrune(projectPaths []string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
	fmt.Fprintln(instrgenLog, "prune")
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
	rootFunctions := make([]alib.FuncDescriptor, 1)
	rootFunctions[0] = alib.FuncDescriptor{entryPointFunSignature[0], entryPointFunSignature[1], false}

	// prune
	{
		interfaces := alib.FindInterfaces(projectPaths, packagePattern, instrgenLog)
		funcDecls := alib.FindFuncDecls(projectPaths, packagePattern, interfaces, instrgenLog)
		backwardCallGraph := alib.BuildCallGraph(projectPaths, packagePattern, funcDecls, interfaces, instrgenLog)
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
			ProjectPaths:      projectPaths,
			PackagePattern:    packagePattern,
			RootFunctions:     rootFunctions,
			FuncDecls:         funcDecls,
			Callgraph:         backwardCallGraph,
			Interfaces:        interfaces,
			SelectedFunctions: selectedFunctions,
			InstrgenLog:       instrgenLog,
			Debug:             false}
		_, err = analysis.Execute(&alib.OtelPruner{}, otelPrunerPassSuffix)
		if err != nil {
			log.Fatal(err)
		}
	}
	// reload
	interfaces := alib.FindInterfaces(projectPaths, packagePattern, instrgenLog)
	funcDecls := alib.FindFuncDecls(projectPaths, packagePattern, interfaces, instrgenLog)
	backwardCallGraph := alib.BuildCallGraph(projectPaths, packagePattern, funcDecls, interfaces, instrgenLog)

	alib.GenerateForwardCfg(backwardCallGraph, "./static/index.html")

	w.WriteHeader(200)
	fmt.Fprintln(instrgenLog, "\tprune done")
	instrgenLog.Flush()
}

func reqBuild(projectPaths []string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
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
	cmd.Dir = projectPaths[0]
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

func reqRun(projectPaths []string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer) {
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
	cmd.Dir = projectPaths[0]
	output, _ := cmd.CombinedOutput()

	execName := "./" + takeExeName(string(output), "/")
	execName = strings.Replace(execName, "\n", "", -1)
	fmt.Fprintln(instrgenLog, execName)
	runCmd := exec.Command(execName)
	runCmd.Dir = projectPaths[0]

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

func reqTerminal(projectPaths []string, packagePattern string, w http.ResponseWriter, r *http.Request, instrgenLog *bufio.Writer, fileOffset *int64) {
	w.WriteHeader(200)
	file, err := os.Open("instrgen.log")
	if err != nil {
		fmt.Println(err)
		return
	}
	defer file.Close()

	fileinfo, err := file.Stat()
	if err != nil {
		fmt.Println(err)
		return
	}
	filesize := int64(fileinfo.Size())
	chunkSize := filesize - *fileOffset
	buffer := make([]byte, chunkSize)

	bytesread, err := file.ReadAt(buffer, *fileOffset)
	*fileOffset += int64(bytesread)
	w.Write(buffer)
}

func server(projectPaths []string, packagePattern string, instrgenLog *bufio.Writer) {
	backwardCallGraph := makeCallGraph(projectPaths, packagePattern, instrgenLog)
	alib.GenerateForwardCfg(backwardCallGraph, "./static/index.html")
	var fileOffset int64
	fileOffset = 0
	http.HandleFunc("/inject", func(w http.ResponseWriter, r *http.Request) {
		reqInject(projectPaths, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/prune", func(w http.ResponseWriter, r *http.Request) {
		reqPrune(projectPaths, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/build", func(w http.ResponseWriter, r *http.Request) {
		reqBuild(projectPaths, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		reqRun(projectPaths, packagePattern, w, r, instrgenLog)
	})
	http.HandleFunc("/terminal", func(w http.ResponseWriter, r *http.Request) {
		reqTerminal(projectPaths, packagePattern, w, r, instrgenLog, &fileOffset)
	})
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	http.ListenAndServe(":8090", nil)

}

func main() {
	fmt.Println("autotel compiler")
	logFile, err := os.Create("instrgen.log")
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()
	instrgenWriter := bufio.NewWriter(logFile)
	err = checkArgs(os.Args)
	if err != nil {
		return
	}
	projectPath := os.Args[2]
	projectPaths := strings.Split(projectPath, ",")
	err = executeCommand(os.Args[1], projectPaths, os.Args[3], instrgenWriter)

	if err != nil {
		log.Fatal(err)
	}
}
