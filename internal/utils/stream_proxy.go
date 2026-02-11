package utils

import (
	"errors"
	"io"
	"net"
	"sync"
)

func ProxyBidirectional(a io.ReadWriteCloser, aRead io.Reader, b io.ReadWriteCloser, bRead io.Reader) error {
	if aRead == nil {
		aRead = a
	}
	if bRead == nil {
		bRead = b
	}

	errCh := make(chan error, 2)
	var once sync.Once
	closeBoth := func() {
		_ = a.Close()
		_ = b.Close()
	}

	go func() {
		_, err := io.Copy(b, aRead)
		once.Do(closeBoth)
		errCh <- normalizeProxyErr(err)
	}()
	go func() {
		_, err := io.Copy(a, bRead)
		once.Do(closeBoth)
		errCh <- normalizeProxyErr(err)
	}()

	first := <-errCh
	second := <-errCh
	if first != nil {
		return first
	}
	return second
}

func normalizeProxyErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, io.EOF):
		return nil
	case errors.Is(err, net.ErrClosed):
		return nil
	default:
		return err
	}
}
