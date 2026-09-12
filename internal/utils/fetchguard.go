package utils

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

// SSRF guard for every outbound fetch of a user-supplied URL.
//
// Both callers of GetWebsite take their URL from something a user typed: /generate's
// url form field, and the agent's fetch_url tool — which is reachable anonymously, so
// no account is needed to aim it. Without this, "read http://169.254.169.254/..." makes
// this backend fetch its own cloud metadata and hand the result back in the thread.
//
// The check that matters is on the resolved IP at connect time, not on the hostname:
// attacker.example can publish an A record pointing at 127.0.0.1, and a validate-then-
// fetch guard resolves twice and can be raced (DNS rebinding). safeDialContext resolves
// once, rejects any disallowed address, and then dials the exact IP it vetted, so there
// is no second lookup to poison. Redirects re-enter the same dialer, so hop 3 is checked
// as strictly as hop 1.

const (
	fetchTimeout = 15 * time.Second

	// maxFetchRedirects bounds a redirect chain. Every hop is re-checked by the dialer
	// regardless; this only stops a loop from eating the whole request deadline.
	maxFetchRedirects = 5

	// MaxFetchBytes caps one fetched document. Generous for HTML — the point is that a
	// URL pointing at an endless stream cannot exhaust memory.
	MaxFetchBytes = 2 << 20 // 2 MiB
)

// ErrBlockedURL is returned for a URL this service refuses to fetch. It is deliberately
// vague: the caller is told the fetch failed, never which internal address answered.
var ErrBlockedURL = errors.New("refusing to fetch that URL")

// blockedRanges are networks with no legitimate recipe page on them, beyond what
// netip's own predicates already cover (loopback, private, link-local, multicast,
// unspecified).
var blockedRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),   // RFC 6598 carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),    // RFC 6890 IETF protocol assignments
	netip.MustParsePrefix("192.0.2.0/24"),    // TEST-NET-1
	netip.MustParsePrefix("198.18.0.0/15"),   // RFC 2544 benchmarking
	netip.MustParsePrefix("198.51.100.0/24"), // TEST-NET-2
	netip.MustParsePrefix("203.0.113.0/24"),  // TEST-NET-3
	netip.MustParsePrefix("240.0.0.0/4"),     // reserved
	netip.MustParsePrefix("::ffff:0:0/96"),   // IPv4-mapped IPv6, checked unmapped below
	netip.MustParsePrefix("64:ff9b::/96"),    // NAT64
	netip.MustParsePrefix("2002::/16"),       // 6to4
}

// addrAllowed reports whether an address is a plausible public host.
func addrAllowed(addr netip.Addr) bool {
	// An IPv4-mapped IPv6 address (::ffff:127.0.0.1) must be judged as the IPv4 address
	// it carries — otherwise every predicate below silently passes it.
	ip := addr.Unmap()

	if !ip.IsValid() ||
		ip.IsLoopback() ||
		ip.IsPrivate() || // RFC1918 for v4, fc00::/7 for v6
		ip.IsLinkLocalUnicast() || // includes 169.254.169.254, the metadata service
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	for _, r := range blockedRanges {
		if r.Contains(ip) {
			return false
		}
	}
	return true
}

// checkFetchURL validates the parts of a URL that do not need DNS. The address check
// happens in the dialer; this rejects what should never reach a dialer at all.
func checkFetchURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ErrBlockedURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ErrBlockedURL
	}
	if u.Hostname() == "" {
		return ErrBlockedURL
	}
	// Credentials in a fetched URL are never something a recipe page needs, and they
	// are a common way to smuggle a second host past a naive parser.
	if u.User != nil {
		return ErrBlockedURL
	}
	// A bare IP literal is checked here too, so an obviously-internal URL fails before
	// any connection is attempted.
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !addrAllowed(ip) {
		return ErrBlockedURL
	}
	return nil
}

// safeDialContext resolves the host, refuses the connection if any address it resolves
// to is non-public, and dials a vetted address directly.
func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, ErrBlockedURL
	}

	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, ErrBlockedURL
	}

	// Reject if *any* resolved address is disallowed, rather than picking a public one
	// out of a mixed set: a host that answers with both is not a host we want to talk
	// to, and choosing the public one would make the block depend on record ordering.
	for _, ip := range ips {
		if !addrAllowed(ip) {
			return nil, ErrBlockedURL
		}
	}

	d := net.Dialer{Timeout: fetchTimeout}
	var lastErr error
	for _, ip := range ips {
		// Dial the exact address that was just vetted. TLS still verifies against the
		// original hostname, which http.Transport takes from the URL, not from here.
		conn, err := d.DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// fetchClient is the only client used for user-supplied URLs. Package-level so the
// connection pool is reused; every field on it is part of the guard.
var fetchClient = &http.Client{
	Timeout: fetchTimeout,
	Transport: &http.Transport{
		DialContext:           safeDialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: fetchTimeout,
		DisableKeepAlives:     false,
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxFetchRedirects {
			return fmt.Errorf("%w: too many redirects", ErrBlockedURL)
		}
		// The dialer re-checks the address of every hop; this catches a redirect to a
		// scheme that would bypass it entirely.
		return checkFetchURL(req.URL.String())
	},
}
