package kv

import (
	"encoding/binary"
	"hash/crc32"
)

const (
	opPut    byte = 1
	opDelete byte = 2

	headerSize = 13

	MaxKeySize   = 64 << 10
	MaxValueSize = 1 << 30
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

type header struct {
	op   byte
	klen uint32
	vlen uint32
}

func (h header) recordLen() int64 {
	return headerSize + int64(h.klen) + int64(h.vlen)
}

func encodeRecord(op byte, key string, value []byte) []byte {
	rec := make([]byte, headerSize+len(key)+len(value))
	rec[4] = op
	binary.LittleEndian.PutUint32(rec[5:9], uint32(len(key)))
	binary.LittleEndian.PutUint32(rec[9:13], uint32(len(value)))
	copy(rec[headerSize:], key)
	copy(rec[headerSize+len(key):], value)
	binary.LittleEndian.PutUint32(rec[0:4], crc32.Checksum(rec[4:], castagnoli))
	return rec
}

func parseHeader(b []byte) (header, bool) {
	h := header{
		op:   b[4],
		klen: binary.LittleEndian.Uint32(b[5:9]),
		vlen: binary.LittleEndian.Uint32(b[9:13]),
	}
	ok := h.klen > 0 && h.klen <= MaxKeySize && h.vlen <= MaxValueSize
	switch h.op {
	case opPut:
	case opDelete:
		ok = ok && h.vlen == 0
	default:
		ok = false
	}
	return h, ok
}

func verifyRecord(rec []byte) (header, bool) {
	if len(rec) < headerSize {
		return header{}, false
	}
	h, ok := parseHeader(rec)
	if !ok || int64(len(rec)) != h.recordLen() {
		return h, false
	}
	if binary.LittleEndian.Uint32(rec[0:4]) != crc32.Checksum(rec[4:], castagnoli) {
		return h, false
	}
	return h, true
}

func recordKey(rec []byte, h header) string {
	return string(rec[headerSize : headerSize+int(h.klen)])
}
