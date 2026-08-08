package mobilebridge

import (
	"net"
	"testing"
)

func TestPrivateIPv4Candidates(t *testing.T) {
	ifaces := []net.Interface{
		{Index: 1, Name: "lo0", Flags: net.FlagUp | net.FlagLoopback},
		{Index: 2, Name: "en0", Flags: net.FlagUp},
		{Index: 3, Name: "utun3", Flags: net.FlagUp}, // VPN — skip
		{Index: 4, Name: "en5", Flags: 0},            // down — skip
	}
	addrs := map[string][]net.Addr{
		"lo0":   {cidr("127.0.0.1/8")},
		"en0":   {cidr("192.168.1.42/24"), cidr("fe80::1/64")},
		"utun3": {cidr("10.9.9.9/24")},
		"en5":   {cidr("192.168.5.5/24")},
	}
	got := PrivateIPv4Candidates(ifaces, func(i net.Interface) ([]net.Addr, error) {
		return addrs[i.Name], nil
	})
	if len(got) != 1 || got[0] != "192.168.1.42" {
		t.Fatalf("got %v want [192.168.1.42]", got)
	}
}

func cidr(s string) net.Addr {
	ip, ipnet, _ := net.ParseCIDR(s)
	ipnet.IP = ip
	return ipnet
}

// BindAddress is what narrows the LAN listener. "all" must stay 0.0.0.0 (the
// unchanged default) and anything unresolvable must be an error, never a silent
// widening back to every interface.
func TestBindAddress(t *testing.T) {
	for _, tc := range []struct {
		mode, want string
		wantErr    bool
	}{
		{mode: "", want: "0.0.0.0"},
		{mode: "all", want: "0.0.0.0"},
		{mode: "192.168.1.7", want: "192.168.1.7"},
		{mode: "127.0.0.1", want: "127.0.0.1"},
		{mode: "::1", want: "::1"},
		{mode: "not-an-ip", wantErr: true},
		{mode: "tailscale0", wantErr: true}, // the interface name, not the mode
	} {
		got, err := BindAddress(tc.mode)
		if (err != nil) != tc.wantErr {
			t.Errorf("BindAddress(%q) err = %v, wantErr %v", tc.mode, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("BindAddress(%q) = %q, want %q", tc.mode, got, tc.want)
		}
	}
}

// Tailscale is detected by its 100.64.0.0/10 address, not by interface name:
// the interface is utun* on macOS (which skipInterface deliberately skips for
// LAN autopick), tailscale0 on Linux, and something else again on Windows.
func TestTailscaleIPv4(t *testing.T) {
	utun := net.Interface{Index: 1, Name: "utun4", Flags: net.FlagUp}
	en0 := net.Interface{Index: 2, Name: "en0", Flags: net.FlagUp}
	down := net.Interface{Index: 3, Name: "utun9", Flags: 0}
	addrs := map[string][]net.Addr{
		"en0":   {&net.IPNet{IP: net.ParseIP("192.168.1.20"), Mask: net.CIDRMask(24, 32)}},
		"utun4": {&net.IPNet{IP: net.ParseIP("100.101.102.103"), Mask: net.CIDRMask(32, 32)}},
		"utun9": {&net.IPNet{IP: net.ParseIP("100.64.0.9"), Mask: net.CIDRMask(32, 32)}},
	}
	of := func(i net.Interface) ([]net.Addr, error) { return addrs[i.Name], nil }

	if got := TailscaleIPv4([]net.Interface{en0, utun}, of); got != "100.101.102.103" {
		t.Errorf("TailscaleIPv4 = %q, want the CGNAT address", got)
	}
	if got := TailscaleIPv4([]net.Interface{en0}, of); got != "" {
		t.Errorf("no tailnet: got %q, want \"\"", got)
	}
	// 100.64.0.0/10 stops at 100.127.255.255 — 100.128.x is ordinary public space.
	public := net.Interface{Index: 4, Name: "eth0", Flags: net.FlagUp}
	addrs["eth0"] = []net.Addr{&net.IPNet{IP: net.ParseIP("100.128.0.1"), Mask: net.CIDRMask(24, 32)}}
	if got := TailscaleIPv4([]net.Interface{public}, of); got != "" {
		t.Errorf("100.128.0.1 is outside CGNAT: got %q, want \"\"", got)
	}
	if got := TailscaleIPv4([]net.Interface{down}, of); got != "" {
		t.Errorf("a down interface must be skipped: got %q", got)
	}
}
