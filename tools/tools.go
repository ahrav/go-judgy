//go:build tools

// Package tools manages development tool dependencies.
// This file ensures that tool dependencies are tracked in go.mod
// but are not included in the main application binary.
package tools

// No direct tool dependencies - tools are installed via Makefile.
