package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serveStatuses(t *testing.T, statuses ...int) (*httptest.Server, *atomic.Int32, *[]string) {
	var calls atomic.Int32
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(body))
		status := statuses[min(int(calls.Add(1))-1, len(statuses)-1)]
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "0")
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, &bodies
}

func send(t *testing.T, ctx context.Context, method, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	transport := newRetryTransport(http.DefaultTransport)
	transport.baseDelay = time.Millisecond
	return (&http.Client{Transport: transport}).Do(req)
}

func TestRetryTransportReplaysWritesWithBodyAfterRateLimit(t *testing.T) {
	srv, calls, bodies := serveStatuses(t, http.StatusTooManyRequests, http.StatusCreated)

	resp, err := send(t, context.Background(), http.MethodPost, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || calls.Load() != 2 {
		t.Fatalf("got status %d after %d calls, want 201 after 2", resp.StatusCode, calls.Load())
	}
	if (*bodies)[1] != `{"name":"x"}` {
		t.Fatalf("retried body = %q", (*bodies)[1])
	}
}

func TestRetryTransportRetriesReadsOnGatewayErrors(t *testing.T) {
	srv, calls, _ := serveStatuses(t, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusOK)

	resp, err := send(t, context.Background(), http.MethodGet, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || calls.Load() != 3 {
		t.Fatalf("got status %d after %d calls, want 200 after 3", resp.StatusCode, calls.Load())
	}
}

func TestRetryTransportDoesNotRetryWritesOnGatewayErrors(t *testing.T) {
	srv, calls, _ := serveStatuses(t, http.StatusBadGateway, http.StatusCreated)

	resp, err := send(t, context.Background(), http.MethodPost, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadGateway || calls.Load() != 1 {
		t.Fatalf("got status %d after %d calls, want 502 after 1", resp.StatusCode, calls.Load())
	}
}

func TestRetryTransportDoesNotRetryInternalServerErrors(t *testing.T) {
	srv, calls, _ := serveStatuses(t, http.StatusInternalServerError, http.StatusOK)

	resp, err := send(t, context.Background(), http.MethodGet, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusInternalServerError || calls.Load() != 1 {
		t.Fatalf("got status %d after %d calls, want 500 after 1", resp.StatusCode, calls.Load())
	}
}

func TestRetryTransportReturnsLastResponseAfterMaxAttempts(t *testing.T) {
	srv, calls, _ := serveStatuses(t, http.StatusTooManyRequests)

	resp, err := send(t, context.Background(), http.MethodGet, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTooManyRequests || calls.Load() != maxRetryAttempts {
		t.Fatalf("got status %d after %d calls, want 429 after %d", resp.StatusCode, calls.Load(), maxRetryAttempts)
	}
}

func TestRetryTransportStopsWaitingWhenContextIsCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := send(t, ctx, http.MethodGet, srv.URL)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
}

func TestParseRetryAfterAcceptsSecondsAndHTTPDates(t *testing.T) {
	if d, ok := parseRetryAfter("7"); !ok || d != 7*time.Second {
		t.Errorf("seconds: got %v, %v", d, ok)
	}
	inThirty := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if d, ok := parseRetryAfter(inThirty); !ok || d < 28*time.Second || d > 30*time.Second {
		t.Errorf("HTTP date: got %v, %v", d, ok)
	}
	if _, ok := parseRetryAfter("soon"); ok {
		t.Error("invalid value was accepted")
	}
}
