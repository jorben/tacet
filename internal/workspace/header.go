package workspace

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
)

// Header errors. None carries a path (INV-07).
var (
	ErrHeaderCorrupt = errors.New("WORKSPACE_HEADER_CORRUPT")
	ErrHeaderVersion = errors.New("WORKSPACE_FORMAT_UNSUPPORTED") // newer than this build (AR-18)
)

// HeaderFormat is the workspace header format version this build writes.
const HeaderFormat = 1

const (
	slotSize   = 64
	slotCount  = 2
	slotBytes  = 8 + 2 + 8 + 8 + 4 // magic, format, generation, sequence, crc
	headerSize = slotSize * slotCount
)

var magic = [8]byte{'T', 'A', 'C', 'E', 'T', 'W', 'S', 1}

type slot struct {
	format     uint16
	generation uint64
	sequence   uint64
}

func encode(s slot) []byte {
	b := make([]byte, slotSize)
	copy(b, magic[:])
	binary.BigEndian.PutUint16(b[8:], s.format)
	binary.BigEndian.PutUint64(b[10:], s.generation)
	binary.BigEndian.PutUint64(b[18:], s.sequence)
	binary.BigEndian.PutUint32(b[26:], crc32.ChecksumIEEE(b[:26]))
	return b
}

// decode returns ok=false for an empty or torn slot. A slot with a valid CRC
// but a newer format is reported through the error so it is never overwritten.
func decode(b []byte) (slot, bool, error) {
	empty := true
	for _, v := range b {
		if v != 0 {
			empty = false
			break
		}
	}
	if empty {
		return slot{}, false, nil
	}
	if string(b[:8]) != string(magic[:]) || crc32.ChecksumIEEE(b[:26]) != binary.BigEndian.Uint32(b[26:]) {
		return slot{}, false, nil
	}
	s := slot{binary.BigEndian.Uint16(b[8:]), binary.BigEndian.Uint64(b[10:]), binary.BigEndian.Uint64(b[18:])}
	if s.format > HeaderFormat {
		return slot{}, false, ErrHeaderVersion
	}
	return s, true, nil
}

// readLatest picks the valid slot with the highest sequence. If the file is
// non-empty yet has no valid slot it is corrupt: the generation is never
// reset, because a lower generation would let stale workers be admitted.
func readLatest(f *os.File) (slot, int, bool, error) {
	buf := make([]byte, headerSize)
	n, _ := f.ReadAt(buf, 0)
	if n == 0 {
		return slot{}, 0, false, nil
	}
	if n < headerSize {
		buf = append(buf[:n], make([]byte, headerSize-n)...)
	}
	var best slot
	bestIdx, found := 0, false
	for i := 0; i < slotCount; i++ {
		s, ok, err := decode(buf[i*slotSize : i*slotSize+slotBytes+(slotSize-slotBytes)])
		if err != nil {
			return slot{}, 0, false, err
		}
		if ok && (!found || s.sequence > best.sequence) {
			best, bestIdx, found = s, i, true
		}
	}
	if !found {
		return slot{}, 0, false, ErrHeaderCorrupt
	}
	return best, bestIdx, true, nil
}

// writeNext writes the next generation into the slot not currently in use and
// syncs, so a crash leaves either the old or the new generation intact.
func writeNext(f *os.File, cur slot, curIdx int, fresh bool) (slot, error) {
	next := slot{format: HeaderFormat, generation: cur.generation + 1, sequence: cur.sequence + 1}
	idx := 0
	if !fresh {
		idx = 1 - curIdx
	}
	if _, err := f.WriteAt(encode(next), int64(idx*slotSize)); err != nil {
		return slot{}, err
	}
	if err := f.Sync(); err != nil {
		return slot{}, err
	}
	return next, nil
}
