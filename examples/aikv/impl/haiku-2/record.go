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

	crcSize      = 4
	headerSize   = crcSize + 1 + 4 + 4
	maxKeySize   = 1<<16 - 1
	maxValueSize = 1 << 30
	scanChunk    = 64 << 10
)

var errBadRecord = errors.New("bad record")

type record struct {
	op    byte
	key   string
	value []byte
}

type recordHeader struct {
	op   byte
	klen uint32
	vlen uint32
}

func (h recordHeader) size() int64 {
	return int64(headerSize) + int64(h.klen) + int64(h.vlen)
}

func parseHeader(h []byte) (recordHeader, bool) {
	hd := recordHeader{
		op:   h[crcSize],
		klen: binary.LittleEndian.Uint32(h[crcSize+1:]),
		vlen: binary.LittleEndian.Uint32(h[crcSize+5:]),
	}
	ok := (hd.op == opPut || hd.op == opDelete) &&
		hd.klen <= maxKeySize &&
		hd.vlen <= maxValueSize &&
		(hd.op == opPut || hd.vlen == 0)
	return hd, ok
}

func encodeRecord(rec record) []byte {
	buf := make([]byte, headerSize+len(rec.key)+len(rec.value))
	buf[crcSize] = rec.op
	binary.LittleEndian.PutUint32(buf[crcSize+1:], uint32(len(rec.key)))
	binary.LittleEndian.PutUint32(buf[crcSize+5:], uint32(len(rec.value)))
	copy(buf[headerSize:], rec.key)
	copy(buf[headerSize+len(rec.key):], rec.value)
	binary.LittleEndian.PutUint32(buf, crc32.ChecksumIEEE(buf[crcSize:]))
	return buf
}

func readRecord(f io.ReaderAt, off, end int64) (record, int64, error) {
	if end-off < int64(headerSize) {
		return record{}, 0, errBadRecord
	}
	var h [headerSize]byte
	if _, err := f.ReadAt(h[:], off); err != nil {
		return record{}, 0, err
	}
	hd, ok := parseHeader(h[:])
	if !ok || hd.size() > end-off {
		return record{}, 0, errBadRecord
	}
	buf := make([]byte, hd.size())
	if _, err := f.ReadAt(buf, off); err != nil {
		return record{}, 0, err
	}
	if crc32.ChecksumIEEE(buf[crcSize:]) != binary.LittleEndian.Uint32(buf) {
		return record{}, 0, errBadRecord
	}
	rec := record{
		op:    hd.op,
		key:   string(buf[headerSize : headerSize+int(hd.klen)]),
		value: buf[headerSize+int(hd.klen):],
	}
	return rec, hd.size(), nil
}

func containsRecord(f io.ReaderAt, from, size int64) (bool, error) {
	buf := make([]byte, scanChunk)
	for base := from; base < size; {
		n, err := f.ReadAt(buf, base)
		if err != nil && !errors.Is(err, io.EOF) {
			return false, err
		}
		for i := 0; i+headerSize <= n; i++ {
			off := base + int64(i)
			hd, ok := parseHeader(buf[i : i+headerSize])
			if !ok || hd.size() > size-off {
				continue
			}
			_, _, err := readRecord(f, off, size)
			if err == nil {
				return true, nil
			}
			if !errors.Is(err, errBadRecord) {
				return false, err
			}
		}
		if n < len(buf) {
			break
		}
		base += int64(n - headerSize + 1)
	}
	return false, nil
}
