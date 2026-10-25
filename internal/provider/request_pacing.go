package provider

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/time/rate"
)

// Phala's regular API-token quota is 180 requests/minute. Pace request starts,
// not operation completion: independent CVM boots and HTTP calls remain in flight.
const defaultRequestsPerMinute = 180

var requestBudgets = struct {
	sync.Mutex
	byCredential map[requestBudgetKey]*rate.Limiter
}{byCredential: make(map[requestBudgetKey]*rate.Limiter)}

type requestBudgetKey struct {
	baseURL string
	apiKey  string
}

type pacedTransport struct {
	limiter *rate.Limiter
	base    http.RoundTripper
}

func (t *pacedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := t.limiter.Wait(req.Context()); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(req)
}

func newProviderHTTPClient(baseURL, apiKey string) (*http.Client, error) {
	rpm := defaultRequestsPerMinute
	if raw, exists := os.LookupEnv("PHALA_CLOUD_REQUESTS_PER_MINUTE"); exists {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return nil, fmt.Errorf("PHALA_CLOUD_REQUESTS_PER_MINUTE must be a positive integer")
		}
		rpm = value
	}
	key := requestBudgetKey{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey}
	requestBudgets.Lock()
	limiter := requestBudgets.byCredential[key]
	if limiter == nil {
		limiter = rate.NewLimiter(rate.Limit(rpm)/60, 1)
		requestBudgets.byCredential[key] = limiter
	} else if limiter.Limit() != rate.Limit(rpm)/60 {
		requestBudgets.Unlock()
		return nil, fmt.Errorf("conflicting request rates for provider aliases sharing one API credential")
	}
	requestBudgets.Unlock()
	return &http.Client{Transport: &pacedTransport{limiter: limiter, base: http.DefaultTransport}}, nil
}
