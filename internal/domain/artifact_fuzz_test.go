package domain

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzArtifactRef_JSONRoundTrip(f *testing.F) {
	// Seed corpus with edge cases and valid examples
	f.Add(`{"key":"","size":0,"kind":""}`)
	f.Add(`{"key":"test.txt","size":0,"kind":"answer"}`)
	f.Add(`{"key":"test.txt","size":1024,"kind":"answer"}`)
	f.Add(`{"key":"rationale/judge.txt","size":2048,"kind":"judge_rationale"}`)
	f.Add(`{"key":"prompts/rendered.txt","size":512,"kind":"raw_prompt"}`)
	f.Add(`{"key":"files/文档.txt","size":256,"kind":"answer"}`)
	f.Add(`{"key":"path/with spaces/file.txt","size":100,"kind":"judge_rationale"}`)
	f.Add(`{"key":"test","size":9223372036854775807,"kind":"raw_prompt"}`)
	f.Add(`{"key":"test","size":-1,"kind":"answer"}`)
	f.Add(`null`)
	f.Add(`{}`)
	f.Add(`{"key":null,"size":null,"kind":null}`)
	f.Add(`{"key":"test","size":"100","kind":"answer"}`) // string size
	f.Add(`{"unknown_field":"value","key":"test","size":100,"kind":"answer"}`)

	f.Fuzz(func(t *testing.T, input string) {
		var ref ArtifactRef
		err := json.Unmarshal([]byte(input), &ref)
		if err != nil {
			// Invalid JSON should fail unmarshaling
			return
		}

		// If unmarshaling succeeded, round-trip should preserve data
		marshaled, err := json.Marshal(ref)
		if err != nil {
			t.Errorf("failed to marshal valid ArtifactRef: %v", err)
			return
		}

		var ref2 ArtifactRef
		err = json.Unmarshal(marshaled, &ref2)
		if err != nil {
			t.Errorf("round-trip unmarshal failed: %v", err)
			return
		}

		if ref != ref2 {
			t.Errorf("round-trip mismatch: %+v != %+v", ref, ref2)
		}

		// Verify that IsZero() behavior is consistent
		isZero1 := ref.IsZero()
		isZero2 := ref2.IsZero()
		if isZero1 != isZero2 {
			t.Errorf("IsZero() inconsistent after round-trip: %v != %v", isZero1, isZero2)
		}
	})
}

func FuzzArtifactRef_Validation(f *testing.F) {
	// Seed with boundary values and edge cases
	f.Add("", int64(0), "")
	f.Add("test.txt", int64(0), "answer")
	f.Add("test.txt", int64(1024), "answer")
	f.Add("test.txt", int64(-1), "answer")
	f.Add("test.txt", int64(9223372036854775807), "answer") // MaxInt64
	f.Add("", int64(100), "judge_rationale")
	f.Add("key", int64(100), "")
	f.Add("key", int64(100), "invalid_kind")
	f.Add("very/long/path/to/some/file/that/might/be/problematic.txt", int64(1000), "raw_prompt")
	f.Add("files/文档.txt", int64(256), "answer") // Unicode
	f.Add("path with spaces", int64(100), "judge_rationale")
	f.Add("\x00\x01\x02", int64(50), "answer") // Control characters
	f.Add("path/with\nnewlines", int64(75), "raw_prompt")

	validKinds := []string{"answer", "judge_rationale", "raw_prompt"}

	f.Fuzz(func(t *testing.T, key string, size int64, kind string) {
		ref := ArtifactRef{
			Key:  key,
			Size: size,
			Kind: ArtifactKind(kind),
		}

		err := ref.Validate()

		// Validation should be deterministic
		err2 := ref.Validate()
		if (err == nil) != (err2 == nil) {
			t.Errorf("validation not deterministic: first=%v, second=%v", err, err2)
		}

		// Check validation rules
		if err == nil {
			// If validation passes, all constraints should be met
			if key == "" {
				t.Errorf("validation passed with empty key")
			}
			if size < 0 {
				t.Errorf("validation passed with negative size: %d", size)
			}
			if kind == "" {
				t.Errorf("validation passed with empty kind")
			}

			// Kind should be one of the valid values
			isValidKind := false
			for _, validKind := range validKinds {
				if kind == validKind {
					isValidKind = true
					break
				}
			}
			if !isValidKind {
				t.Errorf("validation passed with invalid kind: %s", kind)
			}
		} else {
			// If validation fails, at least one constraint should be violated
			hasError := key == "" || size < 0 || kind == ""

			// Check if kind is invalid
			if kind != "" {
				isValidKind := false
				for _, validKind := range validKinds {
					if kind == validKind {
						isValidKind = true
						break
					}
				}
				if !isValidKind {
					hasError = true
				}
			}

			if !hasError {
				t.Errorf("validation failed but all constraints appear to be met: key=%q, size=%d, kind=%q", key, size, kind)
			}
		}

		// Test IsZero behavior
		isZero := ref.IsZero()
		expectedZero := (key == "" && size == 0 && kind == "")
		if isZero != expectedZero {
			t.Errorf("IsZero() returned %v, expected %v for key=%q size=%d kind=%q",
				isZero, expectedZero, key, size, kind)
		}
	})
}

