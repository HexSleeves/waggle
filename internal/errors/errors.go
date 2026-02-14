// Package errors provides error classification and panic recovery for workers.
// It distinguishes between retryable errors (network, timeout) and permanent
// errors (bad command, not found) to enable intelligent retry strategies.
package errors

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrorType categorizes errors for retry decisions
type ErrorType string

const (
	// ErrorTypeRetryable indicates the error might succeed on retry
	ErrorTypeRetryable ErrorType = "retryable"
	// ErrorTypePermanent indicates the error will not succeed on retry
	ErrorTypePermanent ErrorType = "permanent"
	// ErrorTypePanic indicates a panic was recovered
	ErrorTypePanic ErrorType = "panic"
)

// RetryableError represents errors that may succeed on retry
// Examples: network timeouts, rate limits, temporary unavailability
type RetryableError struct {
	Err  error
	Kind string
}

func (e *RetryableError) Error() string {
	if e.Kind != "" {
		return fmt.Sprintf("[retryable:%s] %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("[retryable] %v", e.Err)
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

// PermanentError represents errors that will not succeed on retry
// Examples: bad commands, file not found, invalid arguments
type PermanentError struct {
	Err  error
	Kind string
}

func (e *PermanentError) Error() string {
	if e.Kind != "" {
		return fmt.Sprintf("[permanent:%s] %v", e.Kind, e.Err)
	}
	return fmt.Sprintf("[permanent] %v", e.Err)
}

func (e *PermanentError) Unwrap() error {
	return e.Err
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var re *RetryableError
	if errors.As(err, &re) {
		return true
	}
	// Check error message patterns for retryable errors
	return ClassifyError(err) == ErrorTypeRetryable
}

// IsPermanent checks if an error is permanent
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	var pe *PermanentError
	if errors.As(err, &pe) {
		return true
	}
	return ClassifyError(err) == ErrorTypePermanent
}

// ClassifyError determines the error type based on error message patterns
// and exit codes. It categorizes errors as retryable or permanent.
func ClassifyError(err error) ErrorType {
	if err == nil {
		return ErrorTypePermanent // Default for nil
	}

	msg := strings.ToLower(err.Error())

	// Retryable error patterns
	retryablePatterns := []string{
		// Network errors
		"connection refused",
		"connection reset",
		"no such host",
		"timeout",
		"deadline exceeded",
		"temporary failure",
		"network is unreachable",
		"connection timed out",
		// Rate limiting
		"rate limit",
		"too many requests",
		"429",
		"503",
		"service unavailable",
		"temporarily unavailable",
		// API errors
		"internal server error",
		"502",
		"504",
		"gateway timeout",
		// Resource exhaustion (might recover)
		"resource temporarily unavailable",
		"try again",
		"busy",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(msg, pattern) {
			return ErrorTypeRetryable
		}
	}

	// Permanent error patterns
	permanentPatterns := []string{
		// Command errors
		"command not found",
		"executable file not found",
		"no such file or directory",
		"permission denied",
		// Invalid input
		"invalid argument",
		"bad request",
		"invalid syntax",
		"parse error",
		"unrecognized",
		"unknown",
		// Not found
		"not found",
		"404",
		// Authentication (won't fix with retry)
		"unauthorized",
		"forbidden",
		"401",
		"403",
		// Logic errors
		"panic:",
		"runtime error",
		"index out of range",
		"nil pointer",
	}

	for _, pattern := range permanentPatterns {
		if strings.Contains(msg, pattern) {
			return ErrorTypePermanent
		}
	}

	// Default: assume retryable for unknown errors
	// This is safer than failing permanently on unknown errors
	return ErrorTypeRetryable
}

// ClassifyErrorWithExitCode classifies errors considering process exit codes
func ClassifyErrorWithExitCode(err error, exitCode int) ErrorType {
	if err == nil {
		return ErrorTypePermanent
	}

	// Exit code classification
	switch exitCode {
	case 0:
		return ErrorTypePermanent // No error
	case 1:
		// General error - check message
		return ClassifyError(err)
	case 2:
		// Misuse of shell builtins (permanent)
		return ErrorTypePermanent
	case 126:
		// Command invoked cannot execute (permission or not executable)
		return ErrorTypePermanent
	case 127:
		// Command not found (permanent)
		return ErrorTypePermanent
	case 130:
		// Script terminated by Ctrl-C (retryable - user might want to retry)
		return ErrorTypeRetryable
	case 137:
		// SIGKILL - likely OOM, might succeed with more resources
		return ErrorTypeRetryable
	case 143:
		// SIGTERM - terminated, might be retryable
		return ErrorTypeRetryable
	default:
		// For other exit codes, use message-based classification
		return ClassifyError(err)
	}
}

// NewRetryableError wraps an error as retryable
func NewRetryableError(err error, kind string) error {
	return &RetryableError{Err: err, Kind: kind}
}

// NewPermanentError wraps an error as permanent
func NewPermanentError(err error, kind string) error {
	return &PermanentError{Err: err, Kind: kind}
}

// RecoveryResult holds the result of a recovered panic
type RecoveryResult struct {
	Recovered  bool
	PanicValue interface{}
	ErrorMsg   string
	ErrorType  ErrorType
	StackTrace string
}

// RecoverPanic recovers from a panic and returns a RecoveryResult.
// Use with defer:
//
//	defer func() {
//	    if r := errors.RecoverPanic(recover()); r.Recovered {
//	        // Handle recovered panic
//	    }
//	}()
func RecoverPanic(r interface{}) RecoveryResult {
	if r == nil {
		return RecoveryResult{Recovered: false}
	}

	result := RecoveryResult{
		Recovered:  true,
		PanicValue: r,
		ErrorType:  ErrorTypePanic,
	}

	switch v := r.(type) {
	case error:
		result.ErrorMsg = fmt.Sprintf("panic: %v", v)
	case string:
		result.ErrorMsg = fmt.Sprintf("panic: %s", v)
	default:
		result.ErrorMsg = fmt.Sprintf("panic: %+v", v)
	}

	return result
}

// CalculateBackoff calculates exponential backoff delay
// baseDelay: initial delay
// retryCount: current retry attempt (0-indexed)
// maxDelay: maximum delay cap
func CalculateBackoff(baseDelay time.Duration, retryCount int, maxDelay time.Duration) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}

	// Calculate: baseDelay * (2 ^ retryCount)
	delay := baseDelay * (1 << retryCount)

	if maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}

	return delay
}

