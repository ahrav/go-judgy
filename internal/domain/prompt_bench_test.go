package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

// Helper to create benchmark artifacts
func createBenchArtifactRef(size int64) ArtifactRef {
	return ArtifactRef{
		Key:  "bench/prompt.txt",
		Size: size,
		Kind: ArtifactRawPrompt,
	}
}

// BenchmarkNewPromptSpec_Small tests performance with small inputs
func BenchmarkNewPromptSpec_Small(b *testing.B) {
	template := "Hello {{.name}}"
	variables := map[string]string{"name": "world"}
	renderedContent := "Hello world"
	artifactRef := createBenchArtifactRef(int64(len(renderedContent)))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := NewPromptSpec(template, variables, renderedContent, artifactRef)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNewPromptSpec_Medium tests performance with medium-sized inputs
func BenchmarkNewPromptSpec_Medium(b *testing.B) {
	template := "Template with {{.data}} and more content: " + strings.Repeat("text ", 100)
	variables := map[string]string{
		"data":   strings.Repeat("value", 50),
		"extra1": strings.Repeat("a", 100),
		"extra2": strings.Repeat("b", 100),
		"extra3": strings.Repeat("c", 100),
	}
	renderedContent := "Template with " + strings.Repeat("value", 50) + " and more content: " + strings.Repeat("text ", 100)
	artifactRef := createBenchArtifactRef(int64(len(renderedContent)))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := NewPromptSpec(template, variables, renderedContent, artifactRef)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNewPromptSpec_Large tests performance with large inputs
func BenchmarkNewPromptSpec_Large(b *testing.B) {
	template := "Large template: " + strings.Repeat("{{.key}} ", 1000)

	// Create large variables map
	variables := make(map[string]string)
	for i := 0; i < 100; i++ {
		variables[fmt.Sprintf("key%d", i)] = strings.Repeat("value", 100)
	}

	renderedContent := "Large template: " + strings.Repeat("large_value ", 1000)
	artifactRef := createBenchArtifactRef(int64(len(renderedContent)))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := NewPromptSpec(template, variables, renderedContent, artifactRef)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNewPromptSpec_VeryLarge tests performance with very large inputs
func BenchmarkNewPromptSpec_VeryLarge(b *testing.B) {
	template := strings.Repeat("Template segment {{.data}} ", 5000)

	// Create very large variables map
	variables := make(map[string]string)
	for i := 0; i < 500; i++ {
		variables[fmt.Sprintf("key%d", i)] = strings.Repeat("val", 200)
	}

	renderedContent := strings.Repeat("Very large rendered content block ", 10000)
	artifactRef := createBenchArtifactRef(int64(len(renderedContent)))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := NewPromptSpec(template, variables, renderedContent, artifactRef)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNewPromptSpec_Parallel tests concurrent performance
func BenchmarkNewPromptSpec_Parallel(b *testing.B) {
	template := "Hello {{.name}} from {{.location}}"
	variables := map[string]string{
		"name":     "world",
		"location": "benchmark",
	}
	renderedContent := "Hello world from benchmark"
	artifactRef := createBenchArtifactRef(int64(len(renderedContent)))

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := NewPromptSpec(template, variables, renderedContent, artifactRef)
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkPromptSpec_Validate tests validation performance
func BenchmarkPromptSpec_Validate(b *testing.B) {
	spec := PromptSpec{
		Template: "Hello {{.name}}",
		Variables: map[string]string{
			"name":  "world",
			"extra": strings.Repeat("value", 100),
		},
		Hash: "abcd1234567890abcd1234567890abcd1234567890abcd1234567890abcd1234",
		RenderedRef: ArtifactRef{
			Key:  "prompts/bench.txt",
			Size: 100,
			Kind: ArtifactRawPrompt,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := spec.Validate()
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkPromptSpec_Validate_Parallel tests concurrent validation performance
func BenchmarkPromptSpec_Validate_Parallel(b *testing.B) {
	spec := PromptSpec{
		Template: "Hello {{.name}}",
		Variables: map[string]string{
			"name":  "world",
			"extra": strings.Repeat("value", 100),
		},
		Hash: "abcd1234567890abcd1234567890abcd1234567890abcd1234567890abcd1234",
		RenderedRef: ArtifactRef{
			Key:  "prompts/bench.txt",
			Size: 100,
			Kind: ArtifactRawPrompt,
		},
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			err := spec.Validate()
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkMapCloning tests performance of variable map cloning
func BenchmarkMapCloning_Small(b *testing.B) {
	originalMap := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := cloneStringMap(originalMap)
		if len(cloned) != len(originalMap) {
			b.Fatal("clone failed")
		}
	}
}

// BenchmarkMapCloning_Medium tests map cloning with medium-sized maps
func BenchmarkMapCloning_Medium(b *testing.B) {
	originalMap := make(map[string]string)
	for i := 0; i < 50; i++ {
		originalMap[fmt.Sprintf("key%d", i)] = strings.Repeat("value", 20)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := cloneStringMap(originalMap)
		if len(cloned) != len(originalMap) {
			b.Fatal("clone failed")
		}
	}
}

// BenchmarkMapCloning_Large tests map cloning with large maps
func BenchmarkMapCloning_Large(b *testing.B) {
	originalMap := make(map[string]string)
	for i := 0; i < 1000; i++ {
		originalMap[fmt.Sprintf("key%d", i)] = strings.Repeat("value", 50)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := cloneStringMap(originalMap)
		if len(cloned) != len(originalMap) {
			b.Fatal("clone failed")
		}
	}
}

// BenchmarkMapCloning_Nil tests nil map cloning performance
func BenchmarkMapCloning_Nil(b *testing.B) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cloned := cloneStringMap(nil)
		if cloned != nil {
			b.Fatal("nil clone should return nil")
		}
	}
}

// BenchmarkHashComputation tests SHA-256 hashing performance by content size
func BenchmarkHashComputation_Small(b *testing.B) {
	content := "Hello world"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hasher := sha256.New()
		hasher.Write([]byte(content))
		hash := hex.EncodeToString(hasher.Sum(nil))
		if len(hash) != 64 {
			b.Fatal("invalid hash length")
		}
	}
}

// BenchmarkHashComputation_Medium tests hashing medium content
func BenchmarkHashComputation_Medium(b *testing.B) {
	content := strings.Repeat("content block ", 1000) // ~13KB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hasher := sha256.New()
		hasher.Write([]byte(content))
		hash := hex.EncodeToString(hasher.Sum(nil))
		if len(hash) != 64 {
			b.Fatal("invalid hash length")
		}
	}
}

// BenchmarkHashComputation_Large tests hashing large content
func BenchmarkHashComputation_Large(b *testing.B) {
	content := strings.Repeat("large content block with more text ", 10000) // ~350KB

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		hasher := sha256.New()
		hasher.Write([]byte(content))
		hash := hex.EncodeToString(hasher.Sum(nil))
		if len(hash) != 64 {
			b.Fatal("invalid hash length")
		}
	}
}

// BenchmarkNewPromptSpec_NoAllocation tests if we can avoid allocations in fast path
func BenchmarkNewPromptSpec_NoAllocation(b *testing.B) {
	template := "Hello {{.name}}"
	variables := map[string]string{"name": "world"}
	renderedContent := "Hello world"
	artifactRef := createBenchArtifactRef(int64(len(renderedContent)))

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, err := NewPromptSpec(template, variables, renderedContent, artifactRef)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkNewPromptSpec_VariousMapSizes tests performance across different map sizes
func BenchmarkNewPromptSpec_VariousMapSizes(b *testing.B) {
	sizes := []int{0, 1, 5, 10, 50, 100, 500}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("MapSize_%d", size), func(b *testing.B) {
			template := "Template"
			variables := make(map[string]string)
			for i := 0; i < size; i++ {
				variables[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
			}
			renderedContent := "Rendered content"
			artifactRef := createBenchArtifactRef(int64(len(renderedContent)))

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := NewPromptSpec(template, variables, renderedContent, artifactRef)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
