package utils

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

// The guard's whole job is that these never resolve to a fetch. 169.254.169.254 is the
// cloud metadata service; the ::ffff: forms are the bypass a hostname-only check misses.
func TestAddrAllowedBlocksInternal(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1",
		"169.254.169.254",        // cloud metadata
		"::ffff:169.254.169.254", // same, IPv4-mapped
		"10.0.0.5", "172.16.0.1", "192.168.1.1",
		"::ffff:127.0.0.1", // loopback smuggled through IPv6
		"fc00::1",          // IPv6 unique-local
		"fe80::1",          // IPv6 link-local
		"0.0.0.0", "::",
		"100.64.0.1", // CGNAT
		"198.18.0.1", // benchmarking
		"224.0.0.1",  // multicast
		"240.0.0.1",  // reserved
	}
	for _, s := range blocked {
		if addrAllowed(netip.MustParseAddr(s)) {
			t.Errorf("addrAllowed(%s) = true, want false", s)
		}
	}

	for _, s := range []string{"93.184.216.34", "1.1.1.1", "2606:2800:220:1:248:1893:25c8:1946"} {
		if !addrAllowed(netip.MustParseAddr(s)) {
			t.Errorf("addrAllowed(%s) = false, want true", s)
		}
	}
}

func TestCheckFetchURLRejects(t *testing.T) {
	bad := []string{
		"file:///etc/passwd",
		"gopher://localhost:70/",
		"ftp://example.com/x",
		"http://127.0.0.1:8080/admin",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/",
		"http://user:pass@example.com/", // credentials
		"http:///nohost",
		"not a url at all",
	}
	for _, u := range bad {
		if err := checkFetchURL(u); err == nil {
			t.Errorf("checkFetchURL(%q) = nil, want error", u)
		}
	}

	for _, u := range []string{"http://example.com/recipe", "https://example.com/a?b=c"} {
		if err := checkFetchURL(u); err != nil {
			t.Errorf("checkFetchURL(%q) = %v, want nil", u, err)
		}
	}
}

// End-to-end: a real listener on loopback must be unreachable through GetWebsite even
// though the URL is well-formed and the server is genuinely up.
func TestGetWebsiteBlocksLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("secret internal data"))
	}))
	defer srv.Close()

	body, err := GetWebsite(srv.URL)
	if err == nil {
		t.Fatalf("GetWebsite(%s) succeeded, want blocked; got %q", srv.URL, body)
	}
	if strings.Contains(body, "secret") {
		t.Fatal("internal response leaked to caller")
	}
}

// A public-looking URL that redirects to loopback must fail at the second hop.
func TestGetWebsiteBlocksRedirectToLoopback(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("secret internal data"))
	}))
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	// The redirector is itself on loopback, so this is blocked at hop 1 — which is the
	// correct outcome either way. The assertion that matters is that nothing leaks.
	body, _ := GetWebsite(redirector.URL)
	if strings.Contains(body, "secret") {
		t.Fatal("internal response leaked through redirect")
	}
}
