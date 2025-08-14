package domain

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"unicode/utf8"
)

// Property-based tests for ArtifactRef using testing/quick

func TestArtifactRef_JSONRoundTripProperty(t *testing.T) {
	// Property: JSON marshal/unmarshal preserves data integrity
	property := func(ref ArtifactRef) bool {
		// Skip invalid UTF-8 in keys as JSON requires valid UTF-8
		if !utf8.ValidString(ref.Key) {
			return true
		}

		data, err := json.Marshal(ref)
		if err != nil {
			// Marshal should always succeed for valid UTF-8 content
			return false
		}

		var unmarshaled ArtifactRef
		err = json.Unmarshal(data, &unmarshaled)
		if err != nil {
			// Unmarshal should succeed if marshal succeeded
			return false
		}

		// Data should be preserved exactly
		return reflect.DeepEqual(ref, unmarshaled)
	}

	config := &quick.Config{
		MaxCount: 1000,
		Rand:     nil, // Use default random source
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("JSON round-trip property failed: %v", err)
	}
}

func TestArtifactRef_IsZeroProperty(t *testing.T) {
	// Property: IsZero() returns true if and only if all fields are zero values
	property := func(ref ArtifactRef) bool {
		isZero := ref.IsZero()
		expectedZero := (ref.Key == "" && ref.Size == 0 && ref.Kind == "")
		return isZero == expectedZero
	}

	if err := quick.Check(property, nil); err != nil {
		t.Errorf("IsZero property failed: %v", err)
	}
}

func TestArtifactRef_ValidationDeterminismProperty(t *testing.T) {
	// Property: Validation results are deterministic
	property := func(ref ArtifactRef) bool {
		err1 := ref.Validate()
		err2 := ref.Validate()

		// Both should succeed or both should fail
		if (err1 == nil) != (err2 == nil) {
			return false
		}

		// If both fail, error messages should be identical
		if err1 != nil && err2 != nil {
			return err1.Error() == err2.Error()
		}

		return true
	}

	if err := quick.Check(property, nil); err != nil {
		t.Errorf("validation determinism property failed: %v", err)
	}
}

func TestArtifactRef_ValidationConsistencyProperty(t *testing.T) {
	// Property: Validation rules are consistent with constraints
	property := func(key string, size int64, kind string) bool {
		ref := ArtifactRef{
			Key:  key,
			Size: size,
			Kind: ArtifactKind(kind),
		}

		err := ref.Validate()

		// Define what makes a valid ArtifactRef according to validation tags
		validKinds := map[string]bool{
			"answer":          true,
			"judge_rationale": true,
			"raw_prompt":      true,
		}

		isValid := key != "" && size >= 0 && validKinds[kind]

		// Validation result should match our expectation
		return (err == nil) == isValid
	}

	config := &quick.Config{
		MaxCount: 1000,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("validation consistency property failed: %v", err)
	}
}

func TestArtifactRef_CopySemantics(t *testing.T) {
	// Property: ArtifactRef has value semantics (copying preserves equality)
	property := func(ref ArtifactRef) bool {
		// Create a copy
		copy := ref

		// Original and copy should be equal
		if ref != copy {
			return false
		}

		// Modifying copy shouldn't affect original (value semantics)
		copy.Key = "modified"
		copy.Size = 999
		copy.Kind = ArtifactAnswer

		// Original should be unchanged
		return ref != copy
	}

	if err := quick.Check(property, nil); err != nil {
		t.Errorf("copy semantics property failed: %v", err)
	}
}

func TestArtifactRef_JSONFieldPresence(t *testing.T) {
	// Property: JSON fields are present according to their values and omitempty rules
	property := func(ref ArtifactRef) bool {
		// Skip invalid UTF-8
		if !utf8.ValidString(ref.Key) {
			return true
		}

		data, err := json.Marshal(ref)
		if err != nil {
			return false
		}

		jsonStr := string(data)

		// Key field should always be present (no omitempty)
		if !strings.Contains(jsonStr, `"key":`) {
			return false
		}

		// Size field should always be present (no omitempty)
		if !strings.Contains(jsonStr, `"size":`) {
			return false
		}

		// Kind field should always be present (no omitempty)
		if !strings.Contains(jsonStr, `"kind":`) {
			return false
		}

		return true
	}

	config := &quick.Config{
		MaxCount: 500,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("JSON field presence property failed: %v", err)
	}
}

func TestArtifactRef_SizeInvariant(t *testing.T) {
	// Property: Size field maintains its value through all operations
	property := func(key string, size int64, kind string) bool {
		ref := ArtifactRef{
			Key:  key,
			Size: size,
			Kind: ArtifactKind(kind),
		}

		originalSize := ref.Size

		// Operations that shouldn't change Size
		_ = ref.Validate()
		if ref.Size != originalSize {
			return false
		}

		_ = ref.IsZero()
		if ref.Size != originalSize {
			return false
		}

		// JSON round-trip (if valid UTF-8)
		if utf8.ValidString(key) {
			data, err := json.Marshal(ref)
			if err != nil {
				// Can't test round-trip, but size should still be unchanged
				return ref.Size == originalSize
			}

			var unmarshaled ArtifactRef
			err = json.Unmarshal(data, &unmarshaled)
			if err != nil {
				return false
			}

			if unmarshaled.Size != originalSize {
				return false
			}
		}

		return ref.Size == originalSize
	}

	config := &quick.Config{
		MaxCount: 500,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("size invariant property failed: %v", err)
	}
}

