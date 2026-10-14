package mobilebridge

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// virtualNamePrefixes are interfaces that are not the machine's own physical
// networking: VPNs, container and VM bridges, and Apple's link-local radios.
var virtualNamePrefixes = []string{"utun", "tun", "tap", "docker", "bridge", "vmnet", "llw", "awdl"}

// hasVirtualName reports whether the interface name looks virtual. Split out of
// skipInterface so MachineFingerprint can reuse the list without inheriting
// skipInterface's link-state check — a fingerprint must not change when Wi-Fi
// is switched off.
func hasVirtualName(name string) bool {
	n := strings.ToLower(name)
	for _, bad := range virtualNamePrefixes {
		if strings.HasPrefix(n, bad) {
			return true
		}
	}
	return false
}

func skipInterface(i net.Interface) bool {
	if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
		return true
	}
	return hasVirtualName(i.Name)
}

// tailscaleCGNAT is the 100.64.0.0/10 range Tailscale assigns to nodes. It is
// deliberately NOT covered by net.IP.IsPrivate (which is RFC1918 only), which
// is why PrivateIPv4Candidates never returns a Tailscale address.
var tailscaleCGNAT = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// isTunnelInterface reports whether the interface is an up, non-loopback tunnel
// device of the kind Tailscale binds to. Deliberately NOT expressed via
// skipInterface, which drops utun*/tun* — exactly where Tailscale lives.
func isTunnelInterface(i net.Interface) bool {
	if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
		return false
	}
	n := strings.ToLower(i.Name)
	return strings.HasPrefix(n, "utun") || strings.HasPrefix(n, "tun") || strings.HasPrefix(n, "tailscale")
}

// ipv4Candidates walks ifaces, keeping the IPv4 addresses of interfaces that
// satisfy keepIface whose IPs satisfy keepIP. addrsOf is injected so callers
// (and tests) can supply the per-interface address lookup.
func ipv4Candidates(
	ifaces []net.Interface,
	addrsOf func(net.Interface) ([]net.Addr, error),
	keepIface func(net.Interface) bool,
	keepIP func(net.IP) bool,
) []string {
	var out []string
	for _, i := range ifaces {
		if !keepIface(i) {
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
			if keepIP(ip4) {
				out = append(out, ip4.String())
			}
		}
	}
	return out
}

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

// PrivateIPv4Candidates returns the private IPv4 addresses of the given
// interfaces, skipping down/loopback/virtual interfaces (see skipInterface) and
// non-private, loopback, or link-local addresses. addrsOf is injected so callers
// (and tests) can supply the per-interface address lookup.
func PrivateIPv4Candidates(ifaces []net.Interface, addrsOf func(net.Interface) ([]net.Addr, error)) []string {
	return ipv4Candidates(ifaces, addrsOf,
		func(i net.Interface) bool { return !skipInterface(i) },
		func(ip net.IP) bool { return ip.IsPrivate() },
	)
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

// TailscaleIPv4Candidates returns the Tailscale IPv4 addresses (100.64.0.0/10
// on a tunnel interface) of the given interfaces. Both filters are required:
// the range check is the real discriminator, since a machine may have several
// utun* interfaces and only Tailscale's carries a 100.x; the interface check
// keeps a genuinely carrier-NAT'd Ethernet interface from being mistaken for
// Tailscale.
func TailscaleIPv4Candidates(ifaces []net.Interface, addrsOf func(net.Interface) ([]net.Addr, error)) []string {
	return ipv4Candidates(ifaces, addrsOf, isTunnelInterface, tailscaleCGNAT.Contains)
}

// AutopickTailscaleIP returns this machine's Tailscale IPv4 address, or "" when
// Tailscale is not installed, not running, or logged out. Best-effort, and the
// caller must treat "" as "no Tailscale address to advertise" rather than an error.
func AutopickTailscaleIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	c := TailscaleIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
	if len(c) == 0 {
		return ""
	}
	return c[0]
}

// LocalPrivateIPv4s returns every private IPv4 address of this machine's
// suitable interfaces. Unlike AutopickLANIP it keeps them all: the phone races
// every advertised endpoint, so a machine on both Wi-Fi and Ethernet must
// advertise both.
func LocalPrivateIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return PrivateIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
}

// LocalTailscaleIPv4s returns every Tailscale IPv4 address of this machine, or
// nil when Tailscale is not installed, not running, or logged out.
func LocalTailscaleIPv4s() []string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	return TailscaleIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return i.Addrs()
	})
}
