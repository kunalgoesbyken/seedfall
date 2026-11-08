//go:build !wasip1

package sim

import (
	"net"
	"time"
)

func Listen(network, address string) (net.Listener, error) { return net.Listen(network, address) }

func ListenPacket(network, address string) (net.PacketConn, error) {
	return net.ListenPacket(network, address)
}

func Dial(network, address string) (net.Conn, error) { return net.Dial(network, address) }

func Fail(msg string) { panic("seedfall: " + msg) }

func property(k kind, cond bool, msg, detail string) {
	if ((k == always || k == alwaysOrUnreachable || k == eventually) && !cond) || k == unreachable {
		Fail(msg + ": " + detail)
	}
}

func numeric(k kind, cond bool, _ float64, msg, detail string) { property(k, cond, msg, detail) }

func event(string, string) {}

func setupComplete() {}

func buggify(string, float64) bool { return false }

func quietFor(time.Duration) {}

func budget(n int) int { return n }

type Op struct{}

func call(int, string, string, any) Op { return Op{} }

func (o Op) Ok(any) {}

func (o Op) Unknown() {}

func (o Op) Failed() {}
