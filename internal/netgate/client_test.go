package netgate

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func tlsOf(srv *httptest.Server) *tls.Config {
	return srv.Client().Transport.(*http.Transport).TLSClientConfig
}

func netLease(t *testing.T) (*Gate, *Lease) {
	t.Helper()
	g := NewGate()
	l, err := g.Acquire(Network)
	if err != nil {
		t.Fatal(err)
	}
	return g, l
}

func TestLeasedClientWorksWhileHeldAndNothingStartsAfterRelease(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	_, lease := netLease(t)
	c, err := lease.HTTPClient(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "ok" || hits.Load() != 1 {
		t.Fatalf("body=%q hits=%d", body, hits.Load())
	}
	if err = lease.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Get(srv.URL); !errors.Is(err, ErrLeaseReleased) {
		t.Fatalf("request after release: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatal("the server was contacted after the lease ended")
	}
}

func TestReleaseStopsAResponseBodyThatIsStillStreaming(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("first"))
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer srv.Close()
	_, lease := netLease(t)
	c, _ := lease.HTTPClient(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	<-started
	first := make([]byte, 5)
	if _, err = io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(resp.Body); done <- err }()
	time.Sleep(100 * time.Millisecond)
	lease.Release()
	select {
	case err = <-done:
		if !errors.Is(err, ErrLeaseReleased) {
			t.Fatalf("read after release: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a transfer kept running after its lease ended")
	}
}

func TestReleaseCancelsARequestWaitingForTheServer(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(30 * time.Second):
		}
	}))
	defer srv.Close()
	_, lease := netLease(t)
	c, _ := lease.HTTPClient(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	done := make(chan error, 1)
	go func() { _, err := c.Get(srv.URL); done <- err }()
	time.Sleep(150 * time.Millisecond)
	lease.Release()
	select {
	case err := <-done:
		if !errors.Is(err, ErrLeaseReleased) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request outlived its lease")
	}
}

func TestOnlyAllowlistedHttpsTargetsWithoutCredentialsAreReachable(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer plain.Close()
	_, lease := netLease(t)
	defer lease.Release()
	c, _ := lease.HTTPClient(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	port := srv.URL[len("https://127.0.0.1"):]
	for name, target := range map[string]string{
		"plain http":  plain.URL,
		"other host":  "https://localhost" + port,
		"credentials": "https://user:pw@127.0.0.1" + port,
		"no scheme":   "ftp://127.0.0.1" + port,
	} {
		if _, err := c.Get(target); !errors.Is(err, ErrNetworkTarget) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if hits.Load() != 0 {
		t.Fatal("a refused target was contacted")
	}
	// an empty allowlist refuses everything
	closed, _ := lease.HTTPClient(Options{TLS: tlsOf(srv)})
	if _, err := closed.Get(srv.URL); !errors.Is(err, ErrNetworkTarget) {
		t.Fatalf("empty allowlist: %v", err)
	}
	for _, bad := range []string{"", "a/b", "host:443", "user@host", "a b"} {
		if _, err := lease.HTTPClient(Options{Hosts: []string{bad}}); !errors.Is(err, ErrNetworkTarget) {
			t.Fatalf("host entry %q accepted", bad)
		}
	}
}

func TestRedirectsAreCheckedAtEveryHopAndBounded(t *testing.T) {
	var elsewhere atomic.Int64
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { elsewhere.Add(1) }))
	defer other.Close()
	var self string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, "https://localhost"+other.URL[len("https://127.0.0.1"):]+"/x", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, self+"/loop", http.StatusFound)
		case "/plain":
			http.Redirect(w, r, "http://127.0.0.1/", http.StatusFound)
		}
	}))
	defer srv.Close()
	self = srv.URL
	_, lease := netLease(t)
	defer lease.Release()
	c, _ := lease.HTTPClient(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	if _, err := c.Get(srv.URL + "/away"); !errors.Is(err, ErrNetworkTarget) {
		t.Fatalf("redirect to another host: %v", err)
	}
	if _, err := c.Get(srv.URL + "/plain"); !errors.Is(err, ErrNetworkTarget) {
		t.Fatalf("redirect to http: %v", err)
	}
	if _, err := c.Get(srv.URL + "/loop"); !errors.Is(err, ErrTooManyHops) {
		t.Fatalf("redirect loop: %v", err)
	}
	if elsewhere.Load() != 0 {
		t.Fatal("a disallowed redirect target was contacted")
	}
}

func TestOnlyANetworkLeaseCanMakeAClientAndProcessingBlocksItsCreation(t *testing.T) {
	g := NewGate()
	for _, kind := range []LeaseKind{Processing, Maintenance} {
		l, err := g.Acquire(kind)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = l.HTTPClient(Options{Hosts: []string{"example.invalid"}}); !errors.Is(err, ErrLeaseKind) {
			t.Fatalf("%s lease produced a client: %v", kind, err)
		}
		l.Release()
	}
	p, _ := g.Acquire(Processing)
	defer p.Release()
	if _, err := g.Acquire(Network); err == nil {
		t.Fatal("a network lease was granted during processing, so no client can exist")
	}
	var none *Lease
	if _, err := none.HTTPClient(Options{}); !errors.Is(err, ErrLeaseReleased) {
		t.Fatalf("nil lease: %v", err)
	}
}

func TestProcessingStaysBlockedWhileANetworkTransferIsInFlight(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	g, lease := netLease(t)
	c, _ := lease.HTTPClient(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	go c.Get(srv.URL)
	<-started
	if _, err := g.Acquire(Processing); err == nil {
		t.Fatal("processing started during an active download")
	}
	lease.Release()
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := g.Acquire(Processing)
		if err == nil {
			p.Release()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("processing never became possible: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
