package gateway

import (
	"sync"

	"dev/internal/tunnelmux"
)

type tunnelPool struct {
	mu    sync.Mutex
	pools map[string]*tunnelmux.Session
}

func newTunnelPool() *tunnelPool {
	return &tunnelPool{
		pools: map[string]*tunnelmux.Session{},
	}
}

func (p *tunnelPool) set(label string, sess *tunnelmux.Session) {
	p.mu.Lock()
	old := p.pools[label]
	p.pools[label] = sess
	p.mu.Unlock()

	if old != nil {
		_ = old.Close()
	}
	go func(expected *tunnelmux.Session) {
		<-expected.Done()
		p.mu.Lock()
		cur, ok := p.pools[label]
		if ok && cur == expected {
			delete(p.pools, label)
		}
		p.mu.Unlock()
	}(sess)
}

func (p *tunnelPool) get(label string) *tunnelmux.Session {
	p.mu.Lock()
	defer p.mu.Unlock()
	sess := p.pools[label]
	if sess == nil {
		return nil
	}
	select {
	case <-sess.Done():
		delete(p.pools, label)
		return nil
	default:
		return sess
	}
}

func (p *tunnelPool) has(label string) bool {
	return p.get(label) != nil
}
