package netgate

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	ErrNetworkTarget = errors.New("NETWORK_TARGET_NOT_ALLOWED")
	ErrTooManyHops   = errors.New("NETWORK_TOO_MANY_REDIRECTS")
)

// Options restricts what a leased client may reach.
type Options struct {
	// Hosts is the host-name allowlist (no ports, case-insensitive). An entry is an
	// exact name, or ".domain.tld" for any subdomain of it (not the domain itself;
	// at least two labels, so ".com" is refused) — content delivery hosts vary by
	// region. The first request and every redirect hop must match; an empty list
	// refuses everything, so a caller can never get an open client by omission.
	Hosts []string
	// TLS is for tests that trust a local certificate; production leaves it nil.
	TLS *tls.Config
}

// HTTPClient returns the only way the application talks to the network. It is
// bound to a Network lease:
//
//   - each request and each dial is admitted under the lease, so nothing starts
//     after Release;
//   - Release cancels in-flight requests and reads of response bodies and closes
//     idle connections, because a lease must not end while a transfer continues;
//   - only https, no credentials in the URL, and only allowlisted hosts, on every
//     redirect hop as well.
//
// The client holds no state beyond the lease; create one per activity.
func (l *Lease) HTTPClient(o Options) (*http.Client, error) {
	if l == nil || l.gate == nil {
		return nil, ErrLeaseReleased
	}
	if l.kind != Network {
		return nil, ErrLeaseKind
	}
	list, err := newAllowlist(o.Hosts)
	if err != nil {
		return nil, err
	}
	check := func(u *url.URL) error {
		if u == nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" || !list.permits(u.Hostname()) {
			return ErrNetworkTarget
		}
		return nil
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	base := &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     o.TLS.Clone(), // HTTP/2 setup mutates it; never touch the caller's config
		TLSHandshakeTimeout: 15 * time.Second,
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if err := l.WhileHeld(func() error { return nil }); err != nil {
				return nil, err
			}
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			if err := refuseInTests(host); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	context.AfterFunc(l.ctx, base.CloseIdleConnections)
	return &http.Client{
		Transport: &guard{lease: l, base: base, check: check},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return ErrTooManyHops
			}
			return check(req.URL)
		},
	}, nil
}

type guard struct {
	lease *Lease
	base  *http.Transport
	check func(*url.URL) error
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := g.check(req.URL); err != nil {
		return nil, err
	}
	if err := refuseInTests(req.URL.Hostname()); err != nil {
		return nil, err
	}
	if err := g.lease.WhileHeld(func() error { return nil }); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(req.Context())
	unbind := context.AfterFunc(g.lease.ctx, cancel)
	stop := func() { unbind(); cancel() }
	resp, err := g.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		stop()
		if g.lease.ctx.Err() != nil {
			return nil, ErrLeaseReleased
		}
		return nil, err
	}
	resp.Body = &leasedBody{ReadCloser: resp.Body, lease: g.lease, stop: stop}
	return resp, nil
}

type leasedBody struct {
	io.ReadCloser
	lease *Lease
	stop  func()
}

func (b *leasedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && b.lease.ctx.Err() != nil {
		return n, ErrLeaseReleased
	}
	return n, err
}

func (b *leasedBody) Close() error {
	err := b.ReadCloser.Close()
	b.stop()
	return err
}

// allowlist holds exact host names and ".domain" entries that admit any subdomain.
type allowlist struct {
	exact    map[string]bool
	suffixes []string
}

func newAllowlist(hosts []string) (allowlist, error) {
	l := allowlist{exact: make(map[string]bool, len(hosts))}
	for _, h := range hosts {
		if h == "" || strings.ContainsAny(h, "/:@ *") {
			return allowlist{}, ErrNetworkTarget
		}
		h = strings.ToLower(h)
		if !strings.HasPrefix(h, ".") {
			l.exact[h] = true
			continue
		}
		if !strings.Contains(h[1:], ".") || strings.Contains(h, "..") || strings.HasSuffix(h, ".") {
			return allowlist{}, ErrNetworkTarget
		}
		l.suffixes = append(l.suffixes, h)
	}
	return l, nil
}

func (l allowlist) permits(host string) bool {
	host = strings.ToLower(host)
	if l.exact[host] {
		return true
	}
	if strings.HasSuffix(host, ".") || strings.Contains(host, "..") {
		return false
	}
	for _, s := range l.suffixes {
		if len(host) > len(s) && strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}
