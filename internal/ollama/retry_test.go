package ollama

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestIsRetryableOnlyAllowsTransportAndTimeout(t *testing.T) {
	timeout := &net.DNSError{IsTimeout: true}
	if !IsRetryable(transportError{cause: errors.New("down")}) || !IsRetryable(context.DeadlineExceeded) || !IsRetryable(timeout) {
		t.Fatal("retryable error was rejected")
	}
	if IsRetryable(context.Canceled) || IsRetryable(llmError("invalid response")) || IsRetryable(nil) {
		t.Fatal("non-retryable error was accepted")
	}
}
