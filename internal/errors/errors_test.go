package errors

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestErrorClassification tests the error classification logic
func TestErrorClassification(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorType
	}{
		// Retryable errors
		{"network timeout", errors.New("connection timeout"), ErrorTypeRetryable},
		{"connection refused", errors.New("connection refused"), ErrorTypeRetryable},
		{"rate limit", errors.New("rate limit exceeded"), ErrorTypeRetryable},
		{"503 service unavailable", errors.New("503 service unavailable"), ErrorTypeRetryable},
		{"gateway timeout", errors.New("504 gateway timeout"), ErrorTypeRetryable},
		{"temporary failure", errors.New("temporary failure, try again"), ErrorTypeRetryable},
		{"network unreachable", errors.New("network is unreachable"), ErrorTypeRetryable},
		{"deadline exceeded", errors.New("context deadline exceeded"), ErrorTypeRetryable},

		// Permanent errors
		{"command not found", errors.New("command not found"), ErrorTypePermanent},
		{"file not found", errors.New("no such file or directory"), ErrorTypePermanent},
		{"permission denied", errors.New("permission denied"), ErrorTypePermanent},
		{"invalid argument", errors.New("invalid argument"), ErrorTypePermanent},
		{"bad request", errors.New("bad request"), ErrorTypePermanent},
		{"unauthorized", errors.New("401 unauthorized"), ErrorTypePermanent},
		{"forbidden", errors.New("403 forbidden"), ErrorTypePermanent},
		{"404 not found", errors.New("404 not found"), ErrorTypePermanent},
		{"parse error", errors.New("parse error: invalid syntax"), ErrorTypePermanent},
		{"nil pointer", errors.New("runtime error: invalid memory address or nil pointer dereference"), ErrorTypeRetryable},

		// Edge cases
		{"nil error", nil, ErrorTypePermanent},
		{"unknown error", errors.New("something weird happened"), ErrorTypeRetryable}, // Default to retryable
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(tt.err)
			if result != tt.expected {
				t.Errorf("ClassifyError(%q) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestErrorClassificationWithExitCode tests classification with exit codes
func TestErrorClassificationWithExitCode(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		exitCode int
		expected ErrorType
	}{
		{"exit 0 - no error", errors.New("success"), 0, ErrorTypePermanent},
		{"exit 1 - general error", errors.New("general failure"), 1, ErrorTypeRetryable}, // Uses message-based classification
		{"exit 2 - misuse", errors.New("misuse of shell builtin"), 2, ErrorTypePermanent},
		{"exit 126 - not executable", errors.New("cannot execute"), 126, ErrorTypePermanent},
		{"exit 127 - command not found", errors.New("command not found"), 127, ErrorTypePermanent},
		{"exit 130 - sigint", errors.New("interrupted"), 130, ErrorTypeRetryable},
		{"exit 137 - sigkill/oom", errors.New("killed"), 137, ErrorTypeRetryable},
		{"exit 143 - sigterm", errors.New("terminated"), 143, ErrorTypeRetryable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyErrorWithExitCode(tt.err, tt.exitCode)
			if result != tt.expected {
				t.Errorf("ClassifyErrorWithExitCode(%q, %d) = %v, want %v", tt.err, tt.exitCode, result, tt.expected)
			}
		})
	}
}

// TestRetryableError tests the RetryableError type
func TestRetryableError(t *testing.T) {
	innerErr := errors.New("connection reset")
	err := &RetryableError{Err: innerErr, Kind: "network"}

	expected := "[retryable:network] connection reset"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}

	if !errors.Is(err, innerErr) {
		t.Error("expected errors.Is to match inner error")
	}

	if !IsRetryable(err) {
		t.Error("expected IsRetryable to return true")
	}

	if IsPermanent(err) {
		t.Error("expected IsPermanent to return false")
	}
}

