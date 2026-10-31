package netgate

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
)

var (
	ErrHTTPStatus   = errors.New("NETWORK_HTTP_STATUS")
	ErrRangeInvalid = errors.New("NETWORK_RANGE_INVALID")
	ErrNoEntity     = errors.New("NETWORK_RESUME_WITHOUT_ENTITY")
)

// Fetcher is a leased client that can only download: a GET with an optional resume
// point. It exists so modules that must not import net/http (models, update) can
// transfer files, and so a request never carries more than a URL and a range.
type Fetcher struct{ client *http.Client }

// Fetcher returns a downloader bound to this lease and its host allowlist.
func (l *Lease) Fetcher(o Options) (*Fetcher, error) {
	c, err := l.HTTPClient(o)
	if err != nil {
		return nil, err
	}
	return &Fetcher{client: c}, nil
}

// Get describes one download request. Offset > 0 resumes: the request asks for the
// bytes from Offset on, and only if the server's entity is still Entity (If-Range),
// so a replaced file is never stitched together from two versions.
type Get struct {
	URL    string
	Offset int64
	Entity string
}

// Reply is the answer. Offset is where Body starts: Get.Offset after a 206, 0 after
// a 200 (the server ignored or refused the range and sends everything). Total is the
// size of the whole entity when the server said it, else 0. Entity is the strong ETag,
// empty if the server gave none or a weak one.
type Reply struct {
	Status int
	Entity string
	Offset int64
	Total  int64
	Body   io.ReadCloser
}

// Do performs the request. Only 200 and 206 succeed; the caller must close Body.
func (f *Fetcher) Do(ctx context.Context, g Get) (*Reply, error) {
	if g.Offset < 0 || (g.Offset > 0 && g.Entity == "") {
		return nil, ErrNoEntity
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.URL, nil)
	if err != nil {
		return nil, ErrNetworkTarget
	}
	req.Header.Set("User-Agent", "Tacet")
	req.Header.Set("Accept-Encoding", "identity") // byte ranges and sizes must be those of the file
	if g.Offset > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(g.Offset, 10)+"-")
		req.Header.Set("If-Range", g.Entity)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	entity := resp.Header.Get("ETag")
	if strings.HasPrefix(entity, "W/") {
		entity = ""
	}
	switch resp.StatusCode {
	case http.StatusOK:
		total := resp.ContentLength
		if total < 0 {
			total = 0
		}
		return &Reply{Status: 200, Entity: entity, Total: total, Body: resp.Body}, nil
	case http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if g.Offset == 0 || !ok || start != g.Offset {
			resp.Body.Close()
			return nil, ErrRangeInvalid
		}
		return &Reply{Status: 206, Entity: entity, Offset: start, Total: total, Body: resp.Body}, nil
	}
	resp.Body.Close()
	return nil, ErrHTTPStatus
}

// parseContentRange reads "bytes <start>-<end>/<total>"; total is 0 when "*".
func parseContentRange(h string) (start, total int64, ok bool) {
	rest, found := strings.CutPrefix(h, "bytes ")
	if !found {
		return 0, 0, false
	}
	span, size, found := strings.Cut(rest, "/")
	if !found {
		return 0, 0, false
	}
	a, b, found := strings.Cut(span, "-")
	if !found {
		return 0, 0, false
	}
	s, err1 := strconv.ParseInt(a, 10, 64)
	e, err2 := strconv.ParseInt(b, 10, 64)
	if err1 != nil || err2 != nil || s < 0 || e < s {
		return 0, 0, false
	}
	if size != "*" {
		t, err := strconv.ParseInt(size, 10, 64)
		if err != nil || t <= e {
			return 0, 0, false
		}
		total = t
	}
	return s, total, true
}
