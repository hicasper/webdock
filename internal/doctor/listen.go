package doctor

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

type Listener struct {
	IP   net.IP
	Port int
}

type Rule struct {
	Name          string
	Required      bool
	Forbidden     bool
	AllowWildcard bool
	AllowedIPs    []string
}

func ReadListeners() ([]Listener, error) {
	var listeners []Listener
	for _, item := range []struct {
		path string
		ipv6 bool
	}{
		{path: "/proc/net/tcp"},
		{path: "/proc/net/tcp6", ipv6: true},
	} {
		file, err := os.Open(item.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("open %s: %w", item.path, err)
		}
		parsed, err := ParseProcNet(file, item.ipv6)
		_ = file.Close()
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", item.path, err)
		}
		listeners = append(listeners, parsed...)
	}
	return listeners, nil
}

func ParseProcNet(r io.Reader, ipv6 bool) ([]Listener, error) {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return nil, nil
	}
	var result []Listener
	for lineNo := 2; scanner.Scan(); lineNo++ {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			return nil, fmt.Errorf("line %d has %d fields", lineNo, len(fields))
		}
		if fields[3] != "0A" {
			continue
		}
		ip, port, err := parseProcNetAddress(fields[1], ipv6)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		result = append(result, Listener{IP: ip, Port: port})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func parseProcNetAddress(value string, ipv6 bool) (net.IP, int, error) {
	host, portHex, ok := strings.Cut(value, ":")
	if !ok || host == "" || portHex == "" {
		return nil, 0, fmt.Errorf("invalid local address %q", value)
	}
	port, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid port %q", portHex)
	}
	raw, err := hexBytes(host)
	if err != nil {
		return nil, 0, err
	}
	if ipv6 {
		if len(raw) != net.IPv6len {
			return nil, 0, fmt.Errorf("invalid IPv6 address %q", host)
		}
		for offset := 0; offset < len(raw); offset += 4 {
			reverse(raw[offset : offset+4])
		}
		return net.IP(raw), int(port), nil
	}
	if len(raw) != net.IPv4len {
		return nil, 0, fmt.Errorf("invalid IPv4 address %q", host)
	}
	reverse(raw)
	return net.IP(raw), int(port), nil
}

func hexBytes(value string) ([]byte, error) {
	if len(value)%2 != 0 {
		return nil, fmt.Errorf("invalid hex value %q", value)
	}
	result := make([]byte, len(value)/2)
	for i := range result {
		parsed, err := strconv.ParseUint(value[i*2:i*2+2], 16, 8)
		if err != nil {
			return nil, fmt.Errorf("invalid hex value %q", value)
		}
		result[i] = byte(parsed)
	}
	return result, nil
}

func reverse(value []byte) {
	for i, j := 0, len(value)-1; i < j; i, j = i+1, j-1 {
		value[i], value[j] = value[j], value[i]
	}
}

func ValidateListeners(listeners []Listener, rules map[int]Rule) []string {
	byPort := make(map[int][]net.IP)
	for _, listener := range listeners {
		byPort[listener.Port] = append(byPort[listener.Port], listener.IP)
	}
	var problems []string
	for port, rule := range rules {
		addresses := byPort[port]
		if rule.Required && len(addresses) == 0 {
			problems = append(problems, fmt.Sprintf("%s is not listening on port %d", rule.Name, port))
		}
		if rule.Forbidden && len(addresses) != 0 {
			problems = append(problems, fmt.Sprintf("%s must not listen on port %d", rule.Name, port))
			continue
		}
		for _, ip := range addresses {
			if listenerAllowed(ip, rule) {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s has unexpected listener %s:%d", rule.Name, ip.String(), port))
		}
	}
	return problems
}

func listenerAllowed(ip net.IP, rule Rule) bool {
	if ip == nil {
		return false
	}
	if rule.AllowWildcard && ip.IsUnspecified() {
		return true
	}
	if ip.IsLoopback() {
		return true
	}
	for _, allowed := range rule.AllowedIPs {
		if allowedIP := net.ParseIP(allowed); allowedIP != nil && allowedIP.Equal(ip) {
			return true
		}
	}
	return false
}

func PortConflicts(listeners []Listener, ports []int) []string {
	wanted := make(map[int]bool, len(ports))
	for _, port := range ports {
		wanted[port] = true
	}
	seen := make(map[int]bool)
	var problems []string
	for _, listener := range listeners {
		if wanted[listener.Port] && !seen[listener.Port] {
			seen[listener.Port] = true
			problems = append(problems, fmt.Sprintf("port %d is already in use while Webdock is stopped", listener.Port))
		}
	}
	return problems
}
