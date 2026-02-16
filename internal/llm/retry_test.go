package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{"nil", nil, false},
		{"EOF", io.EOF, true},
		{"UnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"connection reset", errors.New("read tcp: connection reset by peer"), true},
		{"connection refused", errors.New("dial tcp: connection refused"), true},
		{"i/o timeout", errors.New("i/o timeout"), true},
		{"no such host", errors.New("dial tcp: no such host"), true},
		{"429", errors.New("status 429: too many requests"), true},
		{"503", errors.New("status 503: service unavailable"), true},
		{"500", errors.New("status 500: internal server error"), true},
		{"502", errors.New("status 502: bad gateway"), true},
		{"529", errors.New("status 529: overloaded"), true},
		{"overloaded_error", errors.New("overloaded_error: too many requests"), true},
		{"server_error", errors.New("server_error: internal failure"), true},
		{"permanent: bad request", errors.New("invalid JSON in request body"), false},
		{"permanent: auth", errors.New("authentication failed: invalid key"), false},
		{"permanent: generic", errors.New("something completely unknown"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetryableError(tt.err)
			if got != tt.retryable {
				t.Errorf("IsRetryableError(%v) = %v, want %v", tt.err, got, tt.retryable)
			}
		})
	}
}

func TestRetryLLMCall_SucceedsAfterRetry(t *testing.T) {
	var calls int32
	logger := log.New(os.Stderr, "[test] ", 0)

	resp, err := RetryLLMCall(context.Background(), 3, logger, func() (*Response, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return nil, io.EOF // retryable
		}
		return &Response{StopReason: "end_turn"}, nil
	})

	if err != nil {
		t.Fatalf("expected success, got: %v", err)
	}
	if resp.StopReason != "end_turn" {
		t.Errorf("expected end_turn, got %s", resp.StopReason)
	}
	if atomic.LoadInt32(&calls) != 3 {
		t.Errorf("expected 3 calls, got %d", atomic.LoadInt32(&calls))
	}
}

func TestRetryLLMCall_GivesUpAfterMax(t *testing.T) {
	var calls int32
	logger := log.New(os.Stderr, "[test] ", 0)

	_, err := RetryLLMCall(context.Background(), 3, logger, func() (*Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, fmt.Errorf("status 503: service unavailable")
	})

	if err == nil {
		t.Fatal("expected error")
	}
	// 1 initial + 3 retries = 4 total
	if atomic.LoadInt32(&calls) != 4 {
		t.Errorf("expected 4 calls, got %d", atomic.LoadInt32(&calls))
	}
}

func TestRetryLLMCall_NoRetryOnPermanent(t *testing.T) {
	var calls int32
	logger := log.New(os.Stderr, "[test] ", 0)

	_, err := RetryLLMCall(context.Background(), 3, logger, func() (*Response, error) {
		atomic.AddInt32(&calls, 1)
		return nil, fmt.Errorf("invalid JSON in request body")
	})

	if err == nil {
		t.Fatal("expected error")
	}
	// Should only call once — error is not retryable
	if atomic.LoadInt32(&calls) != 1 {
		t.Errorf("expected 1 call (no retry), got %d", atomic.LoadInt32(&calls))
	}
}

func TestRetryLLMCall_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := log.New(os.Stderr, "[test] ", 0)

	var calls int32
	_, err := RetryLLMCall(ctx, 3, logger, func() (*Response, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			// First call fails with retryable error, then cancel context
			cancel()
			return nil, io.EOF
		}
		return &Response{StopReason: "end_turn"}, nil
	})

	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got: %v", err)
	}

	// Allow a small window for goroutine scheduling
	time.Sleep(10 * time.Millisecond)
	if atomic.LoadInt32(&calls) > 1 {
		t.Errorf("expected at most 1 call, got %d", atomic.LoadInt32(&calls))
	}
}
