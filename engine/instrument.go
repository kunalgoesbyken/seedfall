package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
)

var errTruncated = errors.New("truncated wasm code section")

const (
	callsExport = "seedfall.calls"
	gExport     = "seedfall.g"

	goGlobalG = 2

	secCustom = 0
	secImport = 2
	secGlobal = 6
	secExport = 7
	secCode   = 10

	kindFunc   = 0
	kindGlobal = 3

	opBlock      = 0x02
	opLoop       = 0x03
	opIf         = 0x04
	opElse       = 0x05
	opEnd        = 0x0b
	opBr         = 0x0c
	opBrIf       = 0x0d
	opBrTable    = 0x0e
	opCall       = 0x10
	opCallInd    = 0x11
	opReturnCall = 0x12
	opReturnInd  = 0x13
	opDrop       = 0x1a
	opSelectT    = 0x1c
	opLocalGet   = 0x20
	opTableSet   = 0x26
	opLoad       = 0x28
	opStore      = 0x3e
	opMemorySize = 0x3f
	opMemoryGrow = 0x40
	opGlobalGet  = 0x23
	opGlobalSet  = 0x24
	opI32Const   = 0x41
	opI64Const   = 0x42
	opF32Const   = 0x43
	opF64Const   = 0x44
	opI32Eqz     = 0x45
	opI32Sub     = 0x6b
	opLastNum    = 0xc4
	opRefNull    = 0xd0
	opRefFunc    = 0xd2
	opPrefixFC   = 0xfc
	typeI32      = 0x7f
	blockEmpty   = 0x40
	mutable      = 0x01
)

type wasmSection struct {
	id   byte
	body []byte
}

type wasmReader struct {
	b   []byte
	ok  bool
	pos int
}

func (r *wasmReader) uleb() uint64 {
	v, n := binary.Uvarint(r.b[min(r.pos, len(r.b)):])
	if n <= 0 {
		r.ok = false
		return 0
	}
	r.pos += n
	return v
}

func (r *wasmReader) bytes(n uint64) []byte {
	if !r.ok || n > uint64(len(r.b)-r.pos) {
		r.ok = false
		return nil
	}
	b := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return b
}

