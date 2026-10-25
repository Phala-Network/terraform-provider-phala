package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/time/rate"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPacingDoesNotSerializeResponseCompletion(t *testing.T) {
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	transport := &pacedTransport{limiter: rate.NewLimiter(rate.Every(10*time.Millisecond), 1),
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			started <- struct{}{}
			<-release
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
	var workers sync.WaitGroup
	for i := 0; i < 3; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			req, _ := http.NewRequestWithContext(context.Background(), "GET", "https://example.test", nil)
			response, err := transport.RoundTrip(req)
			if err != nil {
				t.Error(err)
				return
			}
			response.Body.Close()
		}()
	}
	defer workers.Wait()
	defer close(release)
	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("requests waited for earlier responses instead of just the request budget")
		}
	}
}

func TestPacingHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &pacedTransport{limiter: rate.NewLimiter(1, 1),
		base: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("canceled request reached the API")
			return nil, nil
		})}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://example.test", nil)
	if _, err := transport.RoundTrip(req); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context cancellation", err)
	}
}

func TestProviderAliasesShareRequestBudget(t *testing.T) {
	t.Setenv("PHALA_CLOUD_REQUESTS_PER_MINUTE", "180")
	a, err := newProviderHTTPClient(t.Name(), "same-key")
	if err != nil {
		t.Fatal(err)
	}
	b, err := newProviderHTTPClient(t.Name()+"/", "same-key")
	if err != nil {
		t.Fatal(err)
	}
	if a.Transport.(*pacedTransport).limiter != b.Transport.(*pacedTransport).limiter {
		t.Fatal("aliases with one API key must not get independent quotas")
	}
	for _, invalid := range []string{"", "0", "-1", "unlimited"} {
		t.Setenv("PHALA_CLOUD_REQUESTS_PER_MINUTE", invalid)
		if _, err := newProviderHTTPClient(t.Name(), "other-key"); err == nil {
			t.Fatalf("accepted invalid rate %q", invalid)
		}
	}
}
