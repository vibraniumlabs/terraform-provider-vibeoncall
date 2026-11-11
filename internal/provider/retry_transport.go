package provider

import (
	"net/http"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	maxRetryAttempts = 5
	maxRetryWait     = 60 * time.Second
)

type retryTransport struct {
	base      http.RoundTripper
	baseDelay time.Duration
}

func newRetryTransport(base http.RoundTripper) *retryTransport {
	return &retryTransport{base: base, baseDelay: time.Second}
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 1; ; attempt++ {
		attemptReq := req
		if attempt > 1 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			attemptReq = req.Clone(req.Context())
			attemptReq.Body = body
		}

		resp, err := t.base.RoundTrip(attemptReq)
		if attempt == maxRetryAttempts || !shouldRetry(req.Method, resp, err) {
			return resp, err
		}

		wait := t.baseDelay << (attempt - 1)
		if resp != nil {
			if d, ok := parseRetryAfter(resp.Header.Get("Retry-After")); ok {
				wait = d
			}
			resp.Body.Close()
		}
		wait = min(max(wait, 0), maxRetryWait)
		tflog.Warn(req.Context(), "Retrying Vibe OnCall API request", map[string]any{
			"method": req.Method, "url": req.URL.String(), "attempt": attempt, "wait": wait.String(),
		})

		timer := time.NewTimer(wait)
		select {
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		case <-timer.C:
		}
	}
}

func parseRetryAfter(value string) (time.Duration, bool) {
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second, true
	}
	if at, err := http.ParseTime(value); err == nil {
		return time.Until(at), true
	}
	return 0, false
}

// Only 429 is safe to replay for writes: a 5xx or dropped connection may follow a write the server already applied.
func shouldRetry(method string, resp *http.Response, err error) bool {
	if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if method != http.MethodGet {
		return false
	}
	if err != nil {
		return true
	}
	switch resp.StatusCode {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}
