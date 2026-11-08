//go:build wasip1

package sim

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
	"time"
	"unsafe"
)

//go:wasmimport seedfall net_listen
func hostListen(addr unsafe.Pointer, n uint32) int32

//go:wasmimport seedfall net_accept
func hostAccept(lid int32) int32

//go:wasmimport seedfall net_dial
func hostDial(addr unsafe.Pointer, n uint32) int32

//go:wasmimport seedfall net_bind
func hostBind(fd, id int32)

//go:wasmimport seedfall net_addr
func hostAddr(fd, peer int32, p unsafe.Pointer, n uint32) int32

//go:wasmimport seedfall net_udp
func hostUDP(addr unsafe.Pointer, n uint32) int32

//go:wasmimport seedfall net_sendto
func hostSendTo(fd int32, ap unsafe.Pointer, an uint32, dp unsafe.Pointer, dn uint32) int32

//go:wasmimport seedfall net_recvfrom
func hostRecvFrom(fd int32, dp unsafe.Pointer, dn uint32, ap unsafe.Pointer, acap uint32, alen unsafe.Pointer) int32

//go:wasmimport seedfall fail
func hostFail(p unsafe.Pointer, n uint32)

const (
	codeAgain   = -1
	codeRefused = -4
	codeInUse   = -5
	addrMax     = 256
)

func bytesPtr(b []byte) unsafe.Pointer {
	if len(b) == 0 {
		return nil
	}
	return unsafe.Pointer(&b[0])
}

func hostString(fn func(unsafe.Pointer, uint32) int32, s string) int32 {
	b := []byte(s)
	return fn(bytesPtr(b), uint32(len(b)))
}

//go:wasmimport seedfall setup_complete
func setupComplete()

//go:wasmimport seedfall quiet
func hostQuiet(ns int64)

func quietFor(d time.Duration) { hostQuiet(int64(d)) }

//go:wasmimport seedfall budget
func hostBudget(n int64) int64

func budget(n int) int { return int(hostBudget(int64(n))) }

//go:wasmimport seedfall buggify
func hostBuggify(site unsafe.Pointer, n uint32, p float64) int32

func buggify(site string, p float64) bool {
	b := []byte(site)
	return hostBuggify(bytesPtr(b), uint32(len(b)), p) != 0
}

//go:wasmimport seedfall property
func hostProperty(k, cond int32, msg unsafe.Pointer, msgLen uint32, detail unsafe.Pointer, detailLen uint32)

//go:wasmimport seedfall numeric
func hostNumeric(k, cond int32, margin float64, msg unsafe.Pointer, msgLen uint32, detail unsafe.Pointer, detailLen uint32)

//go:wasmimport seedfall event
func hostEvent(name unsafe.Pointer, nameLen uint32, detail unsafe.Pointer, detailLen uint32)

func boolInt(cond bool) int32 {
	if cond {
		return 1
	}
	return 0
}

func property(k kind, cond bool, msg, detail string) {
	m, d := []byte(msg), []byte(detail)
	hostProperty(int32(k), boolInt(cond), bytesPtr(m), uint32(len(m)), bytesPtr(d), uint32(len(d)))
}

func numeric(k kind, cond bool, margin float64, msg, detail string) {
	m, d := []byte(msg), []byte(detail)
	hostNumeric(int32(k), boolInt(cond), margin, bytesPtr(m), uint32(len(m)), bytesPtr(d), uint32(len(d)))
}

func event(name, detail string) {
	n, d := []byte(name), []byte(detail)
	hostEvent(bytesPtr(n), uint32(len(n)), bytesPtr(d), uint32(len(d)))
}

func Fail(msg string) {
	b := []byte(msg)
	hostFail(bytesPtr(b), uint32(len(b)))
	select {}
}

func codeErr(c int32) error {
	switch c {
	case codeRefused:
		return syscall.ECONNREFUSED
	case codeInUse:
		return syscall.EADDRINUSE
	}
	return syscall.EIO
}

