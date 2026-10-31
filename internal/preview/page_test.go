package preview

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jorben/tacet/internal/host"
	"github.com/jorben/tacet/internal/kernel"
	"github.com/jorben/tacet/internal/refs"
)

var fakeAttempt = kernel.AttemptRef{JobID: "j1", AttemptID: "a1", OwnerGeneration: 5, JobVersion: 1}

func wire(mode refs.Mode, h int) refs.Wire {
	e := &refs.Encryption{Version: 1, ResourceID: "x", KeyHex: "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"}
	w := refs.Wire{Ref: "r_" + string(mode), Mode: mode, Handle: h, Encryption: e}
	if mode == refs.Read {
		w.Version = 1
	}
	return w
}

// play answers one format.preview call as the plugin: the items, then the end.
func play(t *testing.T, item map[string]any, status string, marks []Mark, bytesWritten int64, page int) (Page, error) {
	t.Helper()
	local, remote := net.Pipe()
	s, err := host.NewSession(local)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); remote.Close() })
	read := func() map[string]any {
		remote.SetReadDeadline(time.Now().Add(5 * time.Second))
		b, err := host.ReadFrame(remote)
		if err != nil {
			t.Fatalf("peer read: %v", err)
		}
		var m map[string]any
		json.Unmarshal(b, &m)
		return m
	}
	send := func(m map[string]any) {
		b, _ := json.Marshal(m)
		remote.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_ = host.WriteFrame(remote, b)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	init := make(chan error, 1)
	go func() { init <- s.Initialize(ctx, "fixture.test", "0.1.0", 5) }()
	req := read()
	send(map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"protocol": "1.0", "plugin": map[string]any{"id": "fixture.test", "version": "0.1.0"}, "roles": []string{"format"}}})
	if err = <-init; err != nil {
		t.Fatal(err)
	}
	p, err := NewPager()
	if err != nil {
		t.Fatal(err)
	}
	type out struct {
		p   Page
		err error
	}
	done := make(chan out, 1)
	go func() {
		pg, err := p.Render(ctx, s, fakeAttempt, wire(refs.Read, 3), wire(refs.Write, 4), page, 100, marks, func() (int64, error) { return bytesWritten, nil })
		done <- out{pg, err}
	}()
	call := read()
	if call["method"] != "format.preview" {
		t.Fatalf("%v", call["method"])
	}
	n := 0
	if item != nil {
		n = 1
		send(map[string]any{"jsonrpc": "2.0", "method": "stream.item", "params": map[string]any{"stream": "a1", "seq": 1, "item": item}})
		remote.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		host.ReadFrame(remote)
	}
	send(map[string]any{"jsonrpc": "2.0", "id": call["id"], "result": map[string]any{"type": "stream.end", "stream": "a1", "last_seq": n, "status": status}})
	select {
	case o := <-done:
		return o.p, o.err
	case <-time.After(10 * time.Second):
		t.Fatal("Render did not return")
	}
	return Page{}, nil
}

func item(over map[string]any) map[string]any {
	m := map[string]any{"kind": "page_preview", "page": 1, "pages": 3, "width_px": 600, "height_px": 800, "image": "png", "bytes": 1234,
		"rects": []any{map[string]any{"id": "m1", "rect": []any{10, 20, 110, 40}}}, "unmapped": []any{"m2"}}
	for k, v := range over {
		m[k] = v
	}
	return m
}

var twoMarks = []Mark{{ID: "m1", Ordinal: 1, Start: 0, End: 3}, {ID: "m2", Ordinal: 1, Start: 4, End: 9}}

func TestAPreviewPlacesOrDeclaresEveryMark(t *testing.T) {
	pg, err := play(t, item(nil), "completed", twoMarks, 1234, 1)
	if err != nil {
		t.Fatal(err)
	}
	if pg.Pages != 3 || pg.WidthPx != 600 || len(pg.Boxes) != 1 || pg.Boxes[0] != (Box{"m1", [4]int{10, 20, 110, 40}}) || len(pg.Unmapped) != 1 || pg.Unmapped[0] != "m2" {
		t.Fatalf("%+v", pg)
	}
}

func TestEveryDeviationFromThePreviewContractIsRefused(t *testing.T) {
	rect := func(id string, r ...any) []any { return []any{map[string]any{"id": id, "rect": r}} }
	for name, c := range map[string]struct {
		item   map[string]any
		status string
		bytes  int64
		page   int
		want   error
	}{
		"no description":                {nil, "completed", 1234, 1, ErrPageProtocol},
		"plugin failed":                 {item(nil), "failed", 1234, 1, ErrPageFailed},
		"other page than asked":         {item(map[string]any{"page": 2}), "completed", 1234, 1, ErrPageProtocol},
		"size differs from image":       {item(nil), "completed", 99, 1, ErrPageProtocol},
		"rect of a mark not sent":       {item(map[string]any{"rects": rect("zz", 1, 1, 5, 5)}), "completed", 1234, 1, ErrPageProtocol},
		"extra rect of a mark not sent": {item(map[string]any{"rects": []any{map[string]any{"id": "m1", "rect": []any{10, 20, 110, 40}}, map[string]any{"id": "zz", "rect": []any{1, 1, 5, 5}}}}), "completed", 1234, 1, ErrPageProtocol},
		"rect outside the image":        {item(map[string]any{"rects": rect("m1", 10, 20, 700, 40)}), "completed", 1234, 1, ErrPageProtocol},
		"empty rect":                    {item(map[string]any{"rects": rect("m1", 10, 20, 10, 40)}), "completed", 1234, 1, ErrPageProtocol},
		"placed and unmapped":           {item(map[string]any{"unmapped": []any{"m1", "m2"}}), "completed", 1234, 1, ErrPageProtocol},
		"a mark left unanswered":        {item(map[string]any{"unmapped": []any{}}), "completed", 1234, 1, ErrPageProtocol},
		"unmapped unknown mark":         {item(map[string]any{"unmapped": []any{"m2", "zz"}}), "completed", 1234, 1, ErrPageProtocol},
		"page beyond the count":         {item(map[string]any{"page": 4}), "completed", 1234, 4, ErrPageProtocol},
		"not the schema's shape":        {item(map[string]any{"image": "jpeg"}), "completed", 1234, 1, ErrPageProtocol},
	} {
		if _, err := play(t, c.item, c.status, twoMarks, c.bytes, c.page); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestRenderRefusesBadRequestsBeforeAnythingIsSent(t *testing.T) {
	p, _ := NewPager()
	ok := func() (int64, error) { return 0, nil }
	if _, err := p.Render(context.Background(), nil, fakeAttempt, wire(refs.Read, 3), wire(refs.Write, 4), 1, 100, nil, ok); !errors.Is(err, ErrPageProtocol) {
		t.Fatalf("no session: %v", err)
	}
	local, _ := net.Pipe()
	s, _ := host.NewSession(local)
	defer s.Close()
	for name, c := range map[string]struct {
		page  int
		marks []Mark
	}{
		"page zero": {0, nil},
		"duplicate": {1, []Mark{{ID: "a", Ordinal: 1, Start: 0, End: 1}, {ID: "a", Ordinal: 1, Start: 2, End: 3}}},
		"empty id":  {1, []Mark{{ID: "", Ordinal: 1, Start: 0, End: 1}}},
		"too many":  {1, make([]Mark, 4097)},
	} {
		if _, err := p.Render(context.Background(), s, fakeAttempt, wire(refs.Read, 3), wire(refs.Write, 4), c.page, 100, c.marks, ok); !errors.Is(err, ErrPageProtocol) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
