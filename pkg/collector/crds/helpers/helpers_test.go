//nolint:testpackage // Tests need access to private functions
package helpers

import (
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestExtractFieldString(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		path     string
		expected string
	}{
		{
			name: "simple field",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"phase": "Running",
					},
				},
			},
			path:     "status.phase",
			expected: "Running",
		},
		{
			name: "nested field",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"metadata": map[string]any{
						"labels": map[string]any{
							"app": "myapp",
						},
					},
				},
			},
			path:     "metadata.labels.app",
			expected: "myapp",
		},
		{
			name:     "empty path",
			obj:      &unstructured.Unstructured{Object: map[string]any{}},
			path:     "",
			expected: "",
		},
		{
			name:     "non-existent field",
			obj:      &unstructured.Unstructured{Object: map[string]any{}},
			path:     "status.phase",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFieldString(tt.obj, tt.path)
			if got != tt.expected {
				t.Errorf("extractFieldString() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestExtractFieldFloat(t *testing.T) {
	tests := []struct {
		name     string
		obj      *unstructured.Unstructured
		path     string
		expected float64
	}{
		{
			name: "int64 value",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"spec": map[string]any{
						"replicas": int64(3),
					},
				},
			},
			path:     "spec.replicas",
			expected: 3.0,
		},
		{
			name: "float64 value",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"progress": 0.75,
					},
				},
			},
			path:     "status.progress",
			expected: 0.75,
		},
		{
			name:     "non-existent field",
			obj:      &unstructured.Unstructured{Object: map[string]any{}},
			path:     "spec.replicas",
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractFieldFloat(tt.obj, tt.path)
			if got != tt.expected {
				t.Errorf("extractFieldFloat() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name     string
		value    any
		expected float64
	}{
		{name: "float64", value: float64(42.5), expected: 42.5},
		{name: "float32", value: float32(42.5), expected: 42.5},
		{name: "int", value: int(42), expected: 42.0},
		{name: "int32", value: int32(42), expected: 42.0},
		{name: "int64", value: int64(42), expected: 42.0},
		{name: "uint", value: uint(42), expected: 42.0},
		{name: "uint32", value: uint32(42), expected: 42.0},
		{name: "uint64", value: uint64(42), expected: 42.0},
		{name: "bool true", value: true, expected: 1.0},
		{name: "bool false", value: false, expected: 0.0},
		{name: "string number", value: "3.14", expected: 3.14},
		{name: "string non-number", value: "abc", expected: 0.0},
		{name: "unsupported type", value: struct{}{}, expected: 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToFloat64(tt.value)
			if got != tt.expected {
				t.Errorf("toFloat64(%v) = %v, want %v", tt.value, got, tt.expected)
			}
		})
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "lowercase", input: "myapp", expected: "myapp"},
		{name: "uppercase", input: "MyApp", expected: "myapp"},
		{name: "with hyphens", input: "my-app", expected: "my_app"},
		{name: "with dots", input: "my.app.v1", expected: "my_app_v1"},
		{name: "with spaces", input: "my app", expected: "my_app"},
		{name: "with special chars", input: "my@app#v1!", expected: "my_app_v1_"},
		{name: "mixed case and special", input: "MyApp-V1.2", expected: "myapp_v1_2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetSortedKeys(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]string
		expected []string
	}{
		{
			name:     "empty map",
			input:    map[string]string{},
			expected: []string{},
		},
		{
			name: "single key",
			input: map[string]string{
				"key1": "value1",
			},
			expected: []string{"key1"},
		},
		{
			name: "multiple keys sorted",
			input: map[string]string{
				"zebra":  "z",
				"apple":  "a",
				"banana": "b",
			},
			expected: []string{"apple", "banana", "zebra"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetSortedKeys(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("len(GetSortedKeys()) = %d, want %d", len(got), len(tt.expected))
				return
			}

			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("GetSortedKeys()[%d] = %q, want %q", i, got[i], tt.expected[i])
				}
			}
		})
	}
}
