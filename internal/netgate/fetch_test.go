package netgate

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var body = strings.Repeat("0123456789", 100)

// serve is a file server that honours Range/If-Range and has a switchable entity.
func serve(t *testing.T, etag *string, mode *string) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if *etag != "" {
			w.Header().Set("ETag", *etag)
		}
		switch *mode {
		case "404":
			http.NotFound(w, r)
			return
		case "badrange":
			w.Header().Set("Content-Range", "bytes 5-999/1000")
			w.WriteHeader(206)
			return
		case "weak":
			w.Header().Set("ETag", `W/"x"`)
		}
		http.ServeContent(w, r, "f", time.Time{}, strings.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetcher(t *testing.T, srv *httptest.Server) *Fetcher {
	_, l := netLease(t)
	f, err := l.Fetcher(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func read(t *testing.T, r *Reply) string {
	t.Helper()
	b, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFullAndResumedDownloadsReportWhereTheBodyStarts(t *testing.T) {
	etag, mode := `"v1"`, ""
	srv := serve(t, &etag, &mode)
	f := fetcher(t, srv)
	r, err := f.Do(t.Context(), Get{URL: srv.URL})
	if err != nil || r.Status != 200 || r.Offset != 0 || r.Total != 1000 || r.Entity != `"v1"` || read(t, r) != body {
		t.Fatalf("%+v %v", r, err)
	}
	r, err = f.Do(t.Context(), Get{URL: srv.URL, Offset: 400, Entity: `"v1"`})
	if err != nil || r.Status != 206 || r.Offset != 400 || r.Total != 1000 || read(t, r) != body[400:] {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestAChangedEntityRestartsFromZeroInsteadOfStitchingVersions(t *testing.T) {
	etag, mode := `"v2"`, ""
	srv := serve(t, &etag, &mode)
	r, err := fetcher(t, srv).Do(t.Context(), Get{URL: srv.URL, Offset: 400, Entity: `"v1"`})
	if err != nil || r.Status != 200 || r.Offset != 0 || r.Entity != `"v2"` || read(t, r) != body {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestResumeNeedsAnEntityAndBadAnswersAreRefused(t *testing.T) {
	etag, mode := `"v1"`, ""
	srv := serve(t, &etag, &mode)
	f := fetcher(t, srv)
	for name, g := range map[string]Get{"no entity": {URL: srv.URL, Offset: 5}, "negative": {URL: srv.URL, Offset: -1, Entity: "x"}} {
		if _, err := f.Do(t.Context(), g); !errors.Is(err, ErrNoEntity) {
			t.Errorf("%s: %v", name, err)
		}
	}
	mode = "badrange"
	if _, err := f.Do(t.Context(), Get{URL: srv.URL, Offset: 400, Entity: `"v1"`}); !errors.Is(err, ErrRangeInvalid) {
		t.Errorf("a range that does not start where asked: %v", err)
	}
	mode = "404"
	if _, err := f.Do(t.Context(), Get{URL: srv.URL}); !errors.Is(err, ErrHTTPStatus) {
		t.Errorf("%v", err)
	}
	if _, err := f.Do(t.Context(), Get{URL: "https://example.com/x"}); !errors.Is(err, ErrNetworkTarget) {
		t.Errorf("a host outside the allowlist: %v", err)
	}
}

func TestAWeakEntityIsNotUsableForResume(t *testing.T) {
	etag, mode := "", "weak"
	srv := serve(t, &etag, &mode)
	r, err := fetcher(t, srv).Do(t.Context(), Get{URL: srv.URL})
	if err != nil || r.Entity != "" {
		t.Fatalf("%+v %v", r, err)
	}
	read(t, r)
}

func TestContentRangeParsing(t *testing.T) {
	for in, want := range map[string][3]int64{"bytes 5-9/10": {5, 10, 1}, "bytes 0-0/*": {0, 0, 1}, "bytes 7-9/*": {7, 0, 1},
		"": {0, 0, 0}, "bytes 5-9": {0, 0, 0}, "bytes 9-5/10": {0, 0, 0}, "bytes 5-9/9": {0, 0, 0}, "bytes a-9/10": {0, 0, 0}, "items 5-9/10": {0, 0, 0}, "bytes -1-9/10": {0, 0, 0}} {
		s, tot, ok := parseContentRange(in)
		got := [3]int64{s, tot, 0}
		if ok {
			got[2] = 1
		}
		if got != want {
			t.Errorf("%q: %v want %v", in, got, want)
		}
	}
}

func TestAFetcherStopsWithItsLease(t *testing.T) {
	etag, mode := `"v1"`, ""
	srv := serve(t, &etag, &mode)
	_, l := netLease(t)
	f, _ := l.Fetcher(Options{Hosts: []string{"127.0.0.1"}, TLS: tlsOf(srv)})
	l.Release()
	if _, err := f.Do(t.Context(), Get{URL: srv.URL}); err == nil {
		t.Fatal("a fetch started after the lease was released")
	}
	if _, err := (&Lease{}).Fetcher(Options{}); err == nil {
		t.Fatal("an unbound lease made a fetcher")
	}
}

func TestTheRequestCarriesNothingButTheRangeAndAnIdentityEncoding(t *testing.T) {
	var got http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone(); w.Write([]byte("x")) }))
	defer srv.Close()
	r, err := fetcher(t, srv).Do(t.Context(), Get{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	read(t, r)
	if got.Get("Accept-Encoding") != "identity" || got.Get("Cookie") != "" || got.Get("Authorization") != "" || got.Get("Range") != "" || got.Get("User-Agent") != "Tacet" {
		t.Fatalf("%v", got)
	}
}
