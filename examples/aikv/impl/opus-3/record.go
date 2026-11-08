package kv

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
)

const (
	headerSize = 13

	kindPut    byte = 1
	kindDelete byte = 2

	MaxKeySize   = 1 << 16
	MaxValueSize = 1 << 28
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

type record struct {
	kind byte
	key  string
	off  int64
	size uint32
}

func encodeRecord(kind byte, key string, value []byte) []byte {
	buf := make([]byte, headerSize+len(key)+len(value))
	buf[4] = kind
	binary.LittleEndian.PutUint32(buf[5:9], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[9:13], uint32(len(value)))
	copy(buf[headerSize:], key)
	copy(buf[headerSize+len(key):], value)
	binary.LittleEndian.PutUint32(buf[0:4], crc32.Checksum(buf[4:], castagnoli))
	return buf
}

func parseHeader(h []byte) (kind byte, keyLen, valLen uint32, ok bool) {
	kind = h[4]
	keyLen = binary.LittleEndian.Uint32(h[5:9])
	valLen = binary.LittleEndian.Uint32(h[9:13])
	ok = (kind == kindPut || kind == kindDelete) && keyLen <= MaxKeySize && valLen <= MaxValueSize
	if kind == kindDelete && valLen != 0 {
		ok = false
	}
	return kind, keyLen, valLen, ok
}

func decodeRecord(buf []byte) (kind byte, key string, value []byte, err error) {
	if len(buf) < headerSize {
		return 0, "", nil, ErrCorrupt
	}
	kind, keyLen, valLen, ok := parseHeader(buf)
	if !ok || int64(len(buf)) != headerSize+int64(keyLen)+int64(valLen) {
		return 0, "", nil, ErrCorrupt
	}
	if crc32.Checksum(buf[4:], castagnoli) != binary.LittleEndian.Uint32(buf[0:4]) {
		return 0, "", nil, ErrCorrupt
	}
	key = string(buf[headerSize : headerSize+keyLen])
	value = buf[headerSize+keyLen:]
	return kind, key, value, nil
}

func scanFile(f *os.File, size int64, fn func(record)) (int64, error) {
	r := bufio.NewReaderSize(io.NewSectionReader(f, 0, size), 1<<16)
	var header [headerSize]byte
	var body []byte
	var off int64
	for size-off >= headerSize {
		if _, err := io.ReadFull(r, header[:]); err != nil {
			return off, readErr(err)
		}
		kind, keyLen, valLen, ok := parseHeader(header[:])
		if !ok {
			return off, nil
		}
		total := headerSize + int64(keyLen) + int64(valLen)
		if total > size-off {
			return off, nil
		}
		n := int(keyLen + valLen)
		if cap(body) < n {
			body = make([]byte, n)
		}
		body = body[:n]
		if _, err := io.ReadFull(r, body); err != nil {
			return off, readErr(err)
		}
		crc := crc32.Update(crc32.Checksum(header[4:], castagnoli), castagnoli, body)
		if crc != binary.LittleEndian.Uint32(header[0:4]) {
			return off, nil
		}
		fn(record{kind: kind, key: string(body[:keyLen]), off: off, size: uint32(total)})
		off += total
	}
	return off, nil
}

func readErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ErrCorrupt
	}
	return err
}