func FuzzArtifactRef_KeyValidation(f *testing.F) {
	// Seed with various key patterns
	f.Add("")
	f.Add("simple")
	f.Add("path/to/file.txt")
	f.Add("very/deeply/nested/path/to/some/file/that/might/be/quite/long.txt")
	f.Add("files/文档.txt")
	f.Add("path with spaces")
	f.Add("file-name_123.txt")
	f.Add("UPPERCASE.TXT")
	f.Add("dots...in...name")
	f.Add("file\nwith\nnewlines")
	f.Add("file\twith\ttabs")
	f.Add("\x00binary\x01data\x02")
	f.Add("../../malicious/path")
	f.Add("/absolute/path")
	f.Add("./relative/path")
	f.Add(string(make([]byte, 1000))) // Very long key

	f.Fuzz(func(t *testing.T, key string) {
		// Test with a valid configuration except for the key
		ref := ArtifactRef{
			Key:  key,
			Size: 1024,
			Kind: ArtifactAnswer,
		}

		// Key validation is handled by the 'required' tag, not content validation
		err := ref.Validate()
		if key == "" && err == nil {
			t.Errorf("validation should fail for empty key")
		}
		if key != "" && err != nil {
			// Check if error is related to key (should only be 'required' validation)
			if strings.Contains(err.Error(), "Key") && !strings.Contains(err.Error(), "required") {
				t.Errorf("unexpected key validation error: %v", err)
			}
		}

		// Test JSON round-trip with various key values
		if utf8.ValidString(key) { // Only test JSON with valid UTF-8
			data, err := json.Marshal(ref)
			if err != nil {
				// JSON marshal should generally work unless the key has invalid UTF-8
				t.Errorf("failed to marshal ref with key %q: %v", key, err)
				return
			}

			var ref2 ArtifactRef
			err = json.Unmarshal(data, &ref2)
			if err != nil {
				t.Errorf("failed to unmarshal ref with key %q: %v", key, err)
				return
			}

			if ref.Key != ref2.Key {
				t.Errorf("key mismatch after JSON round-trip: %q != %q", ref.Key, ref2.Key)
			}
		}
	})
}

func FuzzArtifactRef_SizeValidation(f *testing.F) {
	// Seed with boundary and edge values
	f.Add(int64(-9223372036854775808)) // MinInt64
	f.Add(int64(-1))
	f.Add(int64(0))
	f.Add(int64(1))
	f.Add(int64(1024))
	f.Add(int64(1048576))             // 1MB
	f.Add(int64(1073741824))          // 1GB
	f.Add(int64(9223372036854775807)) // MaxInt64

	f.Fuzz(func(t *testing.T, size int64) {
		ref := ArtifactRef{
			Key:  "test/file.txt",
			Size: size,
			Kind: ArtifactAnswer,
		}

		err := ref.Validate()

		// Size validation: must be >= 0
		if size < 0 && err == nil {
			t.Errorf("validation should fail for negative size: %d", size)
		}
		if size >= 0 && err != nil && strings.Contains(err.Error(), "min") {
			t.Errorf("validation should pass for non-negative size %d, got error: %v", size, err)
		}

		// Test JSON round-trip with various size values
		data, err := json.Marshal(ref)
		if err != nil {
			t.Errorf("failed to marshal ref with size %d: %v", size, err)
			return
		}

		var ref2 ArtifactRef
		err = json.Unmarshal(data, &ref2)
		if err != nil {
			t.Errorf("failed to unmarshal ref with size %d: %v", size, err)
			return
		}

		if ref.Size != ref2.Size {
			t.Errorf("size mismatch after JSON round-trip: %d != %d", ref.Size, ref2.Size)
		}
	})
}

