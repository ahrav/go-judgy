// Package main provides a standalone linter that detects time.Sleep usage
// in test files without the synctest package.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var (
	documentationPath = flag.String("doc", "docs/SYNCTEST_INTEGRATION.md", "Path to synctest documentation")
	verbose           = flag.Bool("v", false, "Verbose output")
)

type issue struct {
	file    string
	line    int
	column  int
	message string
}

func main() {
	flag.Parse()

	// Get the directory to check (default to current directory)
	dir := "."
	if flag.NArg() > 0 {
		dir = flag.Arg(0)
	}

	issues, err := checkDirectory(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Print issues
	if len(issues) > 0 {
		for _, issue := range issues {
			fmt.Printf("%s:%d:%d: %s\n", issue.file, issue.line, issue.column, issue.message)
		}
		os.Exit(1)
	}

	if *verbose {
		fmt.Println("✅ No time.Sleep usage without synctest found in tests")
	}
}

func checkDirectory(dir string) ([]issue, error) {
	var issues []issue

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if d.IsDir() {
			// Skip vendor and .git directories
			if d.Name() == "vendor" || d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}

		// Only check test files
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Skip files that already have synctest in their name
		if strings.Contains(filepath.Base(path), "synctest") {
			return nil
		}

		fileIssues, err := checkFile(path)
		if err != nil {
			if *verbose {
				fmt.Printf("Warning: skipping %s: %v\n", path, err)
			}
			return nil // Continue with other files
		}

		issues = append(issues, fileIssues...)
		return nil
	})

	return issues, err
}

func checkFile(filename string) ([]issue, error) {
	src, err := os.ReadFile(filename) //nolint:gosec // File path is controlled by linter, safe to read
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Check if file imports synctest
	hasSynctestImport := false
	for _, imp := range file.Imports {
		if imp.Path != nil {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "testing/synctest" {
				hasSynctestImport = true
				break
			}
		}
	}

	// If synctest is imported, we assume it's being used properly
	// (a more sophisticated check would verify time.Sleep is inside synctest.Run)
	if hasSynctestImport {
		return nil, nil
	}

	// Look for time.Sleep calls
	var issues []issue
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		if ident.Name == "time" && sel.Sel.Name == "Sleep" {
			pos := fset.Position(call.Pos())
			issues = append(issues, issue{
				file:   filename,
				line:   pos.Line,
				column: pos.Column,
				message: fmt.Sprintf("test uses time.Sleep without importing testing/synctest. "+
					"Consider using synctest.Run() for deterministic timing tests. "+
					"See %s for migration guide", *documentationPath),
			})
		}

		return true
	})

	return issues, nil
}