func (r *wasmReader) byte() byte {
	b := r.bytes(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *wasmReader) skip(n int) { r.bytes(uint64(n)) }

func (r *wasmReader) leb() {
	for b := byte(0x80); r.ok && b&0x80 != 0; {
		b = r.byte()
	}
}

func (r *wasmReader) instruction() (byte, error) {
	op := r.byte()
	switch {
	case op == opBlock || op == opLoop || op == opIf:
		r.leb()
	case op == opBr || op == opBrIf || op == opCall || op == opReturnCall || op == opRefFunc || (op >= opLocalGet && op <= opTableSet):
		r.uleb()
	case op == opBrTable:
		for range r.uleb() + 1 {
			r.uleb()
		}
	case op == opCallInd || op == opReturnInd || (op >= opLoad && op <= opStore):
		r.uleb()
		r.uleb()
	case op == opSelectT:
		r.skip(int(r.uleb()))
	case op == opMemorySize || op == opMemoryGrow || op == opRefNull:
		r.skip(1)
	case op == opI32Const || op == opI64Const:
		r.leb()
	case op == opF32Const:
		r.skip(4)
	case op == opF64Const:
		r.skip(8)
	case op == opPrefixFC:
		return op, r.prefixed()
	case op > opRefFunc || (op > opElse && op < opEnd) || (op > opReturnInd && op < opDrop) || (op > opSelectT && op < opLocalGet) || (op > opTableSet && op < opLoad) || (op > opLastNum && op < opRefNull):
		return op, fmt.Errorf("unsupported wasm opcode 0x%02x", op)
	}
	return op, nil
}

func (r *wasmReader) prefixed() error {
	switch sub := r.uleb(); {
	case sub <= 7:
	case sub == 8:
		r.uleb()
		r.skip(1)
	case sub == 10:
		r.skip(2)
	case sub == 11:
		r.skip(1)
	case sub == 12 || sub == 14:
		r.uleb()
		r.uleb()
	case sub <= 17:
		r.uleb()
	default:
		return fmt.Errorf("unsupported wasm opcode 0xfc %d", sub)
	}
	return nil
}

func (r *wasmReader) name() string { return string(r.bytes(r.uleb())) }

func (r *wasmReader) rest() []byte { return r.bytes(uint64(len(r.b) - r.pos)) }

func readSections(wasm []byte) ([]wasmSection, bool) {
	if len(wasm) < 8 {
		return nil, false
	}
	r := &wasmReader{b: wasm, ok: true, pos: 8}
	var secs []wasmSection
	for r.ok && r.pos < len(wasm) {
		id := r.byte()
		secs = append(secs, wasmSection{id: id, body: r.bytes(r.uleb())})
	}
	return secs, r.ok
}

func section(secs []wasmSection, id byte) *wasmSection {
	for i := range secs {
		if secs[i].id == id {
			return &secs[i]
		}
	}
	return nil
}

func builtByGo(secs []wasmSection) bool {
	for _, s := range secs {
		if s.id == secCustom && (&wasmReader{b: s.body, ok: true}).name() == "go:buildid" {
			return true
		}
	}
	return false
}

func importedFunc(imports []byte, module, name string) (uint64, bool) {
	r := &wasmReader{b: imports, ok: true}
	var funcs uint64
	for range r.uleb() {
		m, n, kind := r.name(), r.name(), r.byte()
		if kind != kindFunc || !r.ok {
			return 0, false
		}
		r.uleb()
		if m == module && n == name {
			return funcs, true
		}
		funcs++
	}
	return 0, false
}

func appendEntries(body []byte, entries ...[]byte) ([]byte, uint64, bool) {
	r := &wasmReader{b: body, ok: true}
	count := r.uleb()
	out := binary.AppendUvarint(nil, count+uint64(len(entries)))
	out = append(out, r.rest()...)
	for _, e := range entries {
		out = append(out, e...)
	}
	return out, count, r.ok
}

func exportEntry(name string, kind byte, index uint64) []byte {
	e := binary.AppendUvarint(nil, uint64(len(name)))
	e = append(append(e, name...), kind)
	return binary.AppendUvarint(e, index)
}

func countdown(calls, yield uint64) []byte {
	get := binary.AppendUvarint([]byte{opGlobalGet}, calls)
	set := binary.AppendUvarint([]byte{opGlobalSet}, calls)
	code := append(append(append(get, opI32Const, 1, opI32Sub), set...), get...)
	code = append(code, opI32Eqz, opIf, blockEmpty, opCall)
	return append(binary.AppendUvarint(code, yield), opDrop, opEnd)
}

func rewriteBodies(code []byte, rewrite func(locals, body []byte) ([]byte, error)) ([]byte, error) {
	r := &wasmReader{b: code, ok: true}
	count := r.uleb()
	out := binary.AppendUvarint(nil, count)
	for i := range count {
		fn := &wasmReader{b: r.bytes(r.uleb()), ok: r.ok}
		for range fn.uleb() {
			fn.uleb()
			fn.byte()
		}
		locals := fn.b[:min(fn.pos, len(fn.b))]
		rest := fn.rest()
		if !fn.ok {
			return nil, errTruncated
		}
		body, err := rewrite(locals, rest)
		if err != nil {
			return nil, fmt.Errorf("function %d: %w", i, err)
		}
		out = append(binary.AppendUvarint(out, uint64(len(body))), body...)
	}
	if !r.ok || r.pos != len(code) {
		return nil, errTruncated
	}
	return out, nil
}

func countLoops(out, body, countdown []byte) ([]byte, error) {
	r := &wasmReader{b: body, ok: true}
	for r.ok && r.pos < len(body) {
		start := r.pos
		op, err := r.instruction()
		if err != nil {
			return nil, err
		}
		out = append(out, body[start:r.pos]...)
		if op == opLoop {
			out = append(out, countdown...)
		}
	}
	if !r.ok {
		return nil, errTruncated
	}
	return out, nil
}

func instrument(wasm []byte, loops bool) ([]byte, error) {
	secs, ok := readSections(wasm)
	imports, globals, exports, code := section(secs, secImport), section(secs, secGlobal), section(secs, secExport), section(secs, secCode)
	if !ok || !builtByGo(secs) || imports == nil || globals == nil || exports == nil || code == nil {
		return wasm, nil
	}
	yield, ok := importedFunc(imports.body, "wasi_snapshot_preview1", "sched_yield")
	if !ok {
		return wasm, nil
	}
	globalBody, calls, ok := appendEntries(globals.body, []byte{typeI32, mutable, opI32Const, 0, opEnd})
	if !ok {
		return wasm, nil
	}
	exportBody, _, ok := appendEntries(exports.body, exportEntry(callsExport, kindGlobal, calls), exportEntry(gExport, kindGlobal, goGlobalG))
	if !ok {
		return wasm, nil
	}
	tick := countdown(calls, yield)
	codeBody, err := rewriteBodies(code.body, func(locals, body []byte) ([]byte, error) {
		out := append(append([]byte(nil), locals...), tick...)
		if !loops {
			return append(out, body...), nil
		}
		return countLoops(out, body, tick)
	})
	switch {
	case err != nil && loops:
		return nil, fmt.Errorf("preempt.loops: %w", err)
	case err != nil:
		return wasm, nil
	}
	globals.body, exports.body, code.body = globalBody, exportBody, codeBody
	out := append([]byte(nil), wasm[:8]...)
	for _, s := range secs {
		out = binary.AppendUvarint(append(out, s.id), uint64(len(s.body)))
		out = append(out, s.body...)
	}
	return out, nil
}
