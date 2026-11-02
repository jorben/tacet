package netgate

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// In a test binary every client dials this computer only, whatever its
// allowlist says: a test that wants a server serves it on loopback (httptest).
// .invalid never resolves, so without the guard this fails on the lookup.
func TestTestsStayOffTheNetwork(t *testing.T) {
	_, lease := netLease(t)
	c, err := lease.HTTPClient(Options{Hosts: []string{"tacet-offline.invalid"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Get("https://tacet-offline.invalid/model.onnx"); !errors.Is(err, errTestOffline) {
		t.Fatalf("a test reached for another host: %v", err)
	}
	// loopback is still served
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c, _ = lease.HTTPClient(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatalf("loopback refused: %v", err)
	}
	resp.Body.Close()
}

func TestLoopback(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1": true, "127.8.0.1": true, "::1": true, "[::1]": true, "localhost": true, "LOCALHOST.": true, "a.localhost": true,
		"huggingface.co": false, "10.0.0.1": false, "192.168.1.2": false, "localhost.example.com": false, "0.0.0.0": false, "": false,
	} {
		if got := loopback(host); got != want {
			t.Errorf("loopback(%q) = %v, want %v", host, got, want)
		}
	}
}
