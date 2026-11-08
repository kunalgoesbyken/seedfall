package kv

import (
	"bufio"
	"encoding/binary"
	"hash/crc32"
	"io"
)

const (
	kindPut    byte = 1
	kindDelete byte = 2
	kindCommit byte = 3

	headerSize       = 13
	commitBodySize   = 16
	commitRecordSize = headerSize + commitBodySize
	scanBufferSize   = 256 << 10
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type record struct {
	kind byte
	key  []byte
	val  []byte
	off  int64
	size int64
}

func encodeRecord(dst []byte, kind byte, key string, val []byte) []byte {
	n := headerSize + len(key) + len(val)
	if cap(dst) < n {
		dst = make([]byte, n)
	}
	dst = dst[:n]
	dst[4] = kind
	binary.LittleEndian.PutUint32(dst[5:], uint32(len(key)))
	binary.LittleEndian.PutUint32(dst[9:], uint32(len(val)))
	copy(dst[headerSize:], key)
	copy(dst[headerSize+len(key):], val)
	binary.LittleEndian.PutUint32(dst, crc32.Checksum(dst[4:], crcTable))
	return dst
}

func encodeCommit(count uint64, dataLen int64) []byte {
	var body [commitBodySize]byte
	binary.LittleEndian.PutUint64(body[0:], count)
	binary.LittleEndian.PutUint64(body[8:], uint64(dataLen))
	return encodeRecord(nil, kindCommit, "", body[:])
}

func parseCommitBody(val []byte) (count uint64, dataLen int64) {
	return binary.LittleEndian.Uint64(val[0:]), int64(binary.LittleEndian.Uint64(val[8:]))
}

func parseHeader(h []byte) (kind byte, klen, vlen int, ok bool) {
	kind = h[4]
	k := binary.LittleEndian.Uint32(h[5:])
	v := binary.LittleEndian.Uint32(h[9:])
	if k > MaxKeySize || v > MaxValueSize {
		return 0, 0, 0, false
	}
	switch kind {
	case kindPut:
	case kindDelete:
		if v != 0 {
			return 0, 0, 0, false
		}
	case kindCommit:
		if k != 0 || v != commitBodySize {
			return 0, 0, 0, false
		}
	default:
		return 0, 0, 0, false
	}
	return kind, int(k), int(v), true
}

func decodeRecord(buf []byte) (rec record, ok bool) {
	if len(buf) < headerSize {
		return record{}, false
	}
	kind, kl, vl, valid := parseHeader(buf)
	if !valid || len(buf) != headerSize+kl+vl {
		return record{}, false
	}
	if crc32.Checksum(buf[4:], crcTable) != binary.LittleEndian.Uint32(buf) {
		return record{}, false
	}
	return record{
		kind: kind,
		key:  buf[headerSize : headerSize+kl],
		val:  buf[headerSize+kl:],
		size: int64(len(buf)),
	}, true
}

func scan(r io.ReaderAt, size int64, fn func(record) error) (int64, error) {
	br := bufio.NewReaderSize(io.NewSectionReader(r, 0, size), scanBufferSize)
	var off int64
	var hdr [headerSize]byte
	var body []byte
	for size-off >= headerSize {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return off, err
		}
		kind, kl, vl, ok := parseHeader(hdr[:])
		if !ok {
			return off, nil
		}
		total := int64(headerSize) + int64(kl) + int64(vl)
		if total > size-off {
			return off, nil
		}
		n := kl + vl
		if cap(body) < n {
			body = make([]byte, n)
		}
		body = body[:n]
		if _, err := io.ReadFull(br, body); err != nil {
			return off, err
		}
		sum := crc32.Update(crc32.Checksum(hdr[4:], crcTable), crcTable, body)
		if sum != binary.LittleEndian.Uint32(hdr[:4]) {
			return off, nil
		}
		rec := record{kind: kind, key: body[:kl], val: body[kl:], off: off, size: total}
		if err := fn(rec); err != nil {
			return off, err
		}
		off += total
	}
	return off, nil
}

func readCommit(r io.ReaderAt, size int64) (count uint64, ok bool, err error) {
	if size < commitRecordSize {
		return 0, false, nil
	}
	buf := make([]byte, commitRecordSize)
	if _, err := r.ReadAt(buf, size-commitRecordSize); err != nil {
		return 0, false, err
	}
	rec, valid := decodeRecord(buf)
	if !valid || rec.kind != kindCommit {
		return 0, false, nil
	}
	count, dataLen := parseCommitBody(rec.val)
	if dataLen != size-commitRecordSize {
		return 0, false, nil
	}
	return count, true, nil
}
