package store

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ResourceVersion is the first versioned M0 resource encoding. A unique salt
// derives a fresh resource key for every new candidate, including rewrites.
const ResourceVersion = 1
const ResourceSegmentBytes = 64 * 1024
const resourceHeaderBytes = 8 + 2 + 32 + 32
const resourceRecordBytes = ResourceSegmentBytes + 16

var resourceMagic = [8]byte{'T', 'A', 'C', 'E', 'T', 'R', 'E', 'S'}
var ErrResourceInvalid = errors.New("RESOURCE_INVALID")
var ErrResourceVersion = errors.New("RESOURCE_VERSION_UNSUPPORTED")
var ErrResourceClosed = errors.New("RESOURCE_CLOSED")

func resourceCipher(key, salt []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, ErrResourceInvalid
	}
	derived, err := hkdf.Key(sha256.New, key, salt, "tacet/resource/v1", 32)
	if err != nil {
		return nil, ErrResourceInvalid
	}
	defer clear(derived)
	block, err := aes.NewCipher(derived)
	if err != nil {
		return nil, ErrResourceInvalid
	}
	return cipher.NewGCM(block)
}

func resourceNonce(sequence uint64, final bool) []byte {
	var nonce [12]byte
	if final {
		nonce[0] = 1
	}
	binary.BigEndian.PutUint64(nonce[4:], sequence)
	return nonce[:]
}

func resourceAAD(header []byte, sequence uint64, final bool) []byte {
	aad := make([]byte, len(header)+9)
	copy(aad, header)
	binary.BigEndian.PutUint64(aad[len(header):], sequence)
	if final {
		aad[len(aad)-1] = 1
	}
	return aad
}

func writeResourceBytes(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n < 0 || n > len(data) {
			return ErrResourceInvalid
		}
		data = data[n:]
		if err != nil || n == 0 {
			return ErrResourceInvalid
		}
	}
	return nil
}

// ResourceWriter encrypts before writing, uses one 64 KiB buffer and supports
// append-only output. Close authenticates the final segment even for an empty
// resource. A failed/incomplete writer is never a publishable candidate.
// Caller owns the stream, budget admission, fsync and immutable registration.
// Writer is single-threaded and must not be reused or resumed after failure.
type ResourceWriter struct {
	output   io.Writer
	aead     cipher.AEAD
	header   []byte
	buffer   []byte
	sequence uint64
	written  uint64
	limit    uint64
	closed   bool
	failed   bool
}

func NewResourceWriter(w io.Writer, key []byte, resourceID string, limit uint64) (*ResourceWriter, error) {
	if resourceID == "" || len(resourceID) > 1024 || limit == 0 {
		return nil, ErrResourceInvalid
	}
	header := make([]byte, resourceHeaderBytes)
	copy(header, resourceMagic[:])
	binary.BigEndian.PutUint16(header[8:10], ResourceVersion)
	if _, err := rand.Read(header[10:42]); err != nil {
		return nil, ErrResourceInvalid
	}
	identity := sha256.Sum256([]byte(resourceID))
	copy(header[42:], identity[:])
	aead, err := resourceCipher(key, header[10:42])
	if err != nil {
		return nil, err
	}
	if err := writeResourceBytes(w, header); err != nil {
		return nil, err
	}
	return &ResourceWriter{output: w, aead: aead, header: header,
		buffer: make([]byte, 0, ResourceSegmentBytes), limit: limit}, nil
}

func (w *ResourceWriter) segment(final bool) error {
	ciphertext := w.aead.Seal(nil, resourceNonce(w.sequence, final), w.buffer,
		resourceAAD(w.header, w.sequence, final))
	if err := writeResourceBytes(w.output, ciphertext); err != nil {
		w.failed = true
		clear(w.buffer)
		return err
	}
	clear(w.buffer)
	w.buffer = w.buffer[:0]
	w.sequence++
	return nil
}

func (w *ResourceWriter) Write(p []byte) (int, error) {
	if w.closed || w.failed {
		return 0, ErrResourceClosed
	}
	if uint64(len(p)) > w.limit-w.written {
		w.failed = true
		clear(w.buffer)
		return 0, fmt.Errorf("%w: quota", ErrResourceInvalid)
	}
	n := 0
	for len(p) > 0 {
		// Retain a full final segment until another byte arrives so that its
		// final bit is authenticated without seeking or rewriting ciphertext.
		if len(w.buffer) == ResourceSegmentBytes {
			if err := w.segment(false); err != nil {
				return n, err
			}
		}
		used := min(len(p), ResourceSegmentBytes-len(w.buffer))
		w.buffer = append(w.buffer, p[:used]...)
		p = p[used:]
		n += used
		w.written += uint64(used)
	}
	return n, nil
}

