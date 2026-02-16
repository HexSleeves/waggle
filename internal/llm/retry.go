package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"
)

// IsRetryableError checks if an LLM API error is worth retrying.
// It covers common transient failures: network errors, rate limits,
// server errors, and provider-specific overload conditions.
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Standard io errors
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}

	msg := strings.ToLower(err.Error())

	retryablePatterns := []string{
		"connection reset",
		"connection refused",
		"i/o timeout",
		"no such host",
		"overloaded_error",
		"server_error",
	}
	for _, p := range retryablePatterns {
		if strings.Contains(msg, p) {
			return true
		}
	}

	// HTTP status codes that are retryable
	retryableCodes := []int{429, 500, 502, 503, 529}
	for _, code := range retryableCodes {
		// Match patterns like "status 429", "status: 429", "429 too many", "http 503"
		codeStr := fmt.Sprintf("%d", code)
		if strings.Contains(msg, codeStr) {
			return true
		}
	}

	return false
}

// RetryLLMCall retries an LLM function with exponential backoff.
// maxRetries is the number of retry attempts (not counting the initial call).
// Backoff schedule: 1s, 2s, 4s, etc.
// Only retries if IsRetryableError returns true for the error.
func RetryLLMCall(ctx context.Context, maxRetries int, logger *log.Logger, fn func() (*Response, error)) (*Response, error) {
	resp, err := fn()
	if err == nil {
		return resp, nil
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		if !IsRetryableError(err) {
			return nil, err
		}

		// Check context before sleeping
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		backoff := time.Second * (1 << uint(attempt)) // 1s, 2s, 4s
		if logger != nil {
			logger.Printf("⚠ LLM call failed: %v, retrying in %v (attempt %d/%d)", err, backoff, attempt+1, maxRetries)
		}

		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		resp, err = fn()
		if err == nil {
			return resp, nil
		}
	}

	return nil, err
}
