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

//go:wasmimport seedfall fail
func hostFail(p unsafe.Pointer, n uint32)

const (
	codeAgain   = -1
	codeRefused = -4
	codeInUse   = -5
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

func Listen(network, address string) (net.Listener, error) {
	id := hostString(hostListen, address)
	if id < 0 {
		return nil, &net.OpError{Op: "listen", Net: network, Err: codeErr(id)}
	}
	f, err := open(id)
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(address)
	return &listener{f: f, id: id, addr: addr(net.JoinHostPort(Node(), port))}, nil
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
	return &conn{f: f, local: addr(Node()), remote: addr(address)}, nil
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
		return &conn{f: f, local: l.addr, remote: addr("peer")}, nil
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

func (c *conn) Close() error                       { return c.f.Close() }
func (c *conn) LocalAddr() net.Addr                { return c.local }
func (c *conn) RemoteAddr() net.Addr               { return c.remote }
func (c *conn) SetDeadline(t time.Time) error      { return c.f.SetDeadline(t) }
func (c *conn) SetReadDeadline(t time.Time) error  { return c.f.SetReadDeadline(t) }
func (c *conn) SetWriteDeadline(t time.Time) error { return c.f.SetWriteDeadline(t) }

//go:wasmimport seedfall history_call
func hostHistoryCall(p unsafe.Pointer, n uint32) int32

//go:wasmimport seedfall history_return
func hostHistoryReturn(id int32, p unsafe.Pointer, n uint32, outcome int32)

func call(client int, kind, key, value string) Op {
	b, _ := json.Marshal(map[string]any{"client": client, "kind": kind, "key": key, "value": value})
	return Op{id: hostHistoryCall(bytesPtr(b), uint32(len(b)))}
}

func (o Op) Ok(value string) {
	b := []byte(value)
	hostHistoryReturn(o.id, bytesPtr(b), uint32(len(b)), 1)
}

func (o Op) Unknown() { hostHistoryReturn(o.id, nil, 0, 0) }

func (o Op) Failed() { hostHistoryReturn(o.id, nil, 0, 2) }
