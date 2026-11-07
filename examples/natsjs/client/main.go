package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const stream = "EVENTS"

type simDialer struct{}

func (simDialer) Dial(network, address string) (net.Conn, error) { return sim.Dial(network, address) }

type ack struct {
	seq     uint64
	payload string
}

func main() {
	addr := flag.String("server", "nats://node:4222", "server URL")
	count := flag.Int("messages", 300, "messages to publish")
	gap := flag.Duration("gap", 10*time.Millisecond, "pause between publishes")
	flag.Parse()
	js := connect(*addr)
	until(func(ctx context.Context) error { return createStream(ctx, js) })
	acked := publish(js, *count, *gap)
	fmt.Printf("%d of %d publishes acknowledged, verifying\n", len(acked), *count)
	verify(js, acked)
	fmt.Println("every acknowledged message is stored at its sequence")
}

func connect(addr string) jetstream.JetStream {
	nc, err := nats.Connect(addr,
		nats.SetCustomDialer(simDialer{}),
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(50*time.Millisecond),
		nats.Timeout(time.Second),
	)
	if err != nil {
		log.Fatal(err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		log.Fatal(err)
	}
	return js
}

func createStream(ctx context.Context, js jetstream.JetStream) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     stream,
		Subjects: []string{"events.>"},
		Storage:  jetstream.FileStorage,
		Replicas: 1,
	})
	return err
}

func until(op func(context.Context) error) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err := op(ctx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func publish(js jetstream.JetStream, count int, gap time.Duration) []ack {
	var acked []ack
	for i := range count {
		payload := fmt.Sprintf("m%d", i)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		pa, err := js.Publish(ctx, "events.x", []byte(payload))
		cancel()
		if err == nil {
			acked = append(acked, ack{pa.Sequence, payload})
		}
		time.Sleep(gap)
	}
	return acked
}

func verify(js jetstream.JetStream, acked []ack) {
	for len(acked) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		acked = verifyFrom(ctx, js, acked)
		cancel()
	}
}

func verifyFrom(ctx context.Context, js jetstream.JetStream, acked []ack) []ack {
	s, err := js.Stream(ctx, stream)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		sim.Fail(fmt.Sprintf("stream %s lost after restart, %d acknowledged messages gone", stream, len(acked)))
	}
	if err != nil {
		return retryLater(acked)
	}
	for len(acked) > 0 {
		if err := check(ctx, s, acked[0]); err != nil {
			return retryLater(acked)
		}
		acked = acked[1:]
	}
	return acked
}

func check(ctx context.Context, s jetstream.Stream, a ack) error {
	msg, err := s.GetMsg(ctx, a.seq)
	if errors.Is(err, jetstream.ErrMsgNotFound) {
		sim.Fail(fmt.Sprintf("acknowledged message lost: %s at seq %d", a.payload, a.seq))
	}
	if err != nil {
		return err
	}
	sim.Assert(string(msg.Data) == a.payload, fmt.Sprintf("seq %d holds %q, but %s was acknowledged there", a.seq, msg.Data, a.payload))
	return nil
}

func retryLater(acked []ack) []ack {
	time.Sleep(100 * time.Millisecond)
	return acked
}