func (w *ResourceWriter) Close() error {
	if w.closed || w.failed {
		return ErrResourceClosed
	}
	w.closed = true
	defer func() { clear(w.buffer); w.buffer = nil; w.aead = nil }()
	return w.segment(true)
}

// ResourceReader performs bounded authenticated random reads. Opening verifies
// the header identity and final segment; each accessed segment is authenticated
// before releasing its plaintext. Full-resource verification is an explicit
// pass, required before immutable registration/publish, not implied by Open.
// The caller must prevent concurrent modification of the backing stream.
type ResourceReader struct {
	input     io.ReaderAt
	aead      cipher.AEAD
	header    []byte
	segments  uint64
	lastBytes int
	size      int64
}

func OpenResource(input io.ReaderAt, encodedSize int64, key []byte, resourceID string) (*ResourceReader, error) {
	if resourceID == "" || len(resourceID) > 1024 || encodedSize < resourceHeaderBytes+16 {
		return nil, ErrResourceInvalid
	}
	header := make([]byte, resourceHeaderBytes)
	if _, err := input.ReadAt(header, 0); err != nil {
		return nil, ErrResourceInvalid
	}
	if !bytes.Equal(header[:8], resourceMagic[:]) {
		return nil, ErrResourceInvalid
	}
	if binary.BigEndian.Uint16(header[8:10]) != ResourceVersion {
		return nil, ErrResourceVersion
	}
	identity := sha256.Sum256([]byte(resourceID))
	if !bytes.Equal(header[42:], identity[:]) {
		return nil, ErrResourceInvalid
	}
	aead, err := resourceCipher(key, header[10:42])
	if err != nil {
		return nil, err
	}
	body := encodedSize - resourceHeaderBytes
	segments := uint64((body-1)/resourceRecordBytes + 1)
	last := int(body - int64(segments-1)*resourceRecordBytes)
	if last < 16 || last > resourceRecordBytes {
		return nil, ErrResourceInvalid
	}
	r := &ResourceReader{input: input, aead: aead, header: header, segments: segments,
		lastBytes: last, size: int64(segments-1)*ResourceSegmentBytes + int64(last-16)}
	plain, err := r.segment(segments - 1)
	clear(plain)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (r *ResourceReader) segment(sequence uint64) ([]byte, error) {
	if r.aead == nil {
		return nil, ErrResourceClosed
	}
	if sequence >= r.segments {
		return nil, ErrResourceInvalid
	}
	final := sequence == r.segments-1
	size := resourceRecordBytes
	if final {
		size = r.lastBytes
	}
	ciphertext := make([]byte, size)
	if _, err := r.input.ReadAt(ciphertext, resourceHeaderBytes+int64(sequence)*resourceRecordBytes); err != nil {
		return nil, ErrResourceInvalid
	}
	plain, err := r.aead.Open(nil, resourceNonce(sequence, final), ciphertext,
		resourceAAD(r.header, sequence, final))
	if err != nil {
		return nil, ErrResourceInvalid
	}
	return plain, nil
}

func (r *ResourceReader) Size() int64 { return r.size }

func (r *ResourceReader) ReadAt(p []byte, offset int64) (int, error) {
	if offset < 0 {
		return 0, ErrResourceInvalid
	}
	if r.aead == nil {
		return 0, ErrResourceClosed
	}
	if len(p) == 0 {
		return 0, nil
	}
	if offset >= r.size {
		return 0, io.EOF
	}
	n := 0
	for len(p) > 0 && offset < r.size {
		sequence := uint64(offset / ResourceSegmentBytes)
		plain, err := r.segment(sequence)
		if err != nil {
			return n, err
		}
		used := copy(p, plain[offset%ResourceSegmentBytes:])
		clear(plain)
		n += used
		offset += int64(used)
		p = p[used:]
	}
	if len(p) > 0 {
		return n, io.EOF
	}
	return n, nil
}

func (r *ResourceReader) Verify() error {
	for sequence := uint64(0); sequence < r.segments; sequence++ {
		plain, err := r.segment(sequence)
		clear(plain)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *ResourceReader) Close() error {
	r.aead = nil
	clear(r.header)
	return nil
}
