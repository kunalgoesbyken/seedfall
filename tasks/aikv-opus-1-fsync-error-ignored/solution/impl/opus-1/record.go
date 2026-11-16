package kv

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	opPut byte = 1
	opDel byte = 2

	recordHeaderSize = 13
	fileHeaderSize   = 32

	MaxKeySize   = 1 << 16
	MaxValueSize = 1 << 30
)

var (
	ErrClosed        = errors.New("kv: database is closed")
	ErrCorrupt       = errors.New("kv: data file is corrupt")
	ErrKeyTooLarge   = errors.New("kv: key too large")
	ErrValueTooLarge = errors.New("kv: value too large")

	fileMagic    = [8]byte{'K', 'V', 'S', 'T', 'O', 'R', 'E', '1'}
	crcTable     = crc32.MakeTable(crc32.Castagnoli)
	errBadRecord = errors.New("kv: bad record")
)

type fileHeader struct {
	gen     uint64
	baseEnd int64
}

func encodeFileHeader(h fileHeader) []byte {
	b := make([]byte, fileHeaderSize)
	copy(b[0:8], fileMagic[:])
	binary.LittleEndian.PutUint64(b[8:16], h.gen)
	binary.LittleEndian.PutUint64(b[16:24], uint64(h.baseEnd))
	binary.LittleEndian.PutUint32(b[28:32], crc32.Checksum(b[0:28], crcTable))
	return b
}

func decodeFileHeader(b []byte) (fileHeader, error) {
	if len(b) != fileHeaderSize || [8]byte(b[0:8]) != fileMagic {
		return fileHeader{}, fmt.Errorf("%w: bad file header", ErrCorrupt)
	}
	if crc32.Checksum(b[0:28], crcTable) != binary.LittleEndian.Uint32(b[28:32]) {
		return fileHeader{}, fmt.Errorf("%w: file header checksum mismatch", ErrCorrupt)
	}
	h := fileHeader{
		gen:     binary.LittleEndian.Uint64(b[8:16]),
		baseEnd: int64(binary.LittleEndian.Uint64(b[16:24])),
	}
	if h.baseEnd < fileHeaderSize {
		return fileHeader{}, fmt.Errorf("%w: bad base offset", ErrCorrupt)
	}
	return h, nil
}

func recordSize(keyLen, valLen int) int64 {
	return int64(recordHeaderSize + keyLen + valLen)
}

func encodeRecord(op byte, key string, value []byte) []byte {
	b := make([]byte, recordSize(len(key), len(value)))
	b[4] = op
	binary.LittleEndian.PutUint32(b[5:9], uint32(len(key)))
	binary.LittleEndian.PutUint32(b[9:13], uint32(len(value)))
	copy(b[recordHeaderSize:], key)
	copy(b[recordHeaderSize+len(key):], value)
	binary.LittleEndian.PutUint32(b[0:4], crc32.Checksum(b[4:], crcTable))
	return b
}

func checkSizes(key string, value []byte) error {
	if len(key) > MaxKeySize {
		return ErrKeyTooLarge
	}
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	return nil
}

func parseRecordHeader(h []byte) (op byte, keyLen, valLen int, err error) {
	op = h[4]
	k := binary.LittleEndian.Uint32(h[5:9])
	v := binary.LittleEndian.Uint32(h[9:13])
	if (op != opPut && op != opDel) || k > MaxKeySize || v > MaxValueSize || (op == opDel && v != 0) {
		return 0, 0, 0, errBadRecord
	}
	return op, int(k), int(v), nil
}

func decodeRecord(b []byte) (op byte, key string, value []byte, err error) {
	if len(b) < recordHeaderSize {
		return 0, "", nil, errBadRecord
	}
	op, k, v, err := parseRecordHeader(b)
	if err != nil {
		return 0, "", nil, err
	}
	if int64(len(b)) != recordSize(k, v) || crc32.Checksum(b[4:], crcTable) != binary.LittleEndian.Uint32(b[0:4]) {
		return 0, "", nil, errBadRecord
	}
	return op, string(b[recordHeaderSize : recordHeaderSize+k]), b[recordHeaderSize+k:], nil
}

type scannedRecord struct {
	op     byte
	key    string
	offset int64
	size   int64
}

func scanRecord(r *bufio.Reader, offset, fileSize int64) (scannedRecord, error) {
	var hdr [recordHeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return scannedRecord{}, classifyReadErr(err)
	}
	op, k, v, err := parseRecordHeader(hdr[:])
	if err != nil {
		return scannedRecord{}, err
	}
	size := recordSize(k, v)
	if offset+size > fileSize {
		return scannedRecord{}, errBadRecord
	}
	key := make([]byte, k)
	if _, err := io.ReadFull(r, key); err != nil {
		return scannedRecord{}, classifyReadErr(err)
	}
	h := crc32.New(crcTable)
	h.Write(hdr[4:])
	h.Write(key)
	if _, err := io.CopyN(h, r, int64(v)); err != nil {
		return scannedRecord{}, classifyReadErr(err)
	}
	if h.Sum32() != binary.LittleEndian.Uint32(hdr[0:4]) {
		return scannedRecord{}, errBadRecord
	}
	return scannedRecord{op: op, key: string(key), offset: offset, size: size}, nil
}

func classifyReadErr(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errBadRecord
	}
	return err
}
