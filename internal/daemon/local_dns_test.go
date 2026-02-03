package daemon

import (
	"testing"

	mdns "github.com/miekg/dns"
)

func TestLocalDNSMatchesName(t *testing.T) {
	cases := []struct {
		name     string
		apex     string
		expected bool
	}{
		{"minio.foocorp.localhost.", ".localhost", true},
		{"localhost.", ".localhost", true},
		{"api.foo.dev.test.", ".dev.test", true},
		{"foo.example.com.", ".localhost", false},
	}
	for _, tc := range cases {
		if got := localDNSMatchesName(tc.name, tc.apex); got != tc.expected {
			t.Fatalf("name=%q apex=%q expected %t got %t", tc.name, tc.apex, tc.expected, got)
		}
	}
}

func TestLocalDNSAnswersTypes(t *testing.T) {
	q := mdns.Question{Name: "minio.foocorp.localhost.", Qclass: mdns.ClassINET}

	a := localDNSAnswers(mdns.Question{Name: q.Name, Qtype: mdns.TypeA, Qclass: q.Qclass})
	if len(a) != 1 {
		t.Fatalf("expected 1 A answer, got %d", len(a))
	}
	if rr, ok := a[0].(*mdns.A); !ok || rr.A.String() != "127.0.0.1" {
		t.Fatalf("unexpected A answer: %#v", a[0])
	}

	aaaa := localDNSAnswers(mdns.Question{Name: q.Name, Qtype: mdns.TypeAAAA, Qclass: q.Qclass})
	if len(aaaa) != 1 {
		t.Fatalf("expected 1 AAAA answer, got %d", len(aaaa))
	}
	if rr, ok := aaaa[0].(*mdns.AAAA); !ok || rr.AAAA.String() != "::1" {
		t.Fatalf("unexpected AAAA answer: %#v", aaaa[0])
	}

	any := localDNSAnswers(mdns.Question{Name: q.Name, Qtype: mdns.TypeANY, Qclass: q.Qclass})
	if len(any) != 2 {
		t.Fatalf("expected 2 ANY answers, got %d", len(any))
	}
}
