package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"
)

func encodeResource(t *testing.T, data []byte) ([]byte, []byte) {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	w, err := NewResourceWriter(&output, key, "resource-1", uint64(max(1, len(data))))
	if err != nil {
		t.Fatal(err)
	}
	// Irregular chunks deliberately cross record and final boundaries.
	for offset := 0; offset < len(data); {
		end := min(len(data), offset+1373)
		if _, err := w.Write(data[offset:end]); err != nil {
			t.Fatal(err)
		}
		offset = end
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes(), key
}

func TestResourceRandomAccessAndBoundaryLengths(t *testing.T) {
	for _, size := range []int{0, 1, ResourceSegmentBytes - 1, ResourceSegmentBytes, ResourceSegmentBytes + 1, 3 * ResourceSegmentBytes} {
		t.Run(stringSize(size), func(t *testing.T) {
			data := make([]byte, size)
			if _, err := rand.Read(data); err != nil {
				t.Fatal(err)
			}
			encoded, key := encodeResource(t, data)
			r, err := OpenResource(bytes.NewReader(encoded), int64(len(encoded)), key, "resource-1")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if r.Size() != int64(size) {
				t.Fatalf("size=%d want=%d", r.Size(), size)
			}
			if err := r.Verify(); err != nil {
				t.Fatal(err)
			}
			for _, offset := range []int{0, min(7, size), min(ResourceSegmentBytes-1, size), size} {
				buffer := make([]byte, 37)
				n, err := r.ReadAt(buffer, int64(offset))
				want := min(len(buffer), size-offset)
				if n != want || !bytes.Equal(buffer[:n], data[offset:offset+want]) {
					t.Fatal("random read differs")
				}
				if want < len(buffer) && !errors.Is(err, io.EOF) {
					t.Fatalf("want EOF, got %v", err)
				}
			}
		})
	}
}

func stringSize(n int) string {
	// Avoid test case labels carrying content or resource identifiers.
	if n == 0 {
		return "empty"
	}
	if n < ResourceSegmentBytes {
		return "short"
	}
	if n == ResourceSegmentBytes {
		return "one"
	}
	if n == ResourceSegmentBytes+1 {
		return "boundary"
	}
	return "multiple"
}

func TestResourceTamperTruncateReorderAndIdentityFail(t *testing.T) {
	data := bytes.Repeat([]byte("synthetic-private-marker"), 10_000)
	encoded, key := encodeResource(t, data)
	if bytes.Contains(encoded, []byte("synthetic-private-marker")) {
		t.Fatal("plaintext persisted")
	}
	cases := map[string]func([]byte) []byte{
		"header":           func(b []byte) []byte { b[10] ^= 1; return b },
		"first_segment":    func(b []byte) []byte { b[resourceHeaderBytes+5] ^= 1; return b },
		"final_segment":    func(b []byte) []byte { b[len(b)-1] ^= 1; return b },
		"truncated_byte":   func(b []byte) []byte { return b[:len(b)-1] },
		"truncated_record": func(b []byte) []byte { return b[:resourceHeaderBytes+resourceRecordBytes] },
		"append":           func(b []byte) []byte { return append(b, 0) },
		"swap": func(b []byte) []byte {
			a := append([]byte(nil), b[resourceHeaderBytes:resourceHeaderBytes+resourceRecordBytes]...)
			copy(b[resourceHeaderBytes:], b[resourceHeaderBytes+resourceRecordBytes:resourceHeaderBytes+2*resourceRecordBytes])
			copy(b[resourceHeaderBytes+resourceRecordBytes:], a)
			return b
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			corrupt := change(append([]byte(nil), encoded...))
			r, err := OpenResource(bytes.NewReader(corrupt), int64(len(corrupt)), key, "resource-1")
			if err == nil {
				err = r.Verify()
				r.Close()
			}
			if err == nil {
				t.Fatal("corrupt resource accepted")
			}
		})
	}
	for _, input := range []struct {
		key []byte
		id  string
	}{{bytes.Repeat([]byte{3}, 32), "resource-1"}, {key, "another-resource"}} {
		if _, err := OpenResource(bytes.NewReader(encoded), int64(len(encoded)), input.key, input.id); err == nil {
			t.Fatal("wrong key or resource identity accepted")
		}
	}
}

func TestResourceNewWriterHasFreshCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{1}, 32)
	var outputs [2]bytes.Buffer
	for i := range outputs {
		w, err := NewResourceWriter(&outputs[i], key, "same-logical-resource", 64)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write([]byte("same content")); err != nil {
			t.Fatal(err)
		}
		if err = w.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if bytes.Equal(outputs[0].Bytes(), outputs[1].Bytes()) {
		t.Fatal("rewrite reused resource encryption")
	}
}

func TestResourceQuotaAndClosedHandles(t *testing.T) {
	var output bytes.Buffer
	w, err := NewResourceWriter(&output, bytes.Repeat([]byte{1}, 32), "res", 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = w.Write([]byte("too large")); err == nil {
		t.Fatal("quota ignored")
	}
	if err = w.Close(); err == nil {
		t.Fatal("failed writer sealed")
	}
	encoded, key := encodeResource(t, []byte("private"))
	r, err := OpenResource(bytes.NewReader(encoded), int64(len(encoded)), key, "resource-1")
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err = r.ReadAt(make([]byte, 1), 0); !errors.Is(err, ErrResourceClosed) {
		t.Fatalf("closed reader: %v", err)
	}
}

func TestResourceSharedPythonVectors(t *testing.T) {
	data, err := os.ReadFile("../../protocol/testdata/resource-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name    string `json:"name"`
		Key     string `json:"key_hex"`
		ID      string `json:"resource_id"`
		Plain   string `json:"plaintext_hex"`
		Encoded string `json:"encoded_hex"`
	}
	if err := json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, vector := range vectors {
		t.Run(vector.Name, func(t *testing.T) {
			decode := func(s string) []byte {
				b, e := hex.DecodeString(s)
				if e != nil {
					t.Fatal(e)
				}
				return b
			}
			encoded := decode(vector.Encoded)
			r, e := OpenResource(bytes.NewReader(encoded), int64(len(encoded)), decode(vector.Key), vector.ID)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			if e = r.Verify(); e != nil {
				t.Fatal(e)
			}
			plain := make([]byte, r.Size())
			if _, e = r.ReadAt(plain, 0); e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(plain, decode(vector.Plain)) {
				t.Fatal("shared vector mismatch")
			}
		})
	}
}

func TestResourceFutureVersionRejected(t *testing.T) {
	encoded, key := encodeResource(t, nil)
	encoded[9] = 2
	if _, err := OpenResource(bytes.NewReader(encoded), int64(len(encoded)), key, "resource-1"); !errors.Is(err, ErrResourceVersion) {
		t.Fatalf("future version: %v", err)
	}
}
