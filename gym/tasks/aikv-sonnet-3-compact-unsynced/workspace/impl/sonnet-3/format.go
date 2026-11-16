package kv

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	segHeaderSize = 8
	recHeaderSize = 13
	maxKeySize    = 1 << 16
	maxValueSize  = 1 << 28

	recPut    byte = 1
	recDelete byte = 2

	segExt    = ".seg"
	cmpExt    = ".cmp"
	tmpSuffix = ".tmp"
)

var (
	segMagic   = [segHeaderSize]byte{'K', 'V', 'S', 'E', 'G', 0, 0, 1}
	castagnoli = crc32.MakeTable(crc32.Castagnoli)
)

type recHeader struct {
	crc    uint32
	typ    byte
	keyLen uint32
	valLen uint32
}

func (h recHeader) size() int64 {
	return recHeaderSize + int64(h.keyLen) + int64(h.valLen)
}

func parseHeader(b []byte) (recHeader, bool) {
	h := recHeader{
		crc:    binary.LittleEndian.Uint32(b[0:4]),
		typ:    b[4],
		keyLen: binary.LittleEndian.Uint32(b[5:9]),
		valLen: binary.LittleEndian.Uint32(b[9:13]),
	}
	if h.keyLen > maxKeySize || h.valLen > maxValueSize {
		return h, false
	}
	switch h.typ {
	case recPut:
	case recDelete:
		if h.valLen != 0 {
			return h, false
		}
	default:
		return h, false
	}
	return h, true
}

func encodeRecord(typ byte, key string, value []byte) []byte {
	buf := make([]byte, recHeaderSize+len(key)+len(value))
	buf[4] = typ
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[9:13], uint32(len(value)))
	copy(buf[recHeaderSize:], key)
	copy(buf[recHeaderSize+len(key):], value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.Checksum(buf[4:], castagnoli))
	return buf
}

func decodePut(buf []byte, key string) ([]byte, bool) {
	if len(buf) < recHeaderSize {
		return nil, false
	}
	h, ok := parseHeader(buf)
	if !ok || h.typ != recPut || h.size() != int64(len(buf)) {
		return nil, false
	}
	if crc32.Checksum(buf[4:], castagnoli) != h.crc {
		return nil, false
	}
	if string(buf[recHeaderSize:recHeaderSize+int(h.keyLen)]) != key {
		return nil, false
	}
	return buf[recHeaderSize+int(h.keyLen):], true
}

type scanned struct {
	typ  byte
	key  string
	off  int64
	size int64
}

func scanRecords(f *os.File, size int64, visit func(scanned)) (int64, bool, error) {
	br := bufio.NewReaderSize(io.NewSectionReader(f, segHeaderSize, size-segHeaderSize), 1<<18)
	sum := crc32.New(castagnoli)
	var hdr [recHeaderSize]byte
	off := int64(segHeaderSize)
	torn := func(err error) (int64, bool, error) {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return off, false, nil
		}
		return off, false, err
	}
	for off < size {
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return torn(err)
		}
		h, ok := parseHeader(hdr[:])
		if !ok || off+h.size() > size {
			return off, false, nil
		}
		key := make([]byte, h.keyLen)
		if _, err := io.ReadFull(br, key); err != nil {
			return torn(err)
		}
		sum.Reset()
		sum.Write(hdr[4:])
		sum.Write(key)
		if _, err := io.CopyN(sum, br, int64(h.valLen)); err != nil {
			return torn(err)
		}
		if sum.Sum32() != h.crc {
			return off, false, nil
		}
		visit(scanned{typ: h.typ, key: string(key), off: off, size: h.size()})
		off += h.size()
	}
	return off, true, nil
}

func segName(id uint64, cmp bool) string {
	ext := segExt
	if cmp {
		ext = cmpExt
	}
	return fmt.Sprintf("%020d%s", id, ext)
}

func parseSegName(name string) (uint64, bool, bool) {
	var cmp bool
	var base string
	switch {
	case strings.HasSuffix(name, segExt):
		base = strings.TrimSuffix(name, segExt)
	case strings.HasSuffix(name, cmpExt):
		base = strings.TrimSuffix(name, cmpExt)
		cmp = true
	default:
		return 0, false, false
	}
	id, err := strconv.ParseUint(base, 10, 64)
	if err != nil || segName(id, cmp) != name {
		return 0, false, false
	}
	return id, cmp, true
}
