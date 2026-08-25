package http

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewHTTPClient_DefaultOptions(t *testing.T) {
	client := NewHTTPClient(HTTPClientOptions{})

	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.Timeout != 0 {
		t.Errorf("expected zero timeout, got %v", client.Timeout)
	}
	if client.Transport != nil {
		t.Error("expected nil transport for default options")
	}
}

func TestNewHTTPClient_WithTimeout(t *testing.T) {
	timeout := 30 * time.Second
	client := NewHTTPClient(HTTPClientOptions{
		Timeout: timeout,
	})

	if client.Timeout != timeout {
		t.Errorf("expected timeout %v, got %v", timeout, client.Timeout)
	}
}

func TestNewHTTPClient_WithAllOptions(t *testing.T) {
	timeout := 60 * time.Second
	client := NewHTTPClient(HTTPClientOptions{
		Timeout:         timeout,
		BlockPrivateIPs: true,
	})

	if client.Timeout != timeout {
		t.Errorf("expected timeout %v, got %v", timeout, client.Timeout)
	}

	transport := client.Transport.(*http.Transport)
	if transport.DialContext == nil {
		t.Error("expected custom DialContext when BlockPrivateIPs is true")
	}
}

func TestHTTPClientOptions_ZeroValue(t *testing.T) {
	var opts HTTPClientOptions

	if opts.Timeout != 0 {
		t.Errorf("expected zero timeout, got %v", opts.Timeout)
	}
	if opts.BlockPrivateIPs {
		t.Error("expected BlockPrivateIPs to be false")
	}
}

// Verify the client can be used to make requests (compile-time interface check)
func TestNewHTTPClient_ImplementsInterface(t *testing.T) {
	client := NewHTTPClient(HTTPClientOptions{})

	// Verify the client has the expected methods
	var _ interface {
		Do(*http.Request) (*http.Response, error)
	} = client

	// Type assertion to ensure it's a proper *http.Client
	var _ *http.Client = client
}

func TestNewHTTPClient_WithBlockPrivateIPs(t *testing.T) {
	client := NewHTTPClient(HTTPClientOptions{
		BlockPrivateIPs: true,
	})

	if client.Transport == nil {
		t.Fatal("expected non-nil transport when BlockPrivateIPs is true")
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}

	if transport.DialContext == nil {
		t.Fatal("expected non-nil DialContext when BlockPrivateIPs is true")
	}
}

func TestIsBlockedIP(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected bool
	}{
		{"loopback IPv4", "127.0.0.1", true},
		{"loopback IPv6", "::1", true},
		{"private RFC1918 10.x", "10.0.0.5", true},
		{"private RFC1918 172.16.x", "172.16.0.5", true},
		{"private RFC1918 192.168.x", "192.168.1.5", true},
		{"link-local / cloud metadata", "169.254.169.254", true},
		{"unspecified", "0.0.0.0", true},
		{"multicast", "224.0.0.1", true},
		{"public IPv4", "8.8.8.8", false},
		{"public IPv6", "2001:4860:4860::8888", false},
		{"carrier-grade NAT", "100.64.0.1", true},
		{"NAT64-embedded metadata IP", "64:ff9b::169.254.169.254", true},
		{"NAT64-embedded public IP", "64:ff9b::8.8.8.8", false},
		{"deprecated IPv4-compatible metadata IP", "::169.254.169.254", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse test IP: %s", tt.ip)
			}
			if result := isBlockedIP(ip); result != tt.expected {
				t.Errorf("isBlockedIP(%s) = %v, expected %v", tt.ip, result, tt.expected)
			}
		})
	}
}

func TestNewHTTPClient_BlockPrivateIPs_RejectsLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient(HTTPClientOptions{BlockPrivateIPs: true})

	// httptest servers listen on loopback (127.0.0.1), which must be blocked
	_, err := client.Get(server.URL)
	if err == nil {
		t.Fatal("expected error when connecting to loopback address with BlockPrivateIPs enabled")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected error mentioning blocked address, got: %v", err)
	}
}

func TestNewHTTPClient_AllowedPrivateHost_BypassesBlockForExactHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("failed to split test server address: %v", err)
	}

	client := NewHTTPClient(HTTPClientOptions{
		BlockPrivateIPs:    true,
		AllowedPrivateHost: "localhost",
	})

	resp, err := client.Get("http://localhost:" + port)
	if err != nil {
		t.Fatalf("expected allowed host to bypass the private-IP block, got: %v", err)
	}
	resp.Body.Close()
}

func TestNewHTTPClient_AllowedPrivateHost_StillBlocksOtherHosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewHTTPClient(HTTPClientOptions{
		BlockPrivateIPs:    true,
		AllowedPrivateHost: "localhost",
	})

	// server.URL is http://127.0.0.1:PORT -- a different literal hostname than
	// the allowed "localhost", even though both resolve to loopback. This is
	// what protects a trusted host's redirect from reaching an unrelated
	// private target: only the exact allowed hostname is exempted.
	_, err := client.Get(server.URL)
	if err == nil {
		t.Fatal("expected a non-allowed host on a private address to be blocked")
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected error mentioning blocked address, got: %v", err)
	}
}

func TestNewHTTPClient_UsesEnvironmentProxy(t *testing.T) {
	client := NewHTTPClient(HTTPClientOptions{BlockPrivateIPs: true})
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if transport.Proxy == nil {
		t.Fatal("expected Proxy to be set so HTTP_PROXY/HTTPS_PROXY env vars are honored")
	}
}
