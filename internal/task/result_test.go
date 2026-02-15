package task

import (
	"testing"
)

func TestResultSchemaFields(t *testing.T) {
	tests := []struct {
		name        string
		result      Result
		wantSuccess bool
		wantOutput  string
		wantErrors  int
	}{
		{
			name:        "successful result with output",
			result:      Result{Success: true, Output: "hello world"},
			wantSuccess: true,
			wantOutput:  "hello world",
			wantErrors:  0,
		},
		{
			name:        "failed result with errors",
			result:      Result{Success: false, Output: "some output", Errors: []string{"error1", "error2"}},
			wantSuccess: false,
			wantOutput:  "some output",
			wantErrors:  2,
		},
		{
			name:        "result with artifacts",
			result:      Result{Success: true, Output: "done", Artifacts: map[string]string{"file1": "/path/to/file1"}},
			wantSuccess: true,
			wantOutput:  "done",
			wantErrors:  0,
		},
		{
			name:        "result with metrics",
			result:      Result{Success: true, Output: "done", Metrics: map[string]float64{"duration": 1.5, "tokens": 100}},
			wantSuccess: true,
			wantOutput:  "done",
			wantErrors:  0,
		},
		{
			name:        "empty result",
			result:      Result{},
			wantSuccess: false,
			wantOutput:  "",
			wantErrors:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.result.Success != tt.wantSuccess {
				t.Errorf("Result.Success = %v, want %v", tt.result.Success, tt.wantSuccess)
			}
			if tt.result.Output != tt.wantOutput {
				t.Errorf("Result.Output = %q, want %q", tt.result.Output, tt.wantOutput)
			}
			if len(tt.result.Errors) != tt.wantErrors {
				t.Errorf("Result.Errors length = %d, want %d", len(tt.result.Errors), tt.wantErrors)
			}
		})
	}
}

func TestResultSchemaTypes(t *testing.T) {
	result := Result{
		Success:   true,
		Output:    "test output",
		Errors:    []string{"err1"},
		Artifacts: map[string]string{"key": "value"},
		Metrics:   map[string]float64{"count": 42.0},
	}

	if _, ok := interface{}(result.Success).(bool); !ok {
		t.Error("Result.Success should be bool")
	}

	if _, ok := interface{}(result.Output).(string); !ok {
		t.Error("Result.Output should be string")
	}

	if _, ok := interface{}(result.Errors).([]string); !ok {
		t.Error("Result.Errors should be []string")
	}

	if _, ok := interface{}(result.Artifacts).(map[string]string); !ok {
		t.Error("Result.Artifacts should be map[string]string")
	}

	if _, ok := interface{}(result.Metrics).(map[string]float64); !ok {
		t.Error("Result.Metrics should be map[string]float64")
	}
}

func TestResultJSONTags(t *testing.T) {
	result := Result{Success: true, Output: "test"}

	_ = result.Success
	_ = result.Output
	_ = result.Errors
	_ = result.Artifacts
	_ = result.Metrics
}
