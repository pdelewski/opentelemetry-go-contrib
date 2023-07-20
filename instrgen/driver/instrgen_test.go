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

//go:build !windows

package main

import (
	"fmt"
	alib "go.opentelemetry.io/contrib/instrgen/lib"
	"go.opentelemetry.io/contrib/instrgen/rewriters"
	"os"
	"testing"
)

var testcases = map[string]string{
	"./testdata/basic":     "./testdata/expected/basic",
	"./testdata/interface": "./testdata/expected/interface",
}

var failures []string

func TestInstrumentation(t *testing.T) {
	cwd, _ := os.Getwd()
	_ = cwd
	var args []string
	for k, _ := range testcases {
		filePaths := make(map[string]int)

		files := alib.SearchFiles(k, ".go")
		for index, file := range files {
			fmt.Println(file)
			filePaths[file] = index
		}
		pruner := rewriters.OtelPruner{ProjectPath: k,
			PackagePattern: k[2:], Replace: true}
		analyzePackage(pruner, "main", filePaths, nil, "", args)

		rewriter := rewriters.BasicRewriter{ProjectPath: k,
			PackagePattern: k[2:], Replace: "yes"}
		analyzePackage(rewriter, "main", filePaths, nil, "", args)

	}
}
