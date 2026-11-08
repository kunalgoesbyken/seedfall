package engine

import "encoding/binary"

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

	opEnd       = 0x0b
	opIf        = 0x04
	opCall      = 0x10
	opDrop      = 0x1a
	opGlobalGet = 0x23
	opGlobalSet = 0x24
	opI32Const  = 0x41
	opI32Eqz    = 0x45
	opI32Sub    = 0x6b
	typeI32     = 0x7f
	blockEmpty  = 0x40
	mutable     = 0x01
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

func prependToBodies(code, prologue []byte) ([]byte, bool) {
	r := &wasmReader{b: code, ok: true}
	count := r.uleb()
	out := binary.AppendUvarint(nil, count)
	for range count {
		fn := &wasmReader{b: r.bytes(r.uleb()), ok: r.ok}
		for range fn.uleb() {
			fn.uleb()
			fn.byte()
		}
		locals := fn.b[:min(fn.pos, len(fn.b))]
		body := append(append(append([]byte(nil), locals...), prologue...), fn.rest()...)
		if !fn.ok {
			return nil, false
		}
		out = append(binary.AppendUvarint(out, uint64(len(body))), body...)
	}
	return out, r.ok && r.pos == len(code)
}

func instrument(wasm []byte) []byte {
	secs, ok := readSections(wasm)
	imports, globals, exports, code := section(secs, secImport), section(secs, secGlobal), section(secs, secExport), section(secs, secCode)
	if !ok || !builtByGo(secs) || imports == nil || globals == nil || exports == nil || code == nil {
		return wasm
	}
	yield, ok := importedFunc(imports.body, "wasi_snapshot_preview1", "sched_yield")
	if !ok {
		return wasm
	}
	globalBody, calls, ok := appendEntries(globals.body, []byte{typeI32, mutable, opI32Const, 0, opEnd})
	if !ok {
		return wasm
	}
	exportBody, _, ok := appendEntries(exports.body, exportEntry(callsExport, kindGlobal, calls), exportEntry(gExport, kindGlobal, goGlobalG))
	if !ok {
		return wasm
	}
	codeBody, ok := prependToBodies(code.body, countdown(calls, yield))
	if !ok {
		return wasm
	}
	globals.body, exports.body, code.body = globalBody, exportBody, codeBody
	out := append([]byte(nil), wasm[:8]...)
	for _, s := range secs {
		out = binary.AppendUvarint(append(out, s.id), uint64(len(s.body)))
		out = append(out, s.body...)
	}
	return out
}
