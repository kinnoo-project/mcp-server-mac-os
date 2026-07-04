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
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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
	if out, err := runTraceRoute(ctx, cap, map[string]any{"host": "127.0.0.1", "max_hops": 1}); err != nil {
		t.Errorf("runTraceRoute: %v", err)
	} else {
		t.Logf("trace_route:\n%s", out)
	}
	if out, err := runRouteTable(ctx, cap, nil); err != nil {
		t.Errorf("runRouteTable: %v", err)
	} else if !strings.Contains(out, "routing table") {
		t.Errorf("route_table should label the tables, got: %s", out)
	}
	if out, err := runInterfaceStats(ctx, cap, nil); err != nil {
		t.Errorf("runInterfaceStats: %v", err)
	} else {
		t.Logf("interface_stats:\n%s", out)
	}
	if out, err := runDNSCacheLookup(ctx, cap, map[string]any{"host": "localhost"}); err != nil {
		t.Errorf("runDNSCacheLookup: %v", err)
	} else {
		t.Logf("dns_cache_lookup:\n%s", out)
	}
}

// TestNetworkDiagnostics_RejectHostileHost is the per-operation option-injection
// regression required by CLAUDE.md §4 for the three V1 read builtins that take a
// model-controlled host. Each funnels its host through validateNetworkHost
// BEFORE resolving or running any binary, so every hostile value (a
// flag-lookalike or a metacharacter-laden string) must come back as an error
// with no subprocess ever launched. Because the guard runs first, this also
// proves the "-e lands as data, never a flag" property for these operations.
func TestNetworkDiagnostics_RejectHostileHost(t *testing.T) {
	ctx := context.Background()
	cap := registry.Capability{}
	fns := map[string]struct {
		fn    BuiltinFunc
		param string
	}{
		"trace_route":      {runTraceRoute, "host"},
		"whois_lookup":     {runWhoisLookup, "domain"},
		"dns_cache_lookup": {runDNSCacheLookup, "host"},
	}
	for name, spec := range fns {
		for _, h := range hostileValues {
			if _, err := spec.fn(ctx, cap, map[string]any{spec.param: h}); err == nil {
				t.Errorf("%s with hostile host %q = nil error, want rejection before any subprocess", name, h)
			}
		}
	}
}

// TestClampMaxHops pins the traceroute hop-limit bounds: out-of-range values are
// clamped into 1–30 rather than passed through to a run that could hang or be
// useless.
func TestClampMaxHops(t *testing.T) {
	cases := map[int]int{-5: 1, 0: 1, 1: 1, 15: 15, 30: 30, 31: 30, 1000: 30}
	for in, want := range cases {
		if got := clampMaxHops(in); got != want {
			t.Errorf("clampMaxHops(%d) = %d, want %d", in, got, want)
		}
	}
}

// TestTraceRouteArgs pins traceroute's argument vector: the speed-limiting flags
// are present and fixed, the hop count is rendered from the (already-clamped)
// value, and the host is the final operand (no "--" before it, since traceroute
// has none — the allowlist is the guard).
func TestTraceRouteArgs(t *testing.T) {
	got := traceRouteArgs("8.8.8.8", 12)
	want := []string{"-w", "2", "-q", "1", "-m", "12", "8.8.8.8"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("traceRouteArgs = %v, want %v", got, want)
	}
	if got[len(got)-1] != "8.8.8.8" {
		t.Errorf("host must be the final operand, got argv %v", got)
	}
}

// TestClassifyTraceResult pins the subtle precedence in how a traceroute run is
// rendered — specifically that a fired per-trace deadline is reported as data
// even though the runner ALSO returns an error for it, so a bounded timeout can
// never leak out as a hard failure (the Copilot-reported ordering bug).
func TestClassifyTraceResult(t *testing.T) {
	timeout := 40 * time.Second
	sentinel := errors.New("timed out after 2m0s") // what the runner returns on a killed child

	// Deadline fired, parent not cancelled, partial hops present: the runErr is
	// IGNORED and a friendly message with the partial route is returned.
	out, err := classifyTraceResult("8.8.8.8", timeout,
		&runResult{Stdout: " 1  router (192.168.1.1)  1.2 ms\n"}, sentinel, false, true)
	if err != nil {
		t.Fatalf("timeout case returned error %v, want nil (must be reported as data)", err)
	}
	if !strings.Contains(out, "did not complete within") || !strings.Contains(out, "Partial route") || !strings.Contains(out, "router") {
		t.Errorf("timeout case text = %q, want a timeout message with the partial route", out)
	}

	// Deadline fired but no partial output: still a message, still no error.
	out, err = classifyTraceResult("8.8.8.8", timeout, &runResult{}, sentinel, false, true)
	if err != nil || strings.Contains(out, "Partial route") {
		t.Errorf("empty-timeout case = (%q, %v), want a message with no partial section and nil error", out, err)
	}

	// A genuine caller cancellation (parent cancelled) is NOT massaged into a
	// timeout message — it surfaces the real error.
	if _, err := classifyTraceResult("8.8.8.8", timeout, &runResult{}, sentinel, true, true); err == nil {
		t.Error("caller-cancelled case = nil error, want the real error surfaced")
	}

	// A non-timeout error with no deadline surfaces as an error.
	if _, err := classifyTraceResult("8.8.8.8", timeout, &runResult{}, errors.New("boom"), false, false); err == nil {
		t.Error("plain-error case = nil error, want the error surfaced")
	}

	// Clean success: the route is rendered.
	out, err = classifyTraceResult("8.8.8.8", timeout,
		&runResult{Stdout: " 1  gw  1 ms\n 2  8.8.8.8  9 ms\n"}, nil, false, false)
	if err != nil || !strings.Contains(out, "Route to 8.8.8.8") || !strings.Contains(out, "8.8.8.8") {
		t.Errorf("success case = (%q, %v), want a rendered route", out, err)
	}

	// Success with no stdout falls back to the no-information message.
	out, _ = classifyTraceResult("8.8.8.8", timeout, &runResult{}, nil, false, false)
	if !strings.Contains(out, "No route information") {
		t.Errorf("empty-success case = %q, want the no-information message", out)
	}
}

// TestStageFlushDNSCache pins the one mutating network op: it flushes via a fixed
// dscacheutil argv, carries no inverse (a flushed cache cannot be restored), and
// its preview describes the effect and the mDNSResponder scope caveat without
// pre-empting the auto-commit path's own "cannot be undone" note.
func TestStageFlushDNSCache(t *testing.T) {
	plan, err := stageFlushDNSCache(context.Background(), registry.Capability{}, nil)
	if err != nil {
		t.Fatalf("stageFlushDNSCache: %v", err)
	}
	if plan.Forward.Binary != "dscacheutil" {
		t.Errorf("forward binary = %q, want dscacheutil", plan.Forward.Binary)
	}
	if strings.Join(plan.Forward.Args, " ") != "-flushcache" {
		t.Errorf("forward args = %v, want [-flushcache]", plan.Forward.Args)
	}
	if plan.Inverse != nil {
		t.Errorf("flush_dns_cache must be irreversible (nil inverse), got %+v", *plan.Inverse)
	}
	if !strings.Contains(plan.Preview, "cache") {
		t.Errorf("preview should describe the flush, got %q", plan.Preview)
	}
	if strings.Contains(plan.Preview, "cannot be undone") {
		t.Errorf("preview should NOT include the auto-commit suffix itself, got %q", plan.Preview)
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
