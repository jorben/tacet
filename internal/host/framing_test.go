package host

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTripUnicodeAndBoundaryEOF(t *testing.T) {
	body := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"text":"中文 😀"}}`)
	var output bytes.Buffer
	if err := WriteFrame(&output, body); err != nil {
		t.Fatal(err)
	}
	read, err := ReadFrame(&output)
	if err != nil || !bytes.Equal(read, body) {
		t.Fatalf("roundtrip: %v", err)
	}
	if _, err = ReadFrame(&output); !errors.Is(err, io.EOF) {
		t.Fatalf("boundary: %v", err)
	}
}

type headerOnly struct {
	header *bytes.Reader
	reads  int
}

func (r *headerOnly) Read(p []byte) (int, error) {
	if r.header.Len() == 0 {
		r.reads++
		return 0, errors.New("body must not be read")
	}
	return r.header.Read(p)
}

func TestFrameRejectsLengthBeforeBodyRead(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxMessageBytes+1)
	source := &headerOnly{header: bytes.NewReader(header[:])}
	if _, err := ReadFrame(source); !errors.Is(err, ErrProtocolLimit) {
		t.Fatalf("length: %v", err)
	}
	if source.reads != 0 {
		t.Fatal("oversized body read")
	}
}

func TestFrameRejectsTruncationAndNonUTF8(t *testing.T) {
	for _, data := range [][]byte{{0}, {0, 0, 0, 2, '{'}, {0, 0, 0, 1, 0xff}, {0, 0, 0, 0}, {0, 0, 0, 1, '!'}} {
		if _, err := ReadFrame(bytes.NewReader(data)); !errors.Is(err, ErrProtocolFrame) {
			t.Fatalf("invalid frame: %v", err)
		}
	}
}

func TestFrameMaximumBodyAndWriteRejection(t *testing.T) {
	body := append([]byte{'"'}, bytes.Repeat([]byte{'a'}, MaxMessageBytes-2)...)
	body = append(body, '"')
	var output bytes.Buffer
	if err := WriteFrame(&output, body); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFrame(&output); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(io.Discard, append(body, ' ')); !errors.Is(err, ErrProtocolLimit) {
		t.Fatal("oversized write accepted")
	}
	if err := WriteFrame(io.Discard, []byte{0xff}); !errors.Is(err, ErrProtocolFrame) {
		t.Fatal("non-UTF8 write accepted")
	}
}