// TestPermanentError tests the PermanentError type
func TestPermanentError(t *testing.T) {
	innerErr := errors.New("file not found")
	err := &PermanentError{Err: innerErr, Kind: "not_found"}

	expected := "[permanent:not_found] file not found"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}

	if !errors.Is(err, innerErr) {
		t.Error("expected errors.Is to match inner error")
	}

	if IsRetryable(err) {
		t.Error("expected IsRetryable to return false")
	}

	if !IsPermanent(err) {
		t.Error("expected IsPermanent to return true")
	}
}

// TestNewRetryableError tests the constructor
func TestNewRetryableError(t *testing.T) {
	err := NewRetryableError(errors.New("timeout"), "timeout")
	if !IsRetryable(err) {
		t.Error("expected error to be retryable")
	}
}

// TestNewPermanentError tests the constructor
func TestNewPermanentError(t *testing.T) {
	err := NewPermanentError(errors.New("not found"), "not_found")
	if !IsPermanent(err) {
		t.Error("expected error to be permanent")
	}
}

// TestRecoverPanic tests panic recovery
func TestRecoverPanic(t *testing.T) {
	tests := []struct {
		name           string
		panicValue     interface{}
		shouldRecover  bool
		expectedMsg    string
		expectedType   ErrorType
	}{
		{"no panic", nil, false, "", ""},
		{"panic with error", errors.New("runtime error"), true, "panic: runtime error", ErrorTypePanic},
		{"panic with string", "something went wrong", true, "panic: something went wrong", ErrorTypePanic},
		{"panic with int", 42, true, "panic: 42", ErrorTypePanic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result RecoveryResult

			func() {
				defer func() {
					result = RecoverPanic(recover())
				}()
				if tt.panicValue != nil {
					panic(tt.panicValue)
				}
			}()

			if result.Recovered != tt.shouldRecover {
				t.Errorf("Recovered = %v, want %v", result.Recovered, tt.shouldRecover)
			}

			if tt.shouldRecover {
				if result.ErrorMsg != tt.expectedMsg {
					t.Errorf("ErrorMsg = %q, want %q", result.ErrorMsg, tt.expectedMsg)
				}
				if result.ErrorType != tt.expectedType {
					t.Errorf("ErrorType = %v, want %v", result.ErrorType, tt.expectedType)
				}
				if result.PanicValue != tt.panicValue {
					t.Errorf("PanicValue = %v, want %v", result.PanicValue, tt.panicValue)
				}
			}
		})
	}
}

// TestRecoverPanicInFunction tests panic recovery inside a function
func TestRecoverPanicInFunction(t *testing.T) {
	panicFunc := func() (result RecoveryResult) {
		defer func() {
			result = RecoverPanic(recover())
		}()
		panic("test panic")
	}

	r := panicFunc()
	if !r.Recovered {
		t.Error("expected panic to be recovered")
	}
	if !strings.Contains(r.ErrorMsg, "test panic") {
		t.Errorf("expected error message to contain 'test panic', got %q", r.ErrorMsg)
	}
}

// TestCalculateBackoff tests exponential backoff calculation
func TestCalculateBackoff(t *testing.T) {
	tests := []struct {
		name       string
		baseDelay  time.Duration
		retryCount int
		maxDelay   time.Duration
		expected   time.Duration
	}{
		{"0 retries", 2 * time.Second, 0, 60 * time.Second, 2 * time.Second},
		{"1 retry", 2 * time.Second, 1, 60 * time.Second, 4 * time.Second},
		{"2 retries", 2 * time.Second, 2, 60 * time.Second, 8 * time.Second},
		{"3 retries", 2 * time.Second, 3, 60 * time.Second, 16 * time.Second},
		{"4 retries", 2 * time.Second, 4, 60 * time.Second, 32 * time.Second},
		{"5 retries", 2 * time.Second, 5, 60 * time.Second, 60 * time.Second}, // Capped at max
		{"negative retries", 2 * time.Second, -1, 60 * time.Second, 2 * time.Second}, // Treat as 0
		{"no max", 2 * time.Second, 10, 0, 2048 * time.Second}, // No cap
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateBackoff(tt.baseDelay, tt.retryCount, tt.maxDelay)
			if result != tt.expected {
				t.Errorf("CalculateBackoff(%v, %d, %v) = %v, want %v",
					tt.baseDelay, tt.retryCount, tt.maxDelay, result, tt.expected)
			}
		})
	}
}

