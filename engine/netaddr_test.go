package engine

import (
	"strings"
	"testing"
)

func TestListenOnPortZeroBindsAnEphemeralPort(t *testing.T) {
	f := connected(t, Range{ms(1), ms(1)})
	id := f.w.net.listen(f.server, ":0")
	if id < 0 {
		t.Fatalf("listen on port 0: %d", id)
	}
	addr, ok := f.w.net.addr(f.server, id, false)
	if !ok || addr == "server:0" || !strings.HasPrefix(addr, "server:") {
		t.Fatalf("bound address = %q, %v, want an allocated port on server", addr, ok)
	}
	other := f.w.net.listen(f.server, ":0")
	if second, _ := f.w.net.addr(f.server, other, false); second == addr {
		t.Fatalf("two port 0 listeners share %s", addr)
	}
	if conn := f.w.net.dial(f.client, addr); conn < 0 {
		t.Fatalf("dial the bound address: %d", conn)
	}
}

func TestAcceptedConnectionKnowsTheDialerAddress(t *testing.T) {
	f := connected(t, Range{ms(1), ms(1)})
	local, _ := f.w.net.addr(f.client, f.conn, false)
	remote, _ := f.w.net.addr(f.server, f.peer, true)
	if local != remote || !strings.HasPrefix(local, "client:") {
		t.Fatalf("dialer is %q and the server sees %q, want the same client address", local, remote)
	}
	if got, _ := f.w.net.addr(f.client, f.conn, true); got != "server:7000" {
		t.Fatalf("dialer's remote = %q, want server:7000", got)
	}
}

func TestCloseWriteDeliversEOFAndKeepsTheOtherDirection(t *testing.T) {
	f := connected(t, Range{ms(1), ms(1)})
	f.w.net.write(f.client, f.conn, []byte("req"))
	if code := f.w.net.shutdown(f.client, f.conn, shutWrite); code != 0 {
		t.Fatalf("shutdown: %d", code)
	}
	if got := f.w.net.write(f.client, f.conn, []byte("late")); got != codePipe {
		t.Fatalf("write after CloseWrite = %d, want codePipe", got)
	}
	f.w.now += int64(ms(5))
	buf := make([]byte, 8)
	if got := f.w.net.read(f.server, f.peer, buf); string(buf[:max(got, 0)]) != "req" {
		t.Fatalf("server read = %d, want the data before EOF", got)
	}
	if got := f.w.net.read(f.server, f.peer, buf); got != codeEOF {
		t.Fatalf("server read after data = %d, want codeEOF", got)
	}
	if got := f.w.net.write(f.server, f.peer, []byte("resp")); got != 4 {
		t.Fatalf("server write after the client's CloseWrite = %d, want 4", got)
	}
	f.w.now += int64(ms(5))
	if got := f.w.net.read(f.client, f.conn, buf); string(buf[:max(got, 0)]) != "resp" {
		t.Fatalf("client read = %d, want resp", got)
	}
	f.w.net.close(f.client, f.conn)
	if n := len(f.w.net.endpoint(f.server, f.peer).inbox); n != 1 {
		t.Fatalf("server inbox after close = %d chunks, want only the first EOF", n)
	}
}

func TestCloseReadReturnsEOF(t *testing.T) {
	f := connected(t, Range{ms(1), ms(1)})
	f.w.net.write(f.client, f.conn, []byte("data"))
	f.w.net.shutdown(f.server, f.peer, shutRead)
	if !f.w.net.readable(f.peer) {
		t.Fatal("a read-closed connection should be readable")
	}
	if got := f.w.net.read(f.server, f.peer, make([]byte, 8)); got != codeEOF {
		t.Fatalf("read = %d, want codeEOF", got)
	}
}
