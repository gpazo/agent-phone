package phone

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"
)

func TestBlockedNativeSocketCancels(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	defer client.Close()
	b := &Bridge{conn: client}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := b.Tree(ctx); done <- err }()
	header := make([]byte, 4)
	if _, err := io.ReadFull(peer, header); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, binary.BigEndian.Uint32(header))
	if _, err := io.ReadFull(peer, data); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("socket read ignored cancellation")
	}
	if _, err := peer.Write([]byte{0}); err == nil {
		t.Fatal("cancelled socket remained usable")
	}
}
func TestBlockedNativePipeCancels(t *testing.T) {
	inputRead, inputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer inputRead.Close()
	defer inputWrite.Close()
	outputRead, outputWrite, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outputRead.Close()
	defer outputWrite.Close()
	b := &Bridge{input: inputWrite, output: bufio.NewReader(outputRead), actionOutput: outputRead}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := b.Action(ctx, map[string]any{"action": "capture-targets"}); done <- err }()
	if _, err := bufio.NewReader(inputRead).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("pipe read ignored cancellation")
	}
}
func TestSuccessfulNativeStreamSurvivesOldContextCancellation(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	b := &Bridge{conn: client}
	peerDone := make(chan error, 1)
	go func() {
		for range 2 {
			header := make([]byte, 4)
			if _, err := io.ReadFull(peer, header); err != nil {
				peerDone <- err
				return
			}
			data := make([]byte, binary.BigEndian.Uint32(header))
			if _, err := io.ReadFull(peer, data); err != nil {
				peerDone <- err
				return
			}
			reply := []byte(`{"ok":true,"tree":{}}`)
			binary.BigEndian.PutUint32(header, uint32(len(reply)))
			if _, err := peer.Write(append(header, reply...)); err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- nil
	}()
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := b.Tree(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := b.Tree(context.Background()); err != nil {
		t.Fatalf("old callback damaged next call: %v", err)
	}
	if err := <-peerDone; err != nil {
		t.Fatal(err)
	}
}

type delayedCloseConn struct {
	net.Conn
	entered chan struct{}
	release chan struct{}
}

func (c *delayedCloseConn) Close() error {
	err := c.Conn.Close()
	close(c.entered)
	<-c.release
	return err
}
func TestNativeCancellationCallbackFinishesBeforeOperationReturns(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	conn := &delayedCloseConn{Conn: client, entered: make(chan struct{}), release: make(chan struct{})}
	b := &Bridge{conn: conn}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := b.Tree(ctx); done <- err }()
	header := make([]byte, 4)
	if _, err := io.ReadFull(peer, header); err != nil {
		t.Fatal(err)
	}
	cancel()
	<-conn.entered
	select {
	case <-done:
		t.Fatal("ownership released before cancellation callback finished")
	case <-time.After(10 * time.Millisecond):
	}
	close(conn.release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("callback did not complete")
	}
}
