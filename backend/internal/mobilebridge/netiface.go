package mobilebridge

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

func skipInterface(i net.Interface) bool {
	if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
		return true
	}
	n := strings.ToLower(i.Name)
	for _, bad := range []string{"utun", "tun", "tap", "docker", "bridge", "vmnet", "llw", "awdl"} {
		if strings.HasPrefix(n, bad) {
			return true
		}
	}
	return false
}

// PrivateIPv4Candidates returns the private IPv4 addresses of the given
// interfaces, skipping down/loopback/virtual interfaces (see skipInterface) and
// non-private, loopback, or link-local addresses. addrsOf is injected so callers
// (and tests) can supply the per-interface address lookup.
func PrivateIPv4Candidates(ifaces []net.Interface, addrsOf func(net.Interface) ([]net.Addr, error)) []string {
	var out []string
	for _, i := range ifaces {
		if skipInterface(i) {
			continue
		}
		addrs, err := addrsOf(i)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			ip4 := ip.To4()
			if ip4 == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
				continue
			}
			if ip4.IsPrivate() {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}

// tailscaleCGNAT is the 100.64.0.0/10 range Tailscale assigns to every node.
// Matching on the address, not the interface name, is what makes detection
// portable: the interface is utun* on macOS, tailscale0 on Linux, and a
// friendly-named adapter on Windows — and skipInterface deliberately skips
// utun* for LAN autopick.
var tailscaleCGNAT = net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// TailscaleIPv4 returns this node's Tailscale IPv4 among ifaces, or "" if
// Tailscale is not up. addrsOf is injected exactly as in PrivateIPv4Candidates.
func TailscaleIPv4(ifaces []net.Interface, addrsOf func(net.Interface) ([]net.Addr, error)) string {
	for _, i := range ifaces {
		if i.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, err := addrsOf(i)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip4 := ip.To4(); ip4 != nil && tailscaleCGNAT.Contains(ip4) {
				return ip4.String()
			}
		}
	}
	return ""
}

// BindAddress resolves a bind mode to the host the LAN listener should bind:
//
//	"" / "all"    every interface (0.0.0.0) — the default, unchanged behavior
//	"tailscale"   this node's Tailscale address only, so the bridge is reachable
//	              solely over the WireGuard-encrypted tailnet and not from the
//	              local network at all. No TLS work required for that transport.
//	<ip>          a literal address, for pinning one interface
//
// An unresolvable mode is an error rather than a silent fall back to 0.0.0.0:
// quietly widening exposure after the user asked to narrow it is the one
// failure mode worth being loud about.
func BindAddress(mode string) (string, error) {
	switch mode {
	case "", "all":
		return "0.0.0.0", nil
	case "tailscale":
		ifaces, err := net.Interfaces()
		if err != nil {
			return "", fmt.Errorf("list interfaces: %w", err)
		}
		ip := TailscaleIPv4(ifaces, func(i net.Interface) ([]net.Addr, error) { return i.Addrs() })
		if ip == "" {
			return "", errors.New("bind \"tailscale\": no Tailscale (100.64.0.0/10) address found — is Tailscale up?")
		}
		return ip, nil
	}
	if ip := net.ParseIP(mode); ip != nil {
		return ip.String(), nil
	}
	return "", fmt.Errorf("bind %q: want \"all\", \"tailscale\", or an IP address", mode)
}

// AutopickLANIP returns the first private IPv4 address of a suitable local
// interface, or "" if none is found. It is a best-effort convenience for
// surfacing the LAN address the phone should connect to.
func AutopickLANIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	c := PrivateIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
	if len(c) == 0 {
		return ""
	}
	return c[0]
}
