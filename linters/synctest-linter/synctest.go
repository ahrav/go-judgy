// Package main provides a custom linter for golangci-lint that detects
// time.Sleep usage in test files without importing the synctest package.
package main

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("synctest", New)
}

// Settings defines the configuration for the synctest linter.
type Settings struct {
	// DocumentationPath is the path to the synctest documentation file
	DocumentationPath string `json:"documentation-path" mapstructure:"documentation-path"`
}

// PluginSynctest is the synctest linter plugin.
type PluginSynctest struct {
	settings Settings
}

// New creates a new instance of the synctest linter.
func New(settings any) (register.LinterPlugin, error) {
	s, err := register.DecodeSettings[Settings](settings)
	if err != nil {
		return nil, err
	}

	// Set default documentation path if not provided
	if s.DocumentationPath == "" {
		s.DocumentationPath = "docs/SYNCTEST_INTEGRATION.md"
	}

	return &PluginSynctest{
		settings: s,
	}, nil
}

// BuildAnalyzers returns the analyzers for this linter.
func (f *PluginSynctest) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{
		{
			Name: "synctest",
			Doc:  "Checks that test files using time.Sleep import the synctest package",
			Run:  f.run,
		},
	}, nil
}

// GetLoadMode returns the load mode for this linter.
func (f *PluginSynctest) GetLoadMode() string {
	return register.LoadModeTypesInfo
}

// run performs the actual analysis.
func (f *PluginSynctest) run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		// Only check test files
		filename := pass.Fset.Position(file.Pos()).Filename
		if !strings.HasSuffix(filename, "_test.go") {
			continue
		}

		// Skip files that already have synctest in their name
		// These are likely already using synctest properly
		baseName := filepath.Base(filename)
		if strings.Contains(baseName, "synctest") {
			continue
		}

		// Check if file imports synctest package
		hasSynctestImport := f.hasSynctestImport(file)

		// Look for time.Sleep calls
		hasTimeSleep := false
		var timeSleepPos token.Pos
		isInSynctestRun := false

		ast.Inspect(file, func(n ast.Node) bool {
			// Check if we're inside a synctest.Run call
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok {
						if ident.Name == "synctest" && sel.Sel.Name == "Run" {
							// We're in a synctest.Run block, time.Sleep is okay here
							isInSynctestRun = true
						}
					}
				}
			}

			// Check for time.Sleep calls outside of synctest.Run
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if ident, ok := sel.X.(*ast.Ident); ok {
						if ident.Name == "time" && sel.Sel.Name == "Sleep" && !isInSynctestRun {
							hasTimeSleep = true
							timeSleepPos = call.Pos()
						}
					}
				}
			}
			return true
		})

		// Report if time.Sleep is used without synctest import or not wrapped in synctest.Run
		if hasTimeSleep && !hasSynctestImport {
			pass.Report(analysis.Diagnostic{
				Pos: timeSleepPos,
				Message: "test uses time.Sleep without importing testing/synctest. " +
					"Consider using synctest.Run() for deterministic timing tests. " +
					"See " + f.settings.DocumentationPath + " for migration guide",
				Category: "synctest",
			})
		}
	}

	return nil, nil
}

// hasSynctestImport checks if the file imports the synctest package.
func (f *PluginSynctest) hasSynctestImport(file *ast.File) bool {
	for _, imp := range file.Imports {
		if imp.Path != nil {
			path := strings.Trim(imp.Path.Value, `"`)
			if path == "testing/synctest" {
				return true
			}
		}
	}
	return false
}
