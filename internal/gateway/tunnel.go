package gateway

import (
	"bufio"
	"net"
	"sync"
)

type tunnelConn struct {
	conn net.Conn
	br   *bufio.Reader
	bw   *bufio.Writer
}

type tunnelPool struct {
	mu    sync.Mutex
	pools map[string][]*tunnelConn
}

func newTunnelPool() *tunnelPool {
	return &tunnelPool{
		pools: map[string][]*tunnelConn{},
	}
}

func (p *tunnelPool) add(label string, conn net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pools[label] = append(p.pools[label], &tunnelConn{
		conn: conn,
		br:   bufio.NewReader(conn),
		bw:   bufio.NewWriter(conn),
	})
}

func (p *tunnelPool) acquire(label string) *tunnelConn {
	p.mu.Lock()
	defer p.mu.Unlock()
	list := p.pools[label]
	if len(list) == 0 {
		return nil
	}
	tc := list[len(list)-1]
	p.pools[label] = list[:len(list)-1]
	return tc
}

func (p *tunnelPool) release(label string, tc *tunnelConn) {
	if tc == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pools[label] = append(p.pools[label], tc)
}

func (p *tunnelPool) has(label string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pools[label]) > 0
}
