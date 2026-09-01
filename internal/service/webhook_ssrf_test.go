package service

import (
	"net/http"
	"net/netip"
	"testing"
)

func TestIsPublicWebhookIP(t *testing.T) {
	tests := []struct {
		name   string
		ip     string
		public bool
	}{
		{name: "public IPv4", ip: "8.8.8.8", public: true},
		{name: "public IPv6", ip: "2606:4700:4700::1111", public: true},
		{name: "loopback", ip: "127.0.0.1"},
		{name: "private IPv4", ip: "10.0.0.1"},
		{name: "carrier NAT", ip: "100.64.0.1"},
		{name: "link local", ip: "169.254.169.254"},
		{name: "benchmark", ip: "198.18.0.1"},
		{name: "documentation", ip: "203.0.113.1"},
		{name: "6to4 relay anycast", ip: "192.88.99.1"},
		{name: "mapped private IPv4", ip: "::ffff:10.0.0.1"},
		{name: "IPv4 compatible", ip: "::8.8.8.8"},
		{name: "Teredo", ip: "2001:0000:4136:e378:8000:63bf:3fff:fdd2"},
		{name: "6to4", ip: "2002:0808:0808::1"},
		{name: "well-known NAT64", ip: "64:ff9b::808:808"},
		{name: "local-use NAT64", ip: "64:ff9b:1::808:808"},
		{name: "deprecated site local", ip: "fec0::1"},
		{name: "IPv6 documentation", ip: "2001:db8::1"},
		{name: "multicast", ip: "ff02::1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPublicWebhookIP(netip.MustParseAddr(tt.ip)); got != tt.public {
				t.Fatalf("isPublicWebhookIP(%s) = %v, want %v", tt.ip, got, tt.public)
			}
		})
	}
}

func TestWebhookHTTPClientDisablesRedirectsAndEnvironmentProxy(t *testing.T) {
	client := newWebhookHTTPClient(5)
	if err := client.CheckRedirect(nil, nil); err == nil {
		t.Fatal("webhook redirects must be rejected")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("webhook transport must not use environment proxies")
	}
}
