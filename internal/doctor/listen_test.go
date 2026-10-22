package doctor

import (
	"net"
	"strings"
	"testing"
)

func TestParseProcNetIPv4AndIPv6(t *testing.T) {
	ipv4 := strings.NewReader(`  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1
`)
	got, err := ParseProcNet(ipv4, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].IP.Equal(net.ParseIP("127.0.0.1")) || got[0].Port != 8080 {
		t.Fatalf("IPv4 listeners = %#v", got)
	}

	ipv6 := strings.NewReader(`  sl  local_address                         remote_address                        st
   0: 00000000000000000000000001000000:2328 00000000000000000000000000000000:0000 0A
`)
	got, err = ParseProcNet(ipv6, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].IP.Equal(net.ParseIP("::1")) || got[0].Port != 9000 {
		t.Fatalf("IPv6 listeners = %#v", got)
	}
}

func TestParseProcNetIgnoresNonListeningSockets(t *testing.T) {
	input := strings.NewReader(`  sl  local_address rem_address   st
   0: 0100007F:1F90 0100007F:D431 01 00000000:00000000 00:00000000 00000000     0        0 1
`)
	got, err := ParseProcNet(input, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("listeners = %#v, want none", got)
	}
}

func TestValidateListeners(t *testing.T) {
	rules := map[int]Rule{
		80:   {Name: "Nginx HTTP", Required: true, AllowWildcard: true},
		6379: {Name: "Valkey", Required: true, AllowedIPs: []string{"172.17.0.1"}},
		9001: {Name: "Nginx UI", Required: true},
	}
	listeners := []Listener{
		{IP: net.IPv4zero, Port: 80},
		{IP: net.ParseIP("127.0.0.1"), Port: 6379},
		{IP: net.ParseIP("172.17.0.1"), Port: 6379},
		{IP: net.ParseIP("127.0.0.1"), Port: 9001},
	}
	if problems := ValidateListeners(listeners, rules); len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
}

func TestValidateListenersRejectsWildcardPrivateService(t *testing.T) {
	rules := map[int]Rule{
		6379: {Name: "Valkey", Required: true, AllowedIPs: []string{"172.17.0.1"}},
	}
	listeners := []Listener{{IP: net.IPv4zero, Port: 6379}}
	problems := ValidateListeners(listeners, rules)
	if len(problems) != 1 || !strings.Contains(problems[0], "0.0.0.0:6379") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestValidateListenersRejectsForbiddenPort(t *testing.T) {
	rules := map[int]Rule{
		33060: {Name: "MySQL X Protocol", Forbidden: true},
	}
	listeners := []Listener{{IP: net.ParseIP("127.0.0.1"), Port: 33060}}
	problems := ValidateListeners(listeners, rules)
	if len(problems) != 1 || !strings.Contains(problems[0], "must not listen") {
		t.Fatalf("problems = %v", problems)
	}
}

func TestPortConflicts(t *testing.T) {
	listeners := []Listener{
		{IP: net.ParseIP("127.0.0.1"), Port: 5432},
		{IP: net.ParseIP("127.0.0.1"), Port: 5432},
		{IP: net.ParseIP("127.0.0.1"), Port: 1234},
	}
	problems := PortConflicts(listeners, []int{5432})
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want one unique conflict", problems)
	}
}
