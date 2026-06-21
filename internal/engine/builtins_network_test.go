// builtins_network_test.go tests the pure parsing and validation helpers behind
// the network-diagnostics builtins against synthetic command output — no live
// route/ifconfig/scutil/arp/lsof/ping/dig calls. The centrepiece is
// TestValidateNetworkHost, the mandatory option-injection regression for the two
// operations (ping_host, dns_lookup) that take a model-controlled host: a
// flag-like or punctuated value must be rejected as data, never reach a
// subprocess as a flag.
package engine

import (
	"context"
	"os"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// TestNetworkBuiltins_Live exercises the read-only builtins end-to-end against
// the real machine. It is skipped unless MCP_NETWORK_LIVE=1, since its output
// depends on the host's actual network state and it shells out to real
// binaries — mirroring the screenshot domain's live-gated test.
func TestNetworkBuiltins_Live(t *testing.T) {
	if os.Getenv("MCP_NETWORK_LIVE") != "1" {
		t.Skip("set MCP_NETWORK_LIVE=1 to run the live network builtins")
	}
	ctx := context.Background()
	cap := registry.Capability{}

	if out, err := runCurrentNetwork(ctx, cap, nil); err != nil {
		t.Errorf("runCurrentNetwork: %v", err)
	} else {
		t.Logf("current_network:\n%s", out)
	}
	if out, err := runDNSServers(ctx, cap, nil); err != nil {
		t.Errorf("runDNSServers: %v", err)
	} else {
		t.Logf("dns_servers:\n%s", out)
	}
	if out, err := runPingHost(ctx, cap, map[string]any{"host": "127.0.0.1", "count": 1}); err != nil {
		t.Errorf("runPingHost: %v", err)
	} else if !strings.Contains(out, "reachable") {
		t.Errorf("ping of loopback should be reachable, got: %s", out)
	}
}

func TestValidateNetworkHost(t *testing.T) {
	accept := []string{
		"example.com",
		"router.local",
		"8.8.8.8",
		"17.253.144.10",
		"2606:4700:4700::1111",
		"::1", // IPv6 loopback — bare IPv6 literals keep their colons
		"a",   // single-label hosts are legal
	}
	for _, h := range accept {
		if err := validateNetworkHost(h); err != nil {
			t.Errorf("validateNetworkHost(%q) = %v, want accepted", h, err)
		}
	}

	reject := []string{
		"",                       // empty
		"-e",                     // looks like an osascript-style flag
		"-c100",                  // looks like ping's count flag
		"--flood",                // looks like a long flag
		"@evil.example",          // dig reads '@' as an alternate DNS server
		"+short",                 // dig reads '+' as a query option
		"a b",                    // space (would split into two operands)
		"a/b",                    // slash / path metacharacter
		";rm -rf",                // shell metacharacters
		"host\nname",             // embedded newline
		"apple.com:443",          // host:port — ':' is only valid inside an IPv6 literal
		"8.8.8.8:53",             // IP:port — also rejected (not a bare host)
		strings.Repeat("a", 254), // over the length cap
	}
	for _, h := range reject {
		if err := validateNetworkHost(h); err == nil {
			t.Errorf("validateNetworkHost(%q) = nil, want rejected", h)
		}
	}
}

func TestParseDefaultRoute(t *testing.T) {
	out := "   route to: default\ndestination: default\n       mask: default\n    gateway: 192.168.1.1\n  interface: en0\n      flags: <UP,GATEWAY>\n"
	iface, gw := parseDefaultRoute(out)
	if iface != "en0" {
		t.Errorf("interface = %q, want en0", iface)
	}
	if gw != "192.168.1.1" {
		t.Errorf("gateway = %q, want 192.168.1.1", gw)
	}
	if iface, gw := parseDefaultRoute("no route here"); iface != "" || gw != "" {
		t.Errorf("empty input should yield empty fields, got %q/%q", iface, gw)
	}
}

func TestParseIfconfig(t *testing.T) {
	out := strings.Join([]string{
		"en0: flags=8863<UP,BROADCAST,RUNNING> mtu 1500",
		"\tether d6:6c:20:9f:0e:c2",
		"\tinet6 fe80::1%en0 prefixlen 64",
		"\tinet 192.168.1.23 netmask 0xffffff00 broadcast 192.168.1.255",
	}, "\n")
	ip, mask, mac := parseIfconfig(out)
	if ip != "192.168.1.23" {
		t.Errorf("ip = %q, want 192.168.1.23", ip)
	}
	if mask != "0xffffff00" {
		t.Errorf("mask = %q, want 0xffffff00", mask)
	}
	if mac != "d6:6c:20:9f:0e:c2" {
		t.Errorf("mac = %q, want d6:6c:20:9f:0e:c2", mac)
	}
}

func TestHostCapacityFromMask(t *testing.T) {
	cases := []struct {
		mask       string
		wantPrefix int
		wantUsable int
		wantOK     bool
	}{
		{"0xffffff00", 24, 254, true},    // hex /24
		{"255.255.255.0", 24, 254, true}, // dotted /24
		{"0xffff0000", 16, 65534, true},  // /16
		{"0xfffffffc", 30, 2, true},      // /30
		{"0xffffffff", 32, 0, true},      // /32 — no usable range
		{"not-a-mask", 0, 0, false},      // unparseable
	}
	for _, c := range cases {
		prefix, usable, ok := hostCapacityFromMask(c.mask)
		if ok != c.wantOK || prefix != c.wantPrefix || usable != c.wantUsable {
			t.Errorf("hostCapacityFromMask(%q) = (%d,%d,%v), want (%d,%d,%v)",
				c.mask, prefix, usable, ok, c.wantPrefix, c.wantUsable, c.wantOK)
		}
	}
}

func TestDottedMask(t *testing.T) {
	if got := dottedMask("0xffffff00"); got != "255.255.255.0" {
		t.Errorf("dottedMask(0xffffff00) = %q, want 255.255.255.0", got)
	}
	if got := dottedMask("255.255.0.0"); got != "255.255.0.0" {
		t.Errorf("dottedMask(255.255.0.0) = %q, want unchanged", got)
	}
}

func TestParseScutilDNS(t *testing.T) {
	out := strings.Join([]string{
		"resolver #1",
		"  nameserver[0] : 2001:558:feed::1",
		"  nameserver[1] : 192.168.1.1",
		"resolver #2",
		"  nameserver[0] : 2001:558:feed::1", // duplicate across scopes
		"  nameserver[1] : 8.8.8.8",
	}, "\n")
	got := parseScutilDNS(out)
	want := []string{"2001:558:feed::1", "192.168.1.1", "8.8.8.8"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("parseScutilDNS = %v, want %v (deduped, first-seen order)", got, want)
	}
}

func TestParseArp(t *testing.T) {
	out := strings.Join([]string{
		"? (192.168.1.1) at 28:94:1:f5:87:6a on en0 ifscope [ethernet]", // single-digit octet
		"? (192.168.1.4) at b0:f2:f6:28:8a:3a on en0 ifscope [ethernet]",
		"? (192.168.1.8) at (incomplete) on en0 ifscope [ethernet]",     // must be skipped
		"? (192.168.1.1) at 28:94:1:f5:87:6a on en0 ifscope [ethernet]", // duplicate IP
	}, "\n")
	got := parseArp(out)
	if len(got) != 2 {
		t.Fatalf("parseArp returned %d entries, want 2 (incomplete + duplicate dropped): %+v", len(got), got)
	}
	if got[0].ip != "192.168.1.1" || got[0].mac != "28:94:1:f5:87:6a" {
		t.Errorf("first entry = %+v, want 192.168.1.1 / 28:94:1:f5:87:6a", got[0])
	}
}

func TestParseLsofListeners(t *testing.T) {
	// TCP rows carry a trailing "(LISTEN)", so the address is NOT the last
	// field — the parser must take the token right after the TCP/UDP column.
	tcp := strings.Join([]string{
		"COMMAND     PID  USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME",
		"ControlCe   583 jerry    9u  IPv4 0x3ddc6913e65842bb      0t0  TCP *:7000 (LISTEN)",
		"ControlCe   583 jerry   10u  IPv6 0x363415a324c2b785      0t0  TCP *:7000 (LISTEN)", // dup port, same pid
		"node        900 jerry   20u  IPv4 0x111                   0t0  TCP 127.0.0.1:3000 (LISTEN)",
	}, "\n")
	got := parseLsofListeners(tcp, "tcp")
	if len(got) != 2 {
		t.Fatalf("parseLsofListeners(tcp) returned %d, want 2 (7000 deduped): %+v", len(got), got)
	}
	ports := map[int]string{}
	for _, l := range got {
		ports[l.port] = l.command
	}
	if ports[7000] != "ControlCe" || ports[3000] != "node" {
		t.Errorf("parsed ports/commands = %v, want 7000=ControlCe, 3000=node", ports)
	}

	udp := "COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME\nmDNSResp 100 _mdns 5u IPv4 0xabc 0t0 UDP *:5353\n"
	u := parseLsofListeners(udp, "udp")
	if len(u) != 1 || u[0].port != 5353 || u[0].proto != "udp" {
		t.Errorf("parseLsofListeners(udp) = %+v, want one udp/5353", u)
	}
}

func TestSubnetHosts(t *testing.T) {
	// /24 around .23 → 254 candidate addresses, minus our own → 253.
	hosts, err := subnetHosts("192.168.1.23", "0xffffff00")
	if err != nil {
		t.Fatalf("subnetHosts(/24): %v", err)
	}
	if len(hosts) != 253 {
		t.Errorf("/24 host count = %d, want 253 (254 usable minus our own address)", len(hosts))
	}
	for _, h := range hosts {
		if h == "192.168.1.23" {
			t.Error("subnetHosts must exclude this Mac's own address")
		}
		if h == "192.168.1.0" || h == "192.168.1.255" {
			t.Errorf("subnetHosts must exclude network/broadcast, found %s", h)
		}
	}

	// A /16 is larger than the scanner supports and must be refused.
	if _, err := subnetHosts("10.0.0.5", "0xffff0000"); err == nil {
		t.Error("subnetHosts(/16) = nil error, want refusal for an over-large subnet")
	}
}
