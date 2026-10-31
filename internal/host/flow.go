package host

import (
	"errors"
	"sync"
)

var ErrProtocolSequence = errors.New("PROTOCOL_SEQUENCE_INVALID")
var ErrProtocolCredit = errors.New("PROTOCOL_CREDIT_EXCEEDED")
var ErrProtocolCommit = errors.New("PROTOCOL_COMMIT_INVALID")

// Flow tracks data admission separately from durable commit. Acks cannot get
// ahead of accepted items, credits cannot exceed the configured byte/item cap,
// and end-of-stream cannot be declared complete with uncommitted items.
// Control messages are not charged against data credit and use another lane.
// Sequences begin at 1; 0 is the empty-stream/initial committed boundary.
type Flow struct {
	mu              sync.Mutex
	maxBytes        uint64
	maxItems        uint64
	bytes           uint64
	items           uint64
	received        uint64
	committed       uint64
	pending         map[uint64]uint64
	releasableBytes uint64
	releasableItems uint64
	ended           bool
}

func NewFlow(byteLimit, itemLimit uint64) (*Flow, error) {
	if byteLimit == 0 || itemLimit == 0 || byteLimit > MaxMessageBytes || itemLimit > 65536 {
		return nil, ErrProtocolCredit
	}
	return &Flow{maxBytes: byteLimit, maxItems: itemLimit, bytes: byteLimit, items: itemLimit, pending: make(map[uint64]uint64)}, nil
}

func (f *Flow) Accept(sequence, size uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended || sequence != f.received+1 || sequence == 0 {
		return ErrProtocolSequence
	}
	if size == 0 || size > f.bytes || f.items == 0 {
		return ErrProtocolCredit
	}
	f.bytes -= size
	f.items--
	f.received = sequence
	f.pending[sequence] = size
	return nil
}

// Commit confirms durable contiguous results only. Release downstream memory
// separately via Credit, because database persistence does not imply that the
// inference queue or other downstream buffers have become available.
func (f *Flow) Commit(sequence uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended || sequence < f.committed || sequence > f.received {
		return ErrProtocolCommit
	}
	for seq := f.committed + 1; seq <= sequence; seq++ {
		f.releasableBytes += f.pending[seq]
		f.releasableItems++
		delete(f.pending, seq)
	}
	f.committed = sequence
	return nil
}

func (f *Flow) Credit(bytes, items uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended || bytes > f.maxBytes-f.bytes || items > f.maxItems-f.items || bytes > f.releasableBytes || items > f.releasableItems {
		return ErrProtocolCredit
	}
	f.releasableBytes -= bytes
	f.releasableItems -= items
	f.bytes += bytes
	f.items += items
	return nil
}

func (f *Flow) End(last uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended || last != f.received || last != f.committed {
		return ErrProtocolSequence
	}
	f.ended = true
	return nil
}
