package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	mdns "github.com/miekg/dns"
)

const (
	localDNSListenIP   = "127.0.0.1"
	localDNSListenPort = "15353"
	localDNSTTL        = 60
)

func (s *Server) startLocalDNS() error {
	if runningUnderGoTest() {
		return nil
	}
	mux := mdns.NewServeMux()
	mux.HandleFunc(".", s.handleLocalDNS)

	addr := net.JoinHostPort(localDNSListenIP, localDNSListenPort)
	udpConn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("local dns udp listen %s: %w", addr, err)
	}
	tcpListener, err := net.Listen("tcp", addr)
	if err != nil {
		_ = udpConn.Close()
		return fmt.Errorf("local dns tcp listen %s: %w", addr, err)
	}

	s.localDNSUDP = &mdns.Server{PacketConn: udpConn, Handler: mux}
	s.localDNSTCP = &mdns.Server{Listener: tcpListener, Handler: mux}

	go func() {
		if err := s.localDNSUDP.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
			logError(0, "local_dns_udp_failed", err)
		}
	}()
	go func() {
		if err := s.localDNSTCP.ActivateAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
			logError(0, "local_dns_tcp_failed", err)
		}
	}()

	return nil
}

func runningUnderGoTest() bool {
	return strings.HasSuffix(filepath.Base(os.Args[0]), ".test")
}

func (s *Server) stopLocalDNS() {
	if s.localDNSUDP != nil {
		_ = s.localDNSUDP.Shutdown()
		s.localDNSUDP = nil
	}
	if s.localDNSTCP != nil {
		_ = s.localDNSTCP.Shutdown()
		s.localDNSTCP = nil
	}
}

func (s *Server) handleLocalDNS(w mdns.ResponseWriter, req *mdns.Msg) {
	resp := new(mdns.Msg)
	resp.SetReply(req)
	resp.Authoritative = true

	matched := false
	for _, q := range req.Question {
		if !localDNSMatchesName(q.Name, s.projectApexZone()) {
			continue
		}
		matched = true
		resp.Answer = append(resp.Answer, localDNSAnswers(q)...)
	}

	if !matched {
		resp.Rcode = mdns.RcodeNameError
	}

	_ = w.WriteMsg(resp)
}

func localDNSMatchesName(name, apexZone string) bool {
	query := normalizeDNSName(name)
	if query == "" {
		return false
	}
	if query == "localhost" || strings.HasSuffix(query, ".localhost") {
		return true
	}
	apex := normalizeDNSName(apexZone)
	if apex == "" || apex == "localhost" {
		return false
	}
	if query == apex || strings.HasSuffix(query, "."+apex) {
		return true
	}
	return false
}

func normalizeDNSName(name string) string {
	trimmed := strings.TrimSpace(strings.ToLower(name))
	trimmed = strings.TrimPrefix(trimmed, ".")
	trimmed = strings.TrimSuffix(trimmed, ".")
	return trimmed
}

func localDNSAnswers(q mdns.Question) []mdns.RR {
	head := mdns.RR_Header{Name: q.Name, Class: mdns.ClassINET, Ttl: localDNSTTL}
	a := &mdns.A{Hdr: head, A: net.ParseIP("127.0.0.1")}
	aaaa := &mdns.AAAA{Hdr: head, AAAA: net.ParseIP("::1")}
	switch q.Qtype {
	case mdns.TypeA:
		a.Hdr.Rrtype = mdns.TypeA
		return []mdns.RR{a}
	case mdns.TypeAAAA:
		aaaa.Hdr.Rrtype = mdns.TypeAAAA
		return []mdns.RR{aaaa}
	case mdns.TypeANY:
		a.Hdr.Rrtype = mdns.TypeA
		aaaa.Hdr.Rrtype = mdns.TypeAAAA
		return []mdns.RR{a, aaaa}
	default:
		return nil
	}
}
