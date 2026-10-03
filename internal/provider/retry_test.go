package provider

import (
	"context"
	"io"
	"net/http"
	"testing"
)

func TestRetryPolicy_429_Retries(t *testing.T) {
	resp := &http.Response{StatusCode: 429}
	retry, err := retryPolicy(context.Background(), resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !retry {
		t.Error("expected retry on 429")
	}
}

func TestRetryPolicy_500_Retries(t *testing.T) {
	resp := &http.Response{StatusCode: 500}
	retry, err := retryPolicy(context.Background(), resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !retry {
		t.Error("expected retry on 500")
	}
}

func TestRetryPolicy_502_Retries(t *testing.T) {
	resp := &http.Response{StatusCode: 502}
	retry, err := retryPolicy(context.Background(), resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !retry {
		t.Error("expected retry on 502")
	}
}

func TestRetryPolicy_501_DoesNotRetry(t *testing.T) {
	resp := &http.Response{StatusCode: 501}
	retry, err := retryPolicy(context.Background(), resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retry {
		t.Error("expected no retry on 501")
	}
}

func TestRetryPolicy_200_DoesNotRetry(t *testing.T) {
	resp := &http.Response{StatusCode: 200}
	retry, err := retryPolicy(context.Background(), resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retry {
		t.Error("expected no retry on 200")
	}
}

func TestRetryPolicy_404_DoesNotRetry(t *testing.T) {
	resp := &http.Response{StatusCode: 404}
	retry, err := retryPolicy(context.Background(), resp, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if retry {
		t.Error("expected no retry on 404")
	}
}

func TestRetryPolicy_ConnectionError_Retries(t *testing.T) {
	retry, err := retryPolicy(context.Background(), nil, io.ErrUnexpectedEOF)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !retry {
		t.Error("expected retry on connection error")
	}
}

func TestRetryPolicy_CancelledContext_DoesNotRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	retry, err := retryPolicy(ctx, nil, nil)
	if err == nil {
		t.Fatal("expected context error")
	}
	if retry {
		t.Error("expected no retry on cancelled context")
	}
}
