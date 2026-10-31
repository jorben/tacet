package host

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

// MaxMessageBytes is the protocol v1 frame body budget. Check the declared
// length before allocating or reading the body (architecture 4.2).
const MaxMessageBytes = 4 * 1024 * 1024

var ErrProtocolFrame = errors.New("PROTOCOL_FRAME_INVALID")
var ErrProtocolLimit = errors.New("PROTOCOL_FRAME_LIMIT")

// ReadFrame returns a single raw JSON value. io.EOF means a clean boundary;
// truncated frames, invalid UTF-8/JSON and excessive lengths are failures.
// Role/schema validation is a separate mandatory step before dispatch.
func ReadFrame(source io.Reader) (json.RawMessage, error) {
	var header [4]byte
	if n, err := io.ReadFull(source, header[:]); err != nil {
		if n == 0 && errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, ErrProtocolFrame
	}
	size := binary.BigEndian.Uint32(header[:])
	if size > MaxMessageBytes {
		return nil, ErrProtocolLimit
	}
	if size == 0 {
		return nil, ErrProtocolFrame
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(source, body); err != nil {
		clear(body)
		return nil, ErrProtocolFrame
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		clear(body)
		return nil, ErrProtocolFrame
	}
	return body, nil
}

// WriteFrame encodes only one JSON value; no stderr or arbitrary bytes can be
// interleaved on the protocol stream. The caller serializes concurrent writes.
// Secret-bearing buffers must be cleared by their owner after dispatch.
func WriteFrame(destination io.Writer, body json.RawMessage) error {
	if len(body) > MaxMessageBytes {
		return ErrProtocolLimit
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		return ErrProtocolFrame
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	for _, part := range [][]byte{header[:], body} {
		if _, err := io.Copy(destination, bytes.NewReader(part)); err != nil {
			return ErrProtocolFrame
		}
	}
	return nil
}
