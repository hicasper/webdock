package network

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strings"

	"webdock/internal/docker"
)

type LocalIPProvider func() ([]net.IP, error)

type networkInspect struct {
	IPAM struct {
		Config []struct {
			Gateway string
		} `json:"Config"`
	} `json:"IPAM"`
}

func Discover(ctx context.Context, runner docker.Runner, names []string, localIPs LocalIPProvider) ([]string, []string) {
	if len(names) == 0 {
		return nil, nil
	}
	if localIPs == nil {
		localIPs = interfaceIPv4s
	}
	ips, err := localIPs()
	if err != nil {
		return nil, []string{fmt.Sprintf("cannot verify local Docker gateway addresses: %v", err)}
	}
	local := make(map[string]bool, len(ips))
	for _, ip := range ips {
		if ip4 := ip.To4(); ip4 != nil {
			local[ip4.String()] = true
		}
	}

	seen := make(map[string]bool)
	var gateways []string
	var warnings []string
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out, err := runner.Output(ctx, "", nil, "docker", "network", "inspect", name)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("Docker network %q is unavailable: %v", name, err))
			continue
		}
		var inspected []networkInspect
		if err := json.Unmarshal(out, &inspected); err != nil {
			warnings = append(warnings, fmt.Sprintf("Docker network %q returned invalid JSON: %v", name, err))
			continue
		}
		for _, item := range inspected {
			for _, cfg := range item.IPAM.Config {
				gateway := strings.TrimSpace(cfg.Gateway)
				ip := net.ParseIP(gateway)
				if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsMulticast() {
					warnings = append(warnings, fmt.Sprintf("ignored invalid Docker gateway %q from network %q", gateway, name))
					continue
				}
				if !local[gateway] {
					warnings = append(warnings, fmt.Sprintf("ignored Docker gateway %q because it is not assigned locally", gateway))
					continue
				}
				if !seen[gateway] {
					seen[gateway] = true
					gateways = append(gateways, gateway)
				}
			}
		}
	}
	sort.Slice(gateways, func(i, j int) bool {
		return bytesLess(net.ParseIP(gateways[i]).To4(), net.ParseIP(gateways[j]).To4())
	})
	return gateways, warnings
}

func interfaceIPv4s() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	result := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		var ip net.IP
		switch value := addr.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip4 := ip.To4(); ip4 != nil {
			result = append(result, ip4)
		}
	}
	return result, nil
}

func bytesLess(a, b net.IP) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
