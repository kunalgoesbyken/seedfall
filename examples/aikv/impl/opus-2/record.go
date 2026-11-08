package kv

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

const (
	recPut byte = 1
	recDel byte = 2

	recHeaderSize  = 13
	fileHeaderSize = 8

	maxKeySize   = 1 << 16
	maxValueSize = 1 << 30
)

var (
	segMagic  = [fileHeaderSize]byte{'K', 'V', 'S', 'E', 'G', 0, 0, 1}
	baseMagic = [fileHeaderSize]byte{'K', 'V', 'B', 'A', 'S', 0, 0, 1}
	crcTable  = crc32.MakeTable(crc32.Castagnoli)
)

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrCorrupt       = errors.New("kv: data corruption detected")
	ErrKeyTooLarge   = errors.New("kv: key too large")
	ErrValueTooLarge = errors.New("kv: value too large")
	ErrLocked        = errors.New("kv: database directory already open")
)

func recordSize(key string, value []byte) int64 {
	return int64(recHeaderSize + len(key) + len(value))
}

func encodeRecord(typ byte, key string, value []byte) []byte {
	buf := make([]byte, recordSize(key, value))
	buf[4] = typ
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[9:13], uint32(len(value)))
	copy(buf[recHeaderSize:], key)
	copy(buf[recHeaderSize+len(key):], value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.Checksum(buf[4:], crcTable))
	return buf
}

type recordHeader struct {
	crc  uint32
	typ  byte
	klen int64
	vlen int64
}

func parseHeader(h []byte) (recordHeader, bool) {
	rh := recordHeader{
		crc:  binary.LittleEndian.Uint32(h[0:4]),
		typ:  h[4],
		klen: int64(binary.LittleEndian.Uint32(h[5:9])),
		vlen: int64(binary.LittleEndian.Uint32(h[9:13])),
	}
	switch {
	case rh.typ != recPut && rh.typ != recDel:
		return rh, false
	case rh.klen > maxKeySize || rh.vlen > maxValueSize:
		return rh, false
	case rh.typ == recDel && rh.vlen != 0:
		return rh, false
	}
	return rh, true
}

func (rh recordHeader) size() int64 {
	return recHeaderSize + rh.klen + rh.vlen
}

func decodeRecord(buf []byte) (typ byte, key string, value []byte, err error) {
	if len(buf) < recHeaderSize {
		return 0, "", nil, ErrCorrupt
	}
	rh, ok := parseHeader(buf)
	if !ok || rh.size() != int64(len(buf)) || crc32.Checksum(buf[4:], crcTable) != rh.crc {
		return 0, "", nil, ErrCorrupt
	}
	keyEnd := recHeaderSize + rh.klen
	return rh.typ, string(buf[recHeaderSize:keyEnd]), buf[keyEnd:], nil
}

type scanFunc func(typ byte, key string, off, size int64)

type scanResult struct {
	validEnd  int64
	torn      bool
	badHeader bool
}

func scanFile(f *os.File, size int64, magic [fileHeaderSize]byte, fn scanFunc) (scanResult, error) {
	if size < fileHeaderSize {
		return scanResult{torn: true}, nil
	}
	r := bufio.NewReaderSize(io.NewSectionReader(f, 0, size), 1<<20)
	var hdr [fileHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return scanResult{}, err
	}
	if hdr != magic {
		return scanResult{badHeader: true}, nil
	}
	off := int64(fileHeaderSize)
	var rh [recHeaderSize]byte
	var body []byte
	for off < size {
		if size-off < recHeaderSize {
			return scanResult{validEnd: off, torn: true}, nil
		}
		if _, err := io.ReadFull(r, rh[:]); err != nil {
			return scanResult{}, err
		}
		h, ok := parseHeader(rh[:])
		if !ok || h.size() > size-off {
			return scanResult{validEnd: off, torn: true}, nil
		}
		bodyLen := h.klen + h.vlen
		if int64(cap(body)) < bodyLen {
			body = make([]byte, bodyLen)
		}
		body = body[:bodyLen]
		if _, err := io.ReadFull(r, body); err != nil {
			return scanResult{}, err
		}
		crc := crc32.Update(crc32.Checksum(rh[4:], crcTable), crcTable, body)
		if crc != h.crc {
			return scanResult{validEnd: off, torn: true}, nil
		}
		fn(h.typ, string(body[:h.klen]), off, h.size())
		off += h.size()
	}
	return scanResult{validEnd: off}, nil
}

func corruptf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrCorrupt, fmt.Sprintf(format, args...))
}
