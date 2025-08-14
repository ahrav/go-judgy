package domain //nolint:testpackage // Need access to unexported validate

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArtifactKind_Constants(t *testing.T) {
	tests := []struct {
		name     string
		kind     ArtifactKind
		expected string
	}{
		{
			name:     "answer artifact kind",
			kind:     ArtifactAnswer,
			expected: "answer",
		},
		{
			name:     "judge rationale artifact kind",
			kind:     ArtifactJudgeRationale,
			expected: "judge_rationale",
		},
		{
			name:     "raw prompt artifact kind",
			kind:     ArtifactRawPrompt,
			expected: "raw_prompt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.kind))
		})
	}
}

func TestArtifactRef_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     ArtifactRef
		wantErr bool
		errType string
	}{
		{
			name: "valid answer artifact",
			ref: ArtifactRef{
				Key:  "answers/2025/08/test.txt",
				Size: 1024,
				Kind: ArtifactAnswer,
			},
			wantErr: false,
		},
		{
			name: "valid judge rationale artifact",
			ref: ArtifactRef{
				Key:  "rationales/judge-123.txt",
				Size: 2048,
				Kind: ArtifactJudgeRationale,
			},
			wantErr: false,
		},
		{
			name: "valid raw prompt artifact",
			ref: ArtifactRef{
				Key:  "prompts/rendered/prompt.txt",
				Size: 512,
				Kind: ArtifactRawPrompt,
			},
			wantErr: false,
		},
		{
			name: "valid with zero size",
			ref: ArtifactRef{
				Key:  "empty/file.txt",
				Size: 0,
				Kind: ArtifactAnswer,
			},
			wantErr: false,
		},
		{
			name: "valid with maximum size",
			ref: ArtifactRef{
				Key:  "large/file.txt",
				Size: 9223372036854775807, // MaxInt64
				Kind: ArtifactAnswer,
			},
			wantErr: false,
		},
		{
			name: "missing key",
			ref: ArtifactRef{
				Key:  "",
				Size: 1024,
				Kind: ArtifactAnswer,
			},
			wantErr: true,
			errType: "required",
		},
		{
			name: "negative size",
			ref: ArtifactRef{
				Key:  "valid/key.txt",
				Size: -1,
				Kind: ArtifactAnswer,
			},
			wantErr: true,
			errType: "min",
		},
		{
			name: "missing kind",
			ref: ArtifactRef{
				Key:  "valid/key.txt",
				Size: 1024,
				Kind: "",
			},
			wantErr: true,
			errType: "required",
		},
		{
			name: "invalid kind",
			ref: ArtifactRef{
				Key:  "valid/key.txt",
				Size: 1024,
				Kind: "invalid_kind",
			},
			wantErr: true,
			errType: "oneof",
		},
		{
			name: "multiple validation errors - empty key and negative size",
			ref: ArtifactRef{
				Key:  "",
				Size: -100,
				Kind: ArtifactAnswer,
			},
			wantErr: true,
		},
		{
			name:    "zero struct fails validation",
			ref:     ArtifactRef{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()

			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != "" {
					assert.Contains(t, err.Error(), tt.errType)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestArtifactRef_IsZero(t *testing.T) {
	tests := []struct {
		name     string
		ref      ArtifactRef
		expected bool
	}{
		{
			name:     "completely zero struct",
			ref:      ArtifactRef{},
			expected: true,
		},
		{
			name: "zero values explicitly set",
			ref: ArtifactRef{
				Key:  "",
				Size: 0,
				Kind: "",
			},
			expected: true,
		},
		{
			name: "only key set",
			ref: ArtifactRef{
				Key:  "test.txt",
				Size: 0,
				Kind: "",
			},
			expected: false,
		},
		{
			name: "only size set",
			ref: ArtifactRef{
				Key:  "",
				Size: 100,
				Kind: "",
			},
			expected: false,
		},
		{
			name: "only kind set",
			ref: ArtifactRef{
				Key:  "",
				Size: 0,
				Kind: ArtifactAnswer,
			},
			expected: false,
		},
		{
			name: "key and size set",
			ref: ArtifactRef{
				Key:  "test.txt",
				Size: 100,
				Kind: "",
			},
			expected: false,
		},
		{
			name: "key and kind set",
			ref: ArtifactRef{
				Key:  "test.txt",
				Size: 0,
				Kind: ArtifactAnswer,
			},
			expected: false,
		},
		{
			name: "size and kind set",
			ref: ArtifactRef{
				Key:  "",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			expected: false,
		},
		{
			name: "all fields set",
			ref: ArtifactRef{
				Key:  "test.txt",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			expected: false,
		},
		{
			name: "negative size still not zero",
			ref: ArtifactRef{
				Key:  "",
				Size: -1,
				Kind: "",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.ref.IsZero()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestArtifactRef_JSONSerialization(t *testing.T) {
	tests := []struct {
		name     string
		ref      ArtifactRef
		expected string
	}{
		{
			name: "complete artifact reference",
			ref: ArtifactRef{
				Key:  "answers/2025/08/test.txt",
				Size: 1024,
				Kind: ArtifactAnswer,
			},
			expected: `{"key":"answers/2025/08/test.txt","size":1024,"kind":"answer"}`,
		},
		{
			name: "zero size",
			ref: ArtifactRef{
				Key:  "empty.txt",
				Size: 0,
				Kind: ArtifactJudgeRationale,
			},
			expected: `{"key":"empty.txt","size":0,"kind":"judge_rationale"}`,
		},
		{
			name: "unicode key",
			ref: ArtifactRef{
				Key:  "files/文档.txt",
				Size: 512,
				Kind: ArtifactRawPrompt,
			},
			expected: `{"key":"files/文档.txt","size":512,"kind":"raw_prompt"}`,
		},
		{
			name: "special characters in key",
			ref: ArtifactRef{
				Key:  "path/with spaces/file-name_123.txt",
				Size: 256,
				Kind: ArtifactAnswer,
			},
			expected: `{"key":"path/with spaces/file-name_123.txt","size":256,"kind":"answer"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test marshaling
			data, err := json.Marshal(tt.ref)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))

			// Test unmarshaling
			var unmarshaled ArtifactRef
			err = json.Unmarshal(data, &unmarshaled)
			require.NoError(t, err)
			assert.Equal(t, tt.ref, unmarshaled)
		})
	}
}

func TestArtifactRef_JSONDeserialization(t *testing.T) {
	tests := []struct {
		name     string
		json     string
		expected ArtifactRef
		wantErr  bool
	}{
		{
			name: "valid complete JSON",
			json: `{"key":"test.txt","size":100,"kind":"answer"}`,
			expected: ArtifactRef{
				Key:  "test.txt",
				Size: 100,
				Kind: ArtifactAnswer,
			},
			wantErr: false,
		},
		{
			name:    "invalid string size",
			json:    `{"key":"test.txt","size":"100","kind":"answer"}`,
			wantErr: true,
		},
		{
			name:     "missing required fields",
			json:     `{"size":100}`,
			expected: ArtifactRef{Size: 100},
			wantErr:  false, // JSON unmarshaling succeeds, validation would fail
		},
		{
			name:     "null values",
			json:     `{"key":null,"size":null,"kind":null}`,
			expected: ArtifactRef{},
			wantErr:  false,
		},
		{
			name:     "empty JSON object",
			json:     `{}`,
			expected: ArtifactRef{},
			wantErr:  false,
		},
		{
			name:    "invalid JSON",
			json:    `{"key":"test.txt","size":100,"kind":"answer"`,
			wantErr: true,
		},
		{
			name:    "invalid size type",
			json:    `{"key":"test.txt","size":"invalid","kind":"answer"}`,
			wantErr: true,
		},
		{
			name: "large size value",
			json: `{"key":"test.txt","size":9223372036854775807,"kind":"answer"}`,
			expected: ArtifactRef{
				Key:  "test.txt",
				Size: 9223372036854775807,
				Kind: ArtifactAnswer,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ref ArtifactRef
			err := json.Unmarshal([]byte(tt.json), &ref)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, ref)
			}
		})
	}
}

func TestArtifactRef_EdgeCases(t *testing.T) {
	t.Run("extremely long key", func(t *testing.T) {
		longKey := string(make([]byte, 10000)) // 10KB key
		for i := range longKey {
			longKey = string(rune(65 + (i % 26))) // Fill with A-Z
		}

		ref := ArtifactRef{
			Key:  longKey,
			Size: 1000,
			Kind: ArtifactAnswer,
		}

		// Should marshal/unmarshal successfully
		data, err := json.Marshal(ref)
		require.NoError(t, err)

		var unmarshaled ArtifactRef
		err = json.Unmarshal(data, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, ref, unmarshaled)

		// Should validate successfully
		assert.NoError(t, ref.Validate())
	})

	t.Run("empty string kind vs unset kind", func(t *testing.T) {
		emptyKind := ArtifactRef{
			Key:  "test.txt",
			Size: 100,
			Kind: "",
		}

		assert.Error(t, emptyKind.Validate())
		assert.False(t, emptyKind.IsZero()) // Not zero because Key and Size are set
	})

	t.Run("boundary size values", func(t *testing.T) {
		cases := []struct {
			name    string
			size    int64
			wantErr bool
		}{
			{"zero size", 0, false},
			{"one byte", 1, false},
			{"max int64", 9223372036854775807, false},
			{"negative one", -1, true},
			{"large negative", -9223372036854775808, true},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				ref := ArtifactRef{
					Key:  "test.txt",
					Size: tc.size,
					Kind: ArtifactAnswer,
				}

				err := ref.Validate()
				if tc.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})
}

func TestArtifactRef_ValidationTags(t *testing.T) {
	t.Run("required tag enforcement", func(t *testing.T) {
		// Test that 'required' validation tags work as expected
		tests := []struct {
			name string
			ref  ArtifactRef
		}{
			{
				name: "missing key",
				ref: ArtifactRef{
					Size: 100,
					Kind: ArtifactAnswer,
				},
			},
			{
				name: "missing kind",
				ref: ArtifactRef{
					Key:  "test.txt",
					Size: 100,
				},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				err := tt.ref.Validate()
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "required")
			})
		}
	})

	t.Run("min tag enforcement", func(t *testing.T) {
		ref := ArtifactRef{
			Key:  "test.txt",
			Size: -10,
			Kind: ArtifactAnswer,
		}

		err := ref.Validate()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "min")
	})

	t.Run("oneof tag enforcement", func(t *testing.T) {
		invalidKinds := []string{
			"invalid",
			"answer_wrong",
			"ANSWER",          // case sensitive
			"judge-rationale", // wrong format
			"raw-prompt",      // wrong format
			"   answer   ",    // with spaces
		}

		for _, kind := range invalidKinds {
			t.Run("invalid kind: "+kind, func(t *testing.T) {
				ref := ArtifactRef{
					Key:  "test.txt",
					Size: 100,
					Kind: ArtifactKind(kind),
				}

				err := ref.Validate()
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "oneof")
			})
		}
	})
}

// Helper function to create a valid ArtifactRef for testing
func createValidArtifactRef() ArtifactRef {
	return ArtifactRef{
		Key:  "test/artifact.txt",
		Size: 1024,
		Kind: ArtifactAnswer,
	}
}

// Example test demonstrating typical usage
func ExampleArtifactRef_Validate() {
	ref := ArtifactRef{
		Key:  "answers/2025/08/response.txt",
		Size: 1024,
		Kind: ArtifactAnswer,
	}

	err := ref.Validate()
	if err != nil {
		// Handle validation error
		_ = err
	}
	// Output:
}

func ExampleArtifactRef_IsZero() {
	var ref ArtifactRef
	if ref.IsZero() {
		// Handle empty reference
	}
	// Output:
}
