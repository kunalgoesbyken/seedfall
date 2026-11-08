//go:build !wasip1

package sim

import (
	"net"
	"time"
)

func Listen(network, address string) (net.Listener, error) { return net.Listen(network, address) }

func Dial(network, address string) (net.Conn, error) { return net.Dial(network, address) }

func Fail(msg string) { panic("seedfall: " + msg) }

func property(k kind, cond bool, msg string, details []any) {
	if ((k == always || k == alwaysOrUnreachable || k == eventually) && !cond) || k == unreachable {
		Fail(msg + ": " + describe(details))
	}
}

func setupComplete() {}

func buggify(string, float64) bool { return false }

func quietFor(time.Duration) {}

type Op struct{}

func call(int, string, string, any) Op { return Op{} }

func (o Op) Ok(any) {}

func (o Op) Unknown() {}

func (o Op) Failed() {}
