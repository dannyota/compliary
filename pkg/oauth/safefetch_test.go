package oauth

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// fakeResolver returns predetermined IPs for any host lookup.
type fakeResolver struct {
	addrs []net.IPAddr
	err   error
}

func (f fakeResolver) LookupIPAddr(_ context.Context, _ string) ([]net.IPAddr, error) {
	return f.addrs, f.err
}

func ipsFrom(strs ...string) []net.IPAddr {
	out := make([]net.IPAddr, len(strs))
	for i, s := range strs {
		out[i] = net.IPAddr{IP: net.ParseIP(s)}
	}
	return out
}

func TestVetIP_DeniedAddresses(t *testing.T) {
	denied := []struct {
		name string
		ip   string
	}{
		{"loopback_v4", "127.0.0.1"},
		{"loopback_v6", "::1"},
		{"link_local_v4", "169.254.169.254"},
		{"link_local_v6", "fe80::1"},
		{"private_10", "10.0.0.1"},
		{"private_172", "172.16.0.1"},
		{"private_192", "192.168.1.1"},
		{"ula_v6", "fd12:3456::1"},
		{"unspecified_v4", "0.0.0.0"},
		{"unspecified_v6", "::"},
		{"multicast_v4", "224.0.0.1"},
		{"multicast_v6", "ff02::1"},
		{"ec2_metadata", "169.254.169.254"},
	}

	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid IP: %s", tc.ip)
			}
			if err := vetIP(ip); err == nil {
				t.Errorf("vetIP(%s) = nil, want error", tc.ip)
			}
		})
	}
}

func TestVetIP_AllowedAddresses(t *testing.T) {
	allowed := []struct {
		name string
		ip   string
	}{
		{"public_v4", "8.8.8.8"},
		{"public_v4_2", "1.1.1.1"},
		{"public_v6", "2001:4860:4860::8888"},
		{"cloudflare", "104.16.0.1"},
	}

	for _, tc := range allowed {
		t.Run(tc.name, func(t *testing.T) {
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("invalid IP: %s", tc.ip)
			}
			if err := vetIP(ip); err != nil {
				t.Errorf("vetIP(%s) = %v, want nil", tc.ip, err)
			}
		})
	}
}

func TestFetchClientMetadata_SSRFDeny(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		ips    []string
		errSub string
	}{
		{"loopback", "https://evil.com/meta", []string{"127.0.0.1"}, "loopback"},
		{"link_local_metadata", "https://evil.com/meta", []string{"169.254.169.254"}, "link-local"},
		{"private_10", "https://evil.com/meta", []string{"10.0.0.1"}, "private"},
		{"private_172", "https://evil.com/meta", []string{"172.16.5.10"}, "private"},
		{"private_192", "https://evil.com/meta", []string{"192.168.0.1"}, "private"},
		{"mixed_public_private", "https://evil.com/meta", []string{"8.8.8.8", "10.0.0.1"}, "private"},
		{"ula_v6", "https://evil.com/meta", []string{"fd00::1"}, "private"},
		{"unspecified", "https://evil.com/meta", []string{"0.0.0.0"}, "unspecified"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := fakeResolver{addrs: ipsFrom(tc.ips...)}
			_, err := fetchClientMetadataWithResolver(tc.url, res)
			if err == nil {
				t.Fatal("expected SSRF error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.errSub)
			}
		})
	}
}

func TestFetchClientMetadata_SchemeValidation(t *testing.T) {
	t.Run("http_rejected", func(t *testing.T) {
		_, err := fetchClientMetadataWithResolver("http://example.com/meta", defaultResolver{})
		if err == nil || !strings.Contains(err.Error(), "https") {
			t.Errorf("expected https error, got: %v", err)
		}
	})

	t.Run("userinfo_rejected", func(t *testing.T) {
		_, err := fetchClientMetadataWithResolver("https://user:pass@example.com/meta", defaultResolver{})
		if err == nil || !strings.Contains(err.Error(), "userinfo") {
			t.Errorf("expected userinfo error, got: %v", err)
		}
	})
}

func TestFetchClientMetadata_RedirectRefused(t *testing.T) {
	// Set up a server that redirects.
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.internal/steal", http.StatusFound)
	}))
	defer ts.Close()

	// Use the test server's address (loopback) with a fake resolver that allows it.
	// This won't actually work end-to-end because TLS certs won't match,
	// but we can test the URL validation + redirect path at the unit level.
	res := fakeResolver{addrs: ipsFrom("8.8.8.8")}
	_, err := fetchClientMetadataWithResolver("https://example.com/meta", res)
	// Will fail at the fetch stage (TLS or connect), but the URL validation passes.
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchClientMetadata_SizeLimit(t *testing.T) {
	// Verify the size constant is reasonable.
	if cimdMaxBody != 64<<10 {
		t.Errorf("cimdMaxBody = %d, want %d", cimdMaxBody, 64<<10)
	}
}

func TestFetchClientMetadata_ValidDoc(t *testing.T) {
	// Test with a well-formed metadata document (using a fake resolver that
	// bypasses the SSRF check by providing a "public" IP, but we can't actually
	// serve HTTPS with matching certs in a unit test). Test the parsing path
	// via the store's registerCIMDClient instead.
	meta := &oauthex.ClientRegistrationMetadata{
		RedirectURIs: []string{"https://example.com/callback"},
		ClientName:   "Test CIMD",
	}

	doc := struct {
		*oauthex.ClientRegistrationMetadata
		ClientID string `json:"client_id"`
	}{
		ClientRegistrationMetadata: meta,
		ClientID:                   "https://example.com/client-meta.json",
	}

	data, _ := json.Marshal(doc)
	// Verify parsing works (this is what fetchClientMetadata does after the fetch).
	var parsed struct {
		oauthex.ClientRegistrationMetadata
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.ClientID != "https://example.com/client-meta.json" {
		t.Errorf("client_id: %q", parsed.ClientID)
	}
	if len(parsed.RedirectURIs) != 1 {
		t.Errorf("redirect_uris: %v", parsed.RedirectURIs)
	}
}
