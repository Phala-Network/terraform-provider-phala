package provider

import (
	"strings"
	"testing"

	phala "github.com/Phala-Network/phala-cloud/sdks/go"
)

func TestClientUserAgent(t *testing.T) {
	got := clientUserAgent("0.3.0-beta.5", "1.9.8")
	wantPrefix := "terraform-provider-phala/0.3.0-beta.5 terraform/1.9.8 "
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("user agent = %q, want prefix %q", got, wantPrefix)
	}
	rest := strings.TrimPrefix(got, wantPrefix)
	if rest != phala.DefaultUserAgent() {
		t.Fatalf("user agent suffix = %q, want DefaultUserAgent %q", rest, phala.DefaultUserAgent())
	}
	if !strings.HasPrefix(rest, "phala-cloud-sdk-go/") {
		t.Fatalf("DefaultUserAgent missing sdk product token: %q", rest)
	}
}
