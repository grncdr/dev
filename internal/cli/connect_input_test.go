package cli

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestForwardAttachInputPassThrough(t *testing.T) {
	src := bytes.NewBufferString("hello")
	var dst bytes.Buffer

	if err := forwardAttachInput(&dst, src, 50*time.Millisecond); err != nil {
		t.Fatalf("forwardAttachInput returned error: %v", err)
	}
	if got, want := dst.String(), "hello"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestForwardAttachInputDetachSequence(t *testing.T) {
	src := bytes.NewReader([]byte{detachPrefixByte, detachTriggerByte})
	var dst bytes.Buffer

	err := forwardAttachInput(&dst, src, 50*time.Millisecond)
	if !errors.Is(err, errDetachRequested) {
		t.Fatalf("forwardAttachInput error = %v, want %v", err, errDetachRequested)
	}
	if got := dst.Len(); got != 0 {
		t.Fatalf("output length = %d, want 0", got)
	}
}

func TestForwardAttachInputNonDetachSequenceForwardsPrefix(t *testing.T) {
	src := bytes.NewReader([]byte{detachPrefixByte, 'x'})
	var dst bytes.Buffer

	if err := forwardAttachInput(&dst, src, 50*time.Millisecond); err != nil {
		t.Fatalf("forwardAttachInput returned error: %v", err)
	}
	if got, want := dst.String(), string([]byte{detachPrefixByte, 'x'}); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestForwardAttachInputTimeoutForwardsPrefix(t *testing.T) {
	reader, writer := io.Pipe()
	var dst bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- forwardAttachInput(&dst, reader, 20*time.Millisecond)
	}()

	if _, err := writer.Write([]byte{detachPrefixByte}); err != nil {
		t.Fatalf("write prefix: %v", err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := writer.Write([]byte{'z'}); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	if err := <-errCh; err != nil {
		t.Fatalf("forwardAttachInput returned error: %v", err)
	}
	if got, want := dst.String(), string([]byte{detachPrefixByte, 'z'}); got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

