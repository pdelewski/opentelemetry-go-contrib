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
	"go/printer"
	"go/token"
	"go/types"
	"golang.org/x/tools/go/loader"
	"os"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
)

// PackageAnalysis analyze all package set accrding to passed
// pattern. It requires an information about path, pattern,
// root functions - entry points, function declarations,
// and so on.
type PackageAnalysis struct {
	ProjectPath    string
	PackagePattern string
	RootFunctions  []FuncDescriptor
	FuncsInfo      FuncsInfo
	Callgraph      map[FuncDescriptor][]FuncDescriptor
	Interfaces     map[string]types.Object
	Prog           *loader.Program
	GInfo          *types.Info
	Debug          bool
}

type importaction int

const (
	// const that tells whether package should be imported.
	Add importaction = iota
	// or removed.
	Remove
)

// Stores an information about operations on packages.
// Currently packages can be imported with an aliases
// or without.
type Import struct {
	NamedPackage string
	Package      string
	ImportAction importaction
}

// FileAnalysisPass executes an analysis for
// specific file node - translation unit.
type FileAnalysisPass interface {
	Execute(node *ast.File,
		analysis *PackageAnalysis) []Import
}

func createFile(name string) (*os.File, error) {
	var out *os.File
	out, err := os.Create(name)
	if err != nil {
		defer out.Close()
	}
	return out, err
}

func addImports(imports []Import, fset *token.FileSet, fileNode *ast.File) {
	for _, imp := range imports {
		if imp.ImportAction == Add {
			if len(imp.NamedPackage) > 0 {
				astutil.AddNamedImport(fset, fileNode, imp.NamedPackage, imp.Package)
			} else {
				astutil.AddImport(fset, fileNode, imp.Package)
			}
		} else {
			if len(imp.NamedPackage) > 0 {
				astutil.DeleteNamedImport(fset, fileNode, imp.NamedPackage, imp.Package)
			} else {
				astutil.DeleteImport(fset, fileNode, imp.Package)
			}
		}
	}
}

// Execute function, main entry point to analysis process.
func (analysis *PackageAnalysis) Execute(pass FileAnalysisPass, fileSuffix string) ([]*ast.File, error) {
	fset := analysis.Prog.Fset
	var fileNodeSet []*ast.File

	for _, pkg := range analysis.Prog.AllPackages {

		//fmt.Printf("Package path %q\n", pkg.Pkg.Path())
		for _, file := range pkg.Files {
			if analysis.PackagePattern != "" && !strings.Contains(analysis.Prog.Fset.Position(file.Name.Pos()).String(), analysis.PackagePattern) {
				continue
			}
			fileNode := file
			fmt.Println("\t\t", fset.File(fileNode.Pos()).Name())
			var out *os.File
			out, err := createFile(fset.File(fileNode.Pos()).Name() + fileSuffix)
			if err != nil {
				return nil, err
			}
			if len(analysis.RootFunctions) == 0 {
				e := printer.Fprint(out, fset, fileNode)
				if e != nil {
					return nil, e
				}
				continue
			}
			imports := pass.Execute(fileNode, analysis)
			addImports(imports, fset, fileNode)
			e := printer.Fprint(out, fset, fileNode)
			if e != nil {
				return nil, e
			}
			if !analysis.Debug {
				oldFileName := fset.File(fileNode.Pos()).Name() + fileSuffix
				newFileName := fset.File(fileNode.Pos()).Name()
				e = os.Rename(oldFileName, newFileName)
				if e != nil {
					return nil, e
				}
			}
			fileNodeSet = append(fileNodeSet, fileNode)
		}
	}
	return fileNodeSet, nil
}