func open(id int32) (*os.File, error) {
	f, err := os.OpenFile("/.seedfall/conn/"+strconv.Itoa(int(id)), os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	raw, err := f.SyscallConn()
	if err != nil {
		f.Close()
		return nil, err
	}
	raw.Control(func(fd uintptr) { hostBind(int32(fd), id) })
	return f, nil
}

type addr string

func (a addr) Network() string { return "sim" }
func (a addr) String() string  { return string(a) }

func endpointAddr(f *os.File, peer int32) addr {
	raw, err := f.SyscallConn()
	if err != nil {
		return ""
	}
	var buf [addrMax]byte
	var n int32
	raw.Control(func(fd uintptr) { n = hostAddr(int32(fd), peer, unsafe.Pointer(&buf[0]), addrMax) })
	if n <= 0 {
		return ""
	}
	return addr(buf[:n])
}

func Listen(network, address string) (net.Listener, error) {
	id := hostString(hostListen, address)
	if id < 0 {
		return nil, &net.OpError{Op: "listen", Net: network, Err: codeErr(id)}
	}
	f, err := open(id)
	if err != nil {
		return nil, err
	}
	return &listener{f: f, id: id, addr: endpointAddr(f, 0)}, nil
}

func ListenPacket(network, address string) (net.PacketConn, error) {
	id := hostString(hostUDP, address)
	if id < 0 {
		return nil, &net.OpError{Op: "listen", Net: network, Err: codeErr(id)}
	}
	f, err := open(id)
	if err != nil {
		return nil, err
	}
	return &packetConn{f: f, local: endpointAddr(f, 0)}, nil
}

func Dial(network, address string) (net.Conn, error) {
	id := hostString(hostDial, address)
	if id < 0 {
		return nil, &net.OpError{Op: "dial", Net: network, Addr: addr(address), Err: codeErr(id)}
	}
	f, err := open(id)
	if err != nil {
		return nil, err
	}
	return &conn{f: f, local: endpointAddr(f, 0), remote: endpointAddr(f, 1)}, nil
}

type listener struct {
	f    *os.File
	id   int32
	addr addr
}

func (l *listener) Accept() (net.Conn, error) {
	var ready [1]byte
	for {
		if _, err := l.f.Read(ready[:]); err != nil {
			return nil, closedErr(err)
		}
		id := hostAccept(l.id)
		if id == codeAgain {
			continue
		}
		if id < 0 {
			return nil, codeErr(id)
		}
		f, err := open(id)
		if err != nil {
			return nil, err
		}
		return &conn{f: f, local: l.addr, remote: endpointAddr(f, 1)}, nil
	}
}

func (l *listener) Close() error   { return l.f.Close() }
func (l *listener) Addr() net.Addr { return l.addr }

type conn struct {
	f             *os.File
	local, remote addr
}

func closedErr(err error) error {
	if errors.Is(err, os.ErrClosed) {
		return net.ErrClosed
	}
	if errors.Is(err, syscall.EIO) {
		return syscall.ECONNRESET
	}
	return err
}

func (c *conn) Read(p []byte) (int, error) {
	n, err := c.f.Read(p)
	return n, closedErr(err)
}

func (c *conn) Write(p []byte) (int, error) {
	n, err := c.f.Write(p)
	return n, closedErr(err)
}

func (c *conn) shutdown(how int) error {
	raw, err := c.f.SyscallConn()
	if err != nil {
		return err
	}
	var failed error
	if err := raw.Control(func(fd uintptr) { failed = syscall.Shutdown(int(fd), how) }); err != nil {
		return closedErr(err)
	}
	return failed
}

func (c *conn) CloseRead() error                   { return c.shutdown(syscall.SHUT_RD) }
func (c *conn) CloseWrite() error                  { return c.shutdown(syscall.SHUT_WR) }
func (c *conn) Close() error                       { return c.f.Close() }
func (c *conn) LocalAddr() net.Addr                { return c.local }
func (c *conn) RemoteAddr() net.Addr               { return c.remote }
func (c *conn) SetDeadline(t time.Time) error      { return c.f.SetDeadline(t) }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.f.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.f.SetWriteDeadline(t) }

type packetConn struct {
	f     *os.File
	local addr
}

func (c *packetConn) ReadFrom(p []byte) (int, net.Addr, error) {
	raw, err := c.f.SyscallConn()
	if err != nil {
		return 0, nil, err
	}
	var from [addrMax]byte
	var n int32
	var alen uint32
	err = raw.Read(func(fd uintptr) bool {
		n = hostRecvFrom(int32(fd), bytesPtr(p), uint32(len(p)), unsafe.Pointer(&from[0]), addrMax, unsafe.Pointer(&alen))
		return n != codeAgain
	})
	if err != nil {
		return 0, nil, closedErr(err)
	}
	if n < 0 {
		return 0, nil, codeErr(n)
	}
	return int(n), addr(from[:alen]), nil
}

func (c *packetConn) WriteTo(p []byte, to net.Addr) (int, error) {
	if to == nil {
		return 0, &net.OpError{Op: "write", Net: "udp", Err: syscall.EINVAL}
	}
	raw, err := c.f.SyscallConn()
	if err != nil {
		return 0, err
	}
	dest := []byte(to.String())
	var n int32
	err = raw.Write(func(fd uintptr) bool {
		n = hostSendTo(int32(fd), bytesPtr(dest), uint32(len(dest)), bytesPtr(p), uint32(len(p)))
		return true
	})
	if err != nil {
		return 0, closedErr(err)
	}
	if n < 0 {
		return 0, codeErr(n)
	}
	return int(n), nil
}

func (c *packetConn) Close() error                       { return c.f.Close() }
func (c *packetConn) LocalAddr() net.Addr                { return c.local }
func (c *packetConn) SetDeadline(t time.Time) error      { return c.f.SetDeadline(t) }
func (c *packetConn) SetReadDeadline(t time.Time) error  { return c.f.SetReadDeadline(t) }
func (c *packetConn) SetWriteDeadline(t time.Time) error { return c.f.SetWriteDeadline(t) }

//go:wasmimport seedfall history_call
func hostHistoryCall(p unsafe.Pointer, n uint32) int32

//go:wasmimport seedfall history_return
func hostHistoryReturn(id int32, p unsafe.Pointer, n uint32, outcome int32)

type Op struct{ id int32 }

func call(client int, f, key string, value any) Op {
	b, _ := json.Marshal(map[string]any{"client": client, "f": f, "key": key, "value": value})
	return Op{id: hostHistoryCall(bytesPtr(b), uint32(len(b)))}
}

func (o Op) Ok(output any) {
	b, _ := json.Marshal(output)
	hostHistoryReturn(o.id, bytesPtr(b), uint32(len(b)), 1)
}

func (o Op) Unknown() { hostHistoryReturn(o.id, nil, 0, 0) }

func (o Op) Failed() { hostHistoryReturn(o.id, nil, 0, 2) }
