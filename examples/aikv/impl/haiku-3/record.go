package kv

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

const (
	opPut    byte = 1
	opDelete byte = 2

	headerSize = 13

	maxKeySize   = 1<<16 - 1
	maxValueSize = 1 << 30
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

type record struct {
	op    byte
	key   []byte
	value []byte
}

func (r record) size() int64 {
	return int64(headerSize + len(r.key) + len(r.value))
}

func (r record) encode() []byte {
	buf := make([]byte, headerSize+len(r.key)+len(r.value))
	buf[4] = r.op
	binary.LittleEndian.PutUint32(buf[5:], uint32(len(r.key)))
	binary.LittleEndian.PutUint32(buf[9:], uint32(len(r.value)))
	copy(buf[headerSize:], r.key)
	copy(buf[headerSize+len(r.key):], r.value)
	binary.LittleEndian.PutUint32(buf[0:], checksum(buf[4:]))
	return buf
}

func checksum(b []byte) uint32 {
	return crc32.Checksum(b, castagnoli)
}

func plausibleHeader(h []byte, remaining int64) bool {
	op := h[4]
	klen := int64(binary.LittleEndian.Uint32(h[5:]))
	vlen := int64(binary.LittleEndian.Uint32(h[9:]))
	if op != opPut && op != opDelete {
		return false
	}
	if op == opDelete && vlen != 0 {
		return false
	}
	if klen == 0 || klen > maxKeySize || vlen > maxValueSize {
		return false
	}
	return headerSize+klen+vlen <= remaining
}

func readRecord(r io.ReaderAt, off, limit int64) (record, bool, error) {
	if off < 0 || limit-off < headerSize {
		return record{}, false, nil
	}
	var hdr [headerSize]byte
	if _, err := r.ReadAt(hdr[:], off); err != nil {
		if errors.Is(err, io.EOF) {
			return record{}, false, nil
		}
		return record{}, false, err
	}
	if !plausibleHeader(hdr[:], limit-off) {
		return record{}, false, nil
	}
	total := int64(headerSize) + int64(binary.LittleEndian.Uint32(hdr[5:])) + int64(binary.LittleEndian.Uint32(hdr[9:]))
	buf := make([]byte, total)
	copy(buf, hdr[:])
	if _, err := r.ReadAt(buf[headerSize:], off+headerSize); err != nil {
		if errors.Is(err, io.EOF) {
			return record{}, false, nil
		}
		return record{}, false, err
	}
	if binary.LittleEndian.Uint32(buf[0:]) != checksum(buf[4:]) {
		return record{}, false, nil
	}
	klen := int(binary.LittleEndian.Uint32(buf[5:]))
	return record{
		op:    buf[4],
		key:   buf[headerSize : headerSize+klen],
		value: buf[headerSize+klen:],
	}, true, nil
}

func hasRecordAfter(r io.ReaderAt, from, size int64) (bool, error) {
	const window = 1 << 20
	for ws := from; ws+headerSize <= size; ws += window {
		n := size - ws
		if n > window+headerSize {
			n = window + headerSize
		}
		buf := make([]byte, n)
		if _, err := r.ReadAt(buf, ws); err != nil {
			return false, err
		}
		for i := 0; i+headerSize <= len(buf) && i < window; i++ {
			if !plausibleHeader(buf[i:i+headerSize], size-ws-int64(i)) {
				continue
			}
			_, ok, err := readRecord(r, ws+int64(i), size)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
	}
	return false, nil
}
