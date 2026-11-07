package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/kunalgoesbyken/seedfall/sim"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const stream = "EVENTS"

type ack struct {
	seq     uint64
	payload string
}

func main() {
	servers := flag.String("servers", "nats://n1:4222,nats://n2:4222,nats://n3:4222", "server URLs")
	replicas := flag.Int("replicas", 3, "stream replicas")
	count := flag.Int("messages", 300, "messages to publish")
	gap := flag.Duration("gap", 10*time.Millisecond, "pause between publishes")
	recovery := flag.Duration("recovery", 30*time.Second, "quiet period in which every acknowledged message must be readable again")
	flag.Parse()
	js := connect(*servers)
	until(func(ctx context.Context) error { return createStream(ctx, js, *replicas) })
	acked := publish(js, *count, *gap)
	fmt.Printf("%d of %d publishes acknowledged, verifying\n", len(acked), *count)
	sim.Sometimes(len(acked) < *count, "a fault interrupted publishing")
	sim.QuietFor(*recovery)
	unread := verifyWithin(js, acked, *recovery)
	sim.Always(unread == 0, "every acknowledged message is readable within the quiet period", unread)
	fmt.Println("every acknowledged message is stored at its sequence")
}

func connect(servers string) jetstream.JetStream {
	nc, err := nats.Connect(servers,
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

func createStream(ctx context.Context, js jetstream.JetStream, replicas int) error {
	_, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     stream,
		Subjects: []string{"events.>"},
		Storage:  jetstream.FileStorage,
		Replicas: replicas,
	})
	return err
}

func until(op func(context.Context) error) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
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

func verifyWithin(js jetstream.JetStream, acked []ack, d time.Duration) int {
	deadline := time.Now().Add(d)
	for len(acked) > 0 && time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		acked = verifyFrom(ctx, js, acked)
		cancel()
	}
	return len(acked)
}

func verifyFrom(ctx context.Context, js jetstream.JetStream, acked []ack) []ack {
	s, err := js.Stream(ctx, stream)
	if errors.Is(err, jetstream.ErrStreamNotFound) {
		sim.Fail(fmt.Sprintf("stream %s lost, %d acknowledged messages gone", stream, len(acked)))
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
