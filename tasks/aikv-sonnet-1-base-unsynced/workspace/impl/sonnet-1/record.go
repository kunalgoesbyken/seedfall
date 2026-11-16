package kv

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
)

const (
	fileMagic     = "KVSEG\x00\x00\x01"
	fileHeaderLen = int64(len(fileMagic))
	recHeaderLen  = 13

	typePut    byte = 1
	typeDelete byte = 2

	extLog  = ".log"
	extBase = ".base"
	extTmp  = ".tmp"

	scanBufSize  = 1 << 16
	scanChunkLen = 1 << 15
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

type entry struct {
	seg  uint64
	off  int64
	size uint32
}

func encodeRecord(typ byte, key string, value []byte) []byte {
	buf := make([]byte, recHeaderLen+len(key)+len(value))
	buf[4] = typ
	binary.LittleEndian.PutUint32(buf[5:], uint32(len(key)))
	binary.LittleEndian.PutUint32(buf[9:], uint32(len(value)))
	copy(buf[recHeaderLen:], key)
	copy(buf[recHeaderLen+len(key):], value)
	binary.LittleEndian.PutUint32(buf[0:], crc32.Checksum(buf[4:], castagnoli))
	return buf
}

func parseRecord(buf []byte) (typ byte, key, value []byte, ok bool) {
	if len(buf) < recHeaderLen {
		return 0, nil, nil, false
	}
	typ = buf[4]
	klen := uint64(binary.LittleEndian.Uint32(buf[5:]))
	vlen := uint64(binary.LittleEndian.Uint32(buf[9:]))
	if typ != typePut && typ != typeDelete {
		return 0, nil, nil, false
	}
	if uint64(len(buf)) != recHeaderLen+klen+vlen {
		return 0, nil, nil, false
	}
	if crc32.Checksum(buf[4:], castagnoli) != binary.LittleEndian.Uint32(buf[0:]) {
		return 0, nil, nil, false
	}
	keyEnd := recHeaderLen + int(klen)
	return typ, buf[recHeaderLen:keyEnd], buf[keyEnd:len(buf):len(buf)], true
}

func scanRecords(r io.ReaderAt, size int64, visit func(typ byte, key string, off, n int64)) (int64, error) {
	br := bufio.NewReaderSize(io.NewSectionReader(r, fileHeaderLen, size-fileHeaderLen), scanBufSize)
	var hdr [recHeaderLen]byte
	var keyBuf []byte
	chunk := make([]byte, scanChunkLen)
	off := fileHeaderLen
	for off < size {
		if size-off < recHeaderLen {
			return off, nil
		}
		if _, err := io.ReadFull(br, hdr[:]); err != nil {
			return tornOrErr(off, err)
		}
		typ := hdr[4]
		klen := int64(binary.LittleEndian.Uint32(hdr[5:]))
		vlen := int64(binary.LittleEndian.Uint32(hdr[9:]))
		if typ != typePut && typ != typeDelete {
			return off, nil
		}
		if klen > MaxKeySize || vlen > MaxValueSize || (typ == typeDelete && vlen != 0) {
			return off, nil
		}
		total := recHeaderLen + klen + vlen
		if off+total > size {
			return off, nil
		}
		if int64(cap(keyBuf)) < klen {
			keyBuf = make([]byte, klen)
		}
		keyBuf = keyBuf[:klen]
		if _, err := io.ReadFull(br, keyBuf); err != nil {
			return tornOrErr(off, err)
		}
		sum := crc32.Checksum(hdr[4:], castagnoli)
		sum = crc32.Update(sum, castagnoli, keyBuf)
		for remaining := vlen; remaining > 0; {
			part := chunk[:min(remaining, int64(len(chunk)))]
			if _, err := io.ReadFull(br, part); err != nil {
				return tornOrErr(off, err)
			}
			sum = crc32.Update(sum, castagnoli, part)
			remaining -= int64(len(part))
		}
		if sum != binary.LittleEndian.Uint32(hdr[0:]) {
			return off, nil
		}
		visit(typ, string(keyBuf), off, total)
		off += total
	}
	return off, nil
}

func tornOrErr(off int64, err error) (int64, error) {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return off, nil
	}
	return off, err
}

func hasHeader(r io.ReaderAt, size int64) (bool, error) {
	if size < fileHeaderLen {
		return false, nil
	}
	var buf [len(fileMagic)]byte
	if _, err := r.ReadAt(buf[:], 0); err != nil {
		return false, err
	}
	return bytes.Equal(buf[:], []byte(fileMagic)), nil
}

func segName(id uint64, ext string) string {
	return fmt.Sprintf("%016d%s", id, ext)
}

func parseSegName(name string) (id uint64, ext string, ok bool) {
	for _, e := range []string{extLog, extBase, extTmp} {
		stem, found := strings.CutSuffix(name, e)
		if !found || len(stem) != 16 {
			continue
		}
		n, err := strconv.ParseUint(stem, 10, 64)
		if err != nil {
			return 0, "", false
		}
		return n, e, true
	}
	return 0, "", false
}