func TestArtifactKind_StringConversion(t *testing.T) {
	// Property: ArtifactKind string conversion is consistent
	property := func(s string) bool {
		kind := ArtifactKind(s)
		converted := string(kind)
		return s == converted
	}

	if err := quick.Check(property, nil); err != nil {
		t.Errorf("ArtifactKind string conversion property failed: %v", err)
	}
}

func TestArtifactRef_ZeroValueProperty(t *testing.T) {
	// Property: Zero value has expected characteristics
	t.Run("zero value properties", func(t *testing.T) {
		var ref ArtifactRef

		// Zero value should report as zero
		if !ref.IsZero() {
			t.Errorf("zero value should report IsZero() as true")
		}

		// Zero value should fail validation (due to required fields)
		if err := ref.Validate(); err == nil {
			t.Errorf("zero value should fail validation")
		}

		// Zero value should marshal to JSON successfully
		data, err := json.Marshal(ref)
		if err != nil {
			t.Errorf("zero value should marshal to JSON: %v", err)
		}

		// Should unmarshal back to equivalent zero value
		var ref2 ArtifactRef
		if err := json.Unmarshal(data, &ref2); err != nil {
			t.Errorf("zero value JSON should unmarshal: %v", err)
		}

		if ref != ref2 {
			t.Errorf("zero value round-trip failed: %+v != %+v", ref, ref2)
		}
	})
}

func TestArtifactRef_EquivalenceRelation(t *testing.T) {
	// Property: Equality is an equivalence relation (reflexive, symmetric, transitive)
	property := func(ref1, ref2, ref3 ArtifactRef) bool {
		// Reflexive: ref == ref
		if ref1 != ref1 {
			return false
		}

		// Symmetric: if ref1 == ref2, then ref2 == ref1
		if (ref1 == ref2) != (ref2 == ref1) {
			return false
		}

		// Transitive: if ref1 == ref2 and ref2 == ref3, then ref1 == ref3
		if ref1 == ref2 && ref2 == ref3 && ref1 != ref3 {
			return false
		}

		return true
	}

	config := &quick.Config{
		MaxCount: 500,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("equivalence relation property failed: %v", err)
	}
}

// Custom generator for valid ArtifactRef instances
func generateValidArtifactRef() ArtifactRef {
	validRefs := []ArtifactRef{
		{Key: "answers/test.txt", Size: 1024, Kind: ArtifactAnswer},
		{Key: "rationales/judge.txt", Size: 2048, Kind: ArtifactJudgeRationale},
		{Key: "prompts/rendered.txt", Size: 512, Kind: ArtifactRawPrompt},
		{Key: "files/empty.txt", Size: 0, Kind: ArtifactAnswer},
	}

	// Return a default valid ref
	return validRefs[0]
}

func TestArtifactRef_ValidInstancesPassValidation(t *testing.T) {
	// Property: All generated valid instances should pass validation
	property := func() bool {
		ref := generateValidArtifactRef()
		return ref.Validate() == nil
	}

	config := &quick.Config{
		MaxCount: 100,
	}

	if err := quick.Check(property, config); err != nil {
		t.Errorf("valid instances validation property failed: %v", err)
	}
}

// Benchmark property tests to ensure they're performant
func BenchmarkArtifactRef_PropertyTests(b *testing.B) {
	ref := ArtifactRef{
		Key:  "benchmark/property.txt",
		Size: 1024,
		Kind: ArtifactAnswer,
	}

	b.Run("IsZero", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			isZero := ref.IsZero()
			expectedZero := (ref.Key == "" && ref.Size == 0 && ref.Kind == "")
			if isZero != expectedZero {
				b.Errorf("property violation")
			}
		}
	})

	b.Run("Validation", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			err1 := ref.Validate()
			err2 := ref.Validate()
			if (err1 == nil) != (err2 == nil) {
				b.Errorf("validation not deterministic")
			}
		}
	})

	b.Run("JSONRoundTrip", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			data, err := json.Marshal(ref)
			if err != nil {
				b.Errorf("marshal failed: %v", err)
			}

			var ref2 ArtifactRef
			err = json.Unmarshal(data, &ref2)
			if err != nil {
				b.Errorf("unmarshal failed: %v", err)
			}

			if ref != ref2 {
				b.Errorf("round-trip failed")
			}
		}
	})
}

// Example of how to use property-based testing for debugging
func Example_artifactPropertyTesting() {
	// This example shows how property-based testing can catch edge cases
	property := func(ref ArtifactRef) bool {
		// Property: IsZero should be consistent
		isZero1 := ref.IsZero()
		isZero2 := ref.IsZero()
		return isZero1 == isZero2
	}

	// This would find any edge case where IsZero() is not deterministic
	if err := quick.Check(property, nil); err != nil {
		fmt.Printf("Found problematic input: %v\n", err)
	}
	// Output:
}