// TestCalculateBackoffWithJitter tests backoff with jitter
func TestCalculateBackoffWithJitter(t *testing.T) {
	baseDelay := 2 * time.Second
	retryCount := 2
	maxDelay := 60 * time.Second
	jitterPercent := 0.1 // 10% jitter

	// Run multiple times to account for randomness
	for i := 0; i < 10; i++ {
		result := CalculateBackoffWithJitter(baseDelay, retryCount, maxDelay, jitterPercent)
		expectedBase := 8 * time.Second // 2 * 2^2
		minExpected := time.Duration(float64(expectedBase) * 0.95) // -5% due to jitter

		if result < minExpected || result > expectedBase {
			t.Errorf("CalculateBackoffWithJitter result %v outside expected range [%v, %v]",
				result, minExpected, expectedBase)
		}
	}
}

// TestGetErrorType tests getting error type for various errors
func TestGetErrorType(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorType
	}{
		{"nil", nil, ErrorTypePermanent},
		{"retryable error", &RetryableError{Err: errors.New("timeout")}, ErrorTypeRetryable},
		{"permanent error", &PermanentError{Err: errors.New("not found")}, ErrorTypePermanent},
		{"timeout error", errors.New("connection timeout"), ErrorTypeRetryable},
		{"not found error", errors.New("file not found"), ErrorTypePermanent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetErrorType(tt.err)
			if result != tt.expected {
				t.Errorf("GetErrorType(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

// TestErrorTypeFromString tests parsing error type from string
func TestErrorTypeFromString(t *testing.T) {
	tests := []struct {
		input    string
		expected ErrorType
	}{
		{"retryable", ErrorTypeRetryable},
		{"RETRYABLE", ErrorTypeRetryable},
		{"Retryable", ErrorTypeRetryable},
		{"permanent", ErrorTypePermanent},
		{"PERMANENT", ErrorTypePermanent},
		{"panic", ErrorTypePanic},
		{"PANIC", ErrorTypePanic},
		{"unknown", ErrorTypePermanent},
		{"", ErrorTypePermanent},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := ErrorTypeFromString(tt.input)
			if result != tt.expected {
				t.Errorf("ErrorTypeFromString(%q) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

// TestErrorWrapping tests that errors can be properly unwrapped
func TestErrorWrapping(t *testing.T) {
	inner := errors.New("root cause")
	
	// Test RetryableError wrapping
	retryable := &RetryableError{Err: inner, Kind: "network"}
	if !errors.Is(retryable, inner) {
		t.Error("RetryableError should wrap inner error")
	}

	// Test PermanentError wrapping
	permanent := &PermanentError{Err: inner, Kind: "not_found"}
	if !errors.Is(permanent, inner) {
		t.Error("PermanentError should wrap inner error")
	}
}

// TestIsRetryableAndPermanentEdgeCases tests edge cases
func TestIsRetryableAndPermanentEdgeCases(t *testing.T) {
	// Test nil error
	if IsRetryable(nil) {
		t.Error("IsRetryable(nil) should be false")
	}
	if IsPermanent(nil) {
		t.Error("IsPermanent(nil) should be false")
	}

	// Test wrapped errors
	inner := errors.New("timeout")
	wrapped := fmt.Errorf("wrapped: %w", NewRetryableError(inner, "network"))
	if !IsRetryable(wrapped) {
		t.Error("IsRetryable should work with wrapped retryable errors")
	}
}
