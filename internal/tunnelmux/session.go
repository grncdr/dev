package tunnelmux

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

const (
	frameOpen  byte = 1
	frameData  byte = 2
	frameClose byte = 3
)

var errSessionClosed = errors.New("tunnel session is closed")

type Session struct {
	conn net.Conn

	writeMu sync.Mutex

	mu      sync.Mutex
	streams map[uint32]*stream
	nextID  uint32
	closed  bool
	err     error

	acceptCh chan *stream
	done     chan struct{}
}

func NewSession(conn net.Conn) *Session {
	s := &Session{
		conn:     conn,
		streams:  map[uint32]*stream{},
		nextID:   1,
		acceptCh: make(chan *stream, 128),
		done:     make(chan struct{}),
	}
	go s.readLoop()
	return s
}

func (s *Session) Done() <-chan struct{} {
	return s.done
}

func (s *Session) Close() error {
	s.fail(io.EOF)
	return nil
}

func (s *Session) OpenStream(ctx context.Context) (net.Conn, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	st, err := s.newStream()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		_ = st.Close()
		return nil, ctx.Err()
	default:
	}
	if err := s.writeFrame(frameOpen, st.id, nil); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

func (s *Session) AcceptStream() (net.Conn, error) {
	select {
	case st := <-s.acceptCh:
		if st == nil {
			return nil, io.EOF
		}
		return st, nil
	case <-s.done:
		return nil, s.sessionErr()
	}
}

func (s *Session) newStream() (*stream, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, s.sessionErr()
	}
	id := s.nextID
	s.nextID++
	st := newStream(s, id)
	s.streams[id] = st
	return st, nil
}

func (s *Session) readLoop() {
	var hdr [9]byte
	for {
		if _, err := io.ReadFull(s.conn, hdr[:]); err != nil {
			s.fail(err)
			return
		}
		typ := hdr[0]
		id := binary.BigEndian.Uint32(hdr[1:5])
		n := binary.BigEndian.Uint32(hdr[5:9])
		payload := make([]byte, n)
		if n > 0 {
			if _, err := io.ReadFull(s.conn, payload); err != nil {
				s.fail(err)
				return
			}
		}
		if err := s.handleFrame(typ, id, payload); err != nil {
			s.fail(err)
			return
		}
	}
}

func (s *Session) handleFrame(typ byte, id uint32, payload []byte) error {
	switch typ {
	case frameOpen:
		st, ok := s.getStream(id)
		if ok {
			return st.setRemoteOpen()
		}
		st = newStream(s, id)
		st.remoteOpen = true
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return s.sessionErr()
		}
		s.streams[id] = st
		s.mu.Unlock()
		select {
		case s.acceptCh <- st:
		default:
			return fmt.Errorf("tunnel stream backlog exceeded")
		}
		return nil
	case frameData:
		st, ok := s.getStream(id)
		if !ok {
			return fmt.Errorf("unknown stream %d", id)
		}
		return st.append(payload)
	case frameClose:
		st, ok := s.getStream(id)
		if !ok {
			return nil
		}
		st.closeRemote()
		s.dropStreamIfDone(st)
		return nil
	default:
		return fmt.Errorf("unknown frame type %d", typ)
	}
}

func (s *Session) getStream(id uint32) (*stream, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[id]
	return st, ok
}

func (s *Session) writeData(id uint32, p []byte) (int, error) {
	if err := s.writeFrame(frameData, id, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *Session) writeClose(id uint32) error {
	return s.writeFrame(frameClose, id, nil)
}

func (s *Session) writeFrame(typ byte, id uint32, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	select {
	case <-s.done:
		return s.sessionErr()
	default:
	}
	var hdr [9]byte
	hdr[0] = typ
	binary.BigEndian.PutUint32(hdr[1:5], id)
	binary.BigEndian.PutUint32(hdr[5:9], uint32(len(payload)))
	if _, err := s.conn.Write(hdr[:]); err != nil {
		s.fail(err)
		return err
	}
	if len(payload) > 0 {
		if _, err := s.conn.Write(payload); err != nil {
			s.fail(err)
			return err
		}
	}
	return nil
}

func (s *Session) dropStreamIfDone(st *stream) {
	if st == nil || !st.isDone() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.streams[st.id]
	if ok && current == st {
		delete(s.streams, st.id)
	}
}

func (s *Session) sessionErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	return errSessionClosed
}

func (s *Session) fail(err error) {
	if err == nil {
		err = io.EOF
	}
	if errors.Is(err, net.ErrClosed) {
		err = io.EOF
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.err = err
	streams := make([]*stream, 0, len(s.streams))
	for _, st := range s.streams {
		streams = append(streams, st)
	}
	s.streams = map[uint32]*stream{}
	s.mu.Unlock()
	_ = s.conn.Close()
	for _, st := range streams {
		st.setError(err)
	}
	close(s.done)
}

type stream struct {
	id   uint32
	sess *Session

	mu          sync.Mutex
	cond        *sync.Cond
	buf         bytes.Buffer
	err         error
	localClosed bool
	remoteOpen  bool
	remoteEOF   bool
}

func newStream(sess *Session, id uint32) *stream {
	st := &stream{id: id, sess: sess}
	st.cond = sync.NewCond(&st.mu)
	return st
}

func (s *stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		if s.buf.Len() > 0 {
			return s.buf.Read(p)
		}
		if s.err != nil {
			if errors.Is(s.err, io.EOF) {
				return 0, io.EOF
			}
			return 0, s.err
		}
		if s.remoteEOF {
			return 0, io.EOF
		}
		s.cond.Wait()
	}
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	closed := s.localClosed
	s.mu.Unlock()
	if closed {
		return 0, net.ErrClosed
	}
	return s.sess.writeData(s.id, p)
}

func (s *stream) Close() error {
	s.mu.Lock()
	if s.localClosed {
		s.mu.Unlock()
		return nil
	}
	s.localClosed = true
	s.mu.Unlock()
	if err := s.sess.writeClose(s.id); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	s.sess.dropStreamIfDone(s)
	return nil
}

func (s *stream) LocalAddr() net.Addr {
	return s.sess.conn.LocalAddr()
}

func (s *stream) RemoteAddr() net.Addr {
	return s.sess.conn.RemoteAddr()
}

func (s *stream) SetDeadline(_ time.Time) error {
	return nil
}

func (s *stream) SetReadDeadline(_ time.Time) error {
	return nil
}

func (s *stream) SetWriteDeadline(_ time.Time) error {
	return nil
}

func (s *stream) append(p []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteEOF {
		return io.EOF
	}
	if len(p) > 0 {
		_, _ = s.buf.Write(p)
	}
	s.cond.Broadcast()
	return nil
}

func (s *stream) setRemoteOpen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remoteOpen {
		return nil
	}
	s.remoteOpen = true
	return nil
}

func (s *stream) closeRemote() {
	s.mu.Lock()
	s.remoteEOF = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *stream) setError(err error) {
	s.mu.Lock()
	s.err = err
	s.cond.Broadcast()
	s.mu.Unlock()
}

func (s *stream) isDone() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.localClosed && s.remoteEOF
}
