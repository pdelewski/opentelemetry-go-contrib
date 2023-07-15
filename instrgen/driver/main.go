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
	"go/printer"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/loader"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// Load whole go program.
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
	funcsInfo := alib.FindFuncDecls(prog, ginfo, interfaces, packagePattern)
	alib.DumpFuncDecls(funcsInfo.FuncDecls)
	backwardCallGraph := alib.BuildCallGraph(prog, ginfo, funcsInfo, packagePattern)
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
		FuncsInfo:      funcsInfo,
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
	var backwardCallGraph map[alib.FuncDescriptor][]alib.FuncDescriptor

	interfaces := alib.GetInterfaces(ginfo.Defs)
	funcsInfo := alib.FindFuncDecls(prog, ginfo, interfaces, packagePattern)
	backwardCallGraph = alib.BuildCallGraph(prog, ginfo, funcsInfo, packagePattern)

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

func executePass(args []string) {
	path := args[0]
	args = args[1:]
	cmd := exec.Command(path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e := cmd.Run(); e != nil {
		fmt.Println(e)
	}
}

func GetCommandName(args []string) string {
	if len(args) == 0 {
		return ""
	}

	cmd := filepath.Base(args[0])
	if ext := filepath.Ext(cmd); ext != "" {
		cmd = strings.TrimSuffix(cmd, ext)
	}
	return cmd
}

func inspectFuncs(pkg string, file *ast.File, fset *token.FileSet, trace *os.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		if funDeclNode, ok := n.(*ast.FuncDecl); ok {

			trace.WriteString("Package:" + pkg + " FuncDecl:" + fset.Position(funDeclNode.Pos()).String() + file.Name.Name + "." + funDeclNode.Name.String())
			trace.WriteString("\n")
		}
		return true
	})
}

type PackageObserver interface {
	Inject(pkg string, filepath string) bool
	ReplaceSource(pkg string, filePath string) bool
	Rewrite(pkg string, file *ast.File, fset *token.FileSet)
	WriteExtraFiles(pkg string, filePath string, destPath string)
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
			trace.WriteString("Package:" + pkg + " FuncDecl:" + fset.Position(funDeclNode.Pos()).String() + file.Name.Name + "." + funDeclNode.Name.String())
			trace.WriteString("\n")
		}
		return true
	})
}

func (CommonRewriter) WriteExtraFiles(pkg string, filePath string, destPath string) {

}

func createFile(name string) (*os.File, error) {
	var out *os.File
	out, err := os.Create(name)
	if err != nil {
		defer out.Close()
	}
	return out, err
}

func analyzePackage(pkg string, filePaths map[string]int, trace *os.File, destPath string, args []string) {
	fset := token.NewFileSet()
	rewriter := CommonRewriter{}

	for filePath, index := range filePaths {
		file, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
		if err != nil {
			trace.WriteString(err.Error())
			trace.WriteString("\n")
		}

		if rewriter.Inject(pkg, filePath) {
			rewriter.Rewrite(pkg, file, fset, trace)
		}

		if rewriter.ReplaceSource(pkg, filePath) {
			var out *os.File
			out, err = createFile(fset.File(file.Pos()).Name() + "tmp")
			if err != nil {
				trace.WriteString(err.Error())
				trace.WriteString("\n")
			}
			err = printer.Fprint(out, fset, file)
			if err != nil {
				trace.WriteString(err.Error())
				trace.WriteString("\n")
			}
			oldFileName := fset.File(file.Pos()).Name() + "tmp"
			newFileName := fset.File(file.Pos()).Name()
			err = os.Rename(oldFileName, newFileName)
			if err != nil {
				trace.WriteString(err.Error())
				trace.WriteString("\n")
			}
		} else {
			filename := filepath.Base(filePath)
			out, err := createFile(destPath + "/" + filename)
			if err != nil {
				trace.WriteString(err.Error())
				trace.WriteString("\n")
				continue
			}
			err = printer.Fprint(out, fset, file)
			if err != nil {
				trace.WriteString(err.Error())
				trace.WriteString("\n")
				continue
			}
			args[index] = destPath + "/" + filename

		}
		rewriter.WriteExtraFiles(pkg, filepath.Dir(filePath), destPath)
	}
}

func toolExecMain(args []string) {
	trace, _ := os.OpenFile("args", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	argsLen := len(args)
	var destPath string
	var pkg string
	for i, a := range args {
		// output directory
		if a == "-o" {
			destPath = filepath.Dir(string(args[i+1]))
		}
		// package
		if a == "-p" {
			pkg = string(args[i+1])
		}
		// source files
		if a == "-pack" {
			files := make(map[string]int)
			for j := i + 1; j < argsLen; j++ {
				// omit -asmhdr switch + following header+
				if string(args[j]) == "-asmhdr" {
					j = j + 2
				}
				if !strings.HasSuffix(args[j], ".go") {
					continue
				}
				filePath := args[j]
				files[filePath] = j
			}
			analyzePackage(pkg, files, trace, destPath, args)
		}
	}
	executePass(args[0:])
}

func executeCommandProxy(cmdName string) {
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

func main() {
	args := os.Args[1:]
	cmdName := GetCommandName(args)
	if cmdName != "compile" {
		switch cmdName {
		case "--inject":
		case "--prune":
		case "--inject-dump-ir":
		case "--dumpcfg":
		case "--rootfunctions":
			executeCommandProxy(cmdName)
			return

		}
		executePass(args[0:])
		return
	}
	toolExecMain(args)
}