func FuzzArtifactRef_KindValidation(f *testing.F) {
	// Seed with valid and invalid kind values
	f.Add("")
	f.Add("answer")
	f.Add("judge_rationale")
	f.Add("raw_prompt")
	f.Add("invalid")
	f.Add("ANSWER")          // wrong case
	f.Add("answer ")         // trailing space
	f.Add(" answer")         // leading space
	f.Add("judge-rationale") // wrong separator
	f.Add("raw-prompt")      // wrong separator
	f.Add("judge_rationale_extra")
	f.Add("answering")
	f.Add("prompt")
	f.Add("rationale")
	f.Add("123")
	f.Add("answer123")
	f.Add("文档")   // Unicode
	f.Add("\x00") // Control character

	validKinds := map[string]bool{
		"answer":          true,
		"judge_rationale": true,
		"raw_prompt":      true,
	}

	f.Fuzz(func(t *testing.T, kind string) {
		ref := ArtifactRef{
			Key:  "test/file.txt",
			Size: 1024,
			Kind: ArtifactKind(kind),
		}

		err := ref.Validate()
		isValid := validKinds[kind]

		if kind == "" {
			// Empty kind should fail with 'required' error
			if err == nil {
				t.Errorf("validation should fail for empty kind")
			} else if !strings.Contains(err.Error(), "required") {
				t.Errorf("expected 'required' error for empty kind, got: %v", err)
			}
		} else if isValid {
			// Valid kinds should pass (assuming other fields are valid)
			if err != nil && strings.Contains(err.Error(), "oneof") {
				t.Errorf("validation should pass for valid kind %q, got: %v", kind, err)
			}
		} else {
			// Invalid kinds should fail with 'oneof' error
			if err == nil {
				t.Errorf("validation should fail for invalid kind: %q", kind)
			} else if !strings.Contains(err.Error(), "oneof") {
				t.Errorf("expected 'oneof' error for invalid kind %q, got: %v", kind, err)
			}
		}

		// Test JSON round-trip
		data, err := json.Marshal(ref)
		if err != nil {
			t.Errorf("failed to marshal ref with kind %q: %v", kind, err)
			return
		}

		var ref2 ArtifactRef
		err = json.Unmarshal(data, &ref2)
		if err != nil {
			t.Errorf("failed to unmarshal ref with kind %q: %v", kind, err)
			return
		}

		if ref.Kind != ref2.Kind {
			t.Errorf("kind mismatch after JSON round-trip: %q != %q", ref.Kind, ref2.Kind)
		}
	})
}

func FuzzArtifactRef_ComplexJSON(f *testing.F) {
	// Seed with various JSON structures that might cause issues
	f.Add(`{"key":"test","size":100,"kind":"answer","extra":"field"}`)
	f.Add(`{"kind":"answer","size":100,"key":"test"}`)   // Different field order
	f.Add(`{"key":"test","size":"100","kind":"answer"}`) // String size
	f.Add(`{"key":"test","size":100.5,"kind":"answer"}`) // Float size
	f.Add(`{"key":"test","size":100,"kind":"answer","nested":{"field":"value"}}`)
	f.Add(`[{"key":"test","size":100,"kind":"answer"}]`) // Array instead of object
	f.Add(`"not an object"`)
	f.Add(`123`)
	f.Add(`true`)
	f.Add(`false`)
	f.Add(`null`)

	f.Fuzz(func(t *testing.T, input string) {
		// Skip invalid UTF-8
		if !utf8.ValidString(input) {
			return
		}

		var ref ArtifactRef
		err := json.Unmarshal([]byte(input), &ref)
		if err != nil {
			// Invalid JSON is expected to fail
			return
		}

		// If we successfully unmarshaled, verify properties

		// IsZero should work correctly
		isZero := ref.IsZero()
		expectedZero := (ref.Key == "" && ref.Size == 0 && ref.Kind == "")
		if isZero != expectedZero {
			t.Errorf("IsZero() inconsistent: got %v, expected %v for %+v", isZero, expectedZero, ref)
		}

		// Should be able to marshal back
		data, err := json.Marshal(ref)
		if err != nil {
			t.Errorf("failed to marshal after successful unmarshal: %v", err)
			return
		}

		// Should unmarshal consistently
		var ref2 ArtifactRef
		err = json.Unmarshal(data, &ref2)
		if err != nil {
			t.Errorf("failed to unmarshal after marshal: %v", err)
			return
		}

		if ref != ref2 {
			t.Errorf("inconsistent after marshal/unmarshal cycle: %+v != %+v", ref, ref2)
		}

		// Validation should be deterministic
		err1 := ref.Validate()
		err2 := ref.Validate()
		if (err1 == nil) != (err2 == nil) {
			t.Errorf("validation not deterministic: %v vs %v", err1, err2)
		}
	})
}

// Benchmark to ensure fuzz tests don't have performance regressions
func BenchmarkArtifactRef_FuzzOperations(b *testing.B) {
	ref := ArtifactRef{
		Key:  "benchmark/test.txt",
		Size: 1024,
		Kind: ArtifactAnswer,
	}

	data, _ := json.Marshal(ref)

	b.Run("Validate", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = ref.Validate()
		}
	})

	b.Run("IsZero", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = ref.IsZero()
		}
	})

	b.Run("Marshal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, _ = json.Marshal(ref)
		}
	})

	b.Run("Unmarshal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var r ArtifactRef
			_ = json.Unmarshal(data, &r)
		}
	})
}
