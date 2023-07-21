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
	"bytes"
	"fmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	alib "go.opentelemetry.io/contrib/instrgen/lib"
	"go.opentelemetry.io/contrib/instrgen/rewriters"
	"os"
	"path/filepath"
	"testing"
)

var testcases = map[string]string{
	"testdata/basic":     "testdata/expected/basic",
	"testdata/interface": "testdata/expected/interface",
}

var failures []string

func TestInstrumentation(t *testing.T) {
	cwd, _ := os.Getwd()
	var args []string
	for k, _ := range testcases {
		filePaths := make(map[string]int)

		files := alib.SearchFiles(k, ".go")
		for index, file := range files {
			filePaths[file] = index
		}
		pruner := rewriters.OtelPruner{ProjectPath: k,
			FilePattern: k, Replace: true}
		analyzePackage(pruner, "main", filePaths, nil, "", args)

		rewriter := rewriters.BasicRewriter{ProjectPath: k,
			FilePattern: k, Replace: "yes"}
		analyzePackage(rewriter, "main", filePaths, nil, "", args)

	}
	fmt.Println(cwd)

	for k, v := range testcases {
		files := alib.SearchFiles(cwd+"/"+k, ".go")
		expectedFiles := alib.SearchFiles(cwd+"/"+v, ".go")
		numOfFiles := len(expectedFiles)
		fmt.Println("Go Files:", len(files))
		fmt.Println("Expected Go Files:", len(expectedFiles))
		numOfComparisons := 0
		for _, file := range files {
			fmt.Println(filepath.Base(file))
			for _, expectedFile := range expectedFiles {
				fmt.Println(filepath.Base(expectedFile))
				if filepath.Base(file) == filepath.Base(expectedFile) {
					f1, err1 := os.ReadFile(file)
					require.NoError(t, err1)
					f2, err2 := os.ReadFile(expectedFile)
					require.NoError(t, err2)
					if !assert.True(t, bytes.Equal(f1, f2), file) {
						failures = append(failures, file)
					}
					numOfComparisons = numOfComparisons + 1
				}
			}
		}
		if numOfFiles != numOfComparisons {
			fmt.Println("numberOfComparisons:", numOfComparisons)
			panic("not all files were compared")
		}
	}

}