// CalculateBackoffWithJitter calculates exponential backoff with random jitter
// jitterPercent: percentage of delay to use as jitter (e.g., 0.1 = 10%)
func CalculateBackoffWithJitter(baseDelay time.Duration, retryCount int, maxDelay time.Duration, jitterPercent float64) time.Duration {
	delay := CalculateBackoff(baseDelay, retryCount, maxDelay)

	if jitterPercent <= 0 {
		return delay
	}

	// Simple jitter: reduce delay by up to jitterPercent
	// In production, you might want crypto/rand for better randomness
	jitter := time.Duration(float64(delay) * jitterPercent)
	if jitter > 0 {
		delay -= jitter / 2 // Reduce by up to half of jitter range
	}

	return delay
}

// GetErrorType returns the ErrorType for any error
func GetErrorType(err error) ErrorType {
	if err == nil {
		return ErrorTypePermanent
	}

	var re *RetryableError
	if errors.As(err, &re) {
		return ErrorTypeRetryable
	}

	var pe *PermanentError
	if errors.As(err, &pe) {
		return ErrorTypePermanent
	}

	return ClassifyError(err)
}

// ErrorTypeFromString parses an ErrorType from string
func ErrorTypeFromString(s string) ErrorType {
	switch strings.ToLower(s) {
	case "retryable":
		return ErrorTypeRetryable
	case "permanent":
		return ErrorTypePermanent
	case "panic":
		return ErrorTypePanic
	default:
		return ErrorTypePermanent
	}
}
