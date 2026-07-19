package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

const cimdMaxBody = 64 << 10 // 64 KiB — metadata docs are small.

// resolver abstracts DNS lookup for testing.
type resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// defaultResolver uses the system DNS.
type defaultResolver struct{}

func (defaultResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

// fetchClientMetadata fetches a Client ID Metadata Document from the given HTTPS
// URL with SSRF protection: the resolved IPs are checked against a deny list
// (loopback, link-local including 169.254.0.0/16, private RFC 1918, ULA, multicast,
// unspecified), and the HTTP client is pinned to the vetted IPs to prevent
// DNS-rebinding. Redirects are refused (simplest safe posture — metadata documents
// should not redirect).
func fetchClientMetadata(metadataURL string) (*oauthex.ClientRegistrationMetadata, error) {
	return fetchClientMetadataWithResolver(metadataURL, defaultResolver{})
}

func fetchClientMetadataWithResolver(metadataURL string, res resolver) (*oauthex.ClientRegistrationMetadata, error) {
	u, err := url.Parse(metadataURL)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}

	// Require HTTPS, no userinfo.
	if u.Scheme != "https" {
		return nil, fmt.Errorf("CIMD URL must use https, got %q", u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("CIMD URL must not contain userinfo")
	}

	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("CIMD URL has no hostname")
	}

	// Resolve and vet all IPs.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	addrs, err := res.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}

	var vetted []net.IPAddr
	for _, addr := range addrs {
		if err := vetIP(addr.IP); err != nil {
			return nil, fmt.Errorf("SSRF: %s resolves to %s: %w", host, addr.IP, err)
		}
		vetted = append(vetted, addr)
	}

	// Build HTTP client pinned to vetted IPs (prevents DNS rebinding).
	port := u.Port()
	if port == "" {
		port = "443"
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Use the first vetted IP. All were checked above.
			target := net.JoinHostPort(vetted[0].IP.String(), port)
			return dialer.DialContext(ctx, network, target)
		},
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		// Refuse redirects — metadata docs must not redirect.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	resp, err := client.Get(metadataURL)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	// Treat redirects as failure.
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("fetch: redirect refused (status %d)", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, cimdMaxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if len(body) > cimdMaxBody {
		return nil, fmt.Errorf("CIMD response exceeds %d bytes", cimdMaxBody)
	}

	var doc struct {
		oauthex.ClientRegistrationMetadata
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	// The client_id in the document must match the URL.
	if doc.ClientID != metadataURL {
		return nil, fmt.Errorf("client_id mismatch: got %q, want %q", doc.ClientID, metadataURL)
	}

	if len(doc.RedirectURIs) == 0 {
		return nil, fmt.Errorf("no redirect_uris in metadata document")
	}

	return &doc.ClientRegistrationMetadata, nil
}

// vetIP rejects IPs that are loopback, link-local (169.254/16, fe80::/10),
// private (RFC 1918, ULA fc00::/7), unspecified, or multicast.
func vetIP(ip net.IP) error {
	switch {
	case ip.IsLoopback():
		return fmt.Errorf("loopback address")
	case ip.IsLinkLocalUnicast():
		return fmt.Errorf("link-local address")
	case ip.IsLinkLocalMulticast():
		return fmt.Errorf("link-local multicast address")
	case ip.IsMulticast():
		return fmt.Errorf("multicast address")
	case ip.IsUnspecified():
		return fmt.Errorf("unspecified address")
	case isPrivate(ip):
		return fmt.Errorf("private address")
	}
	return nil
}

// isPrivate checks RFC 1918 + ULA (fc00::/7).
func isPrivate(ip net.IP) bool {
	// net.IP.IsPrivate covers RFC 1918 (10/8, 172.16/12, 192.168/16) and ULA (fc00::/7).
	return ip.IsPrivate()
}
