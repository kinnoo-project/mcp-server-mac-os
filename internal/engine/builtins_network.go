// builtins_network.go implements the read-only network-diagnostics builtins:
// "where am I on the network" (current_network), DNS resolver listing
// (dns_servers), reachability and name-resolution probes (ping_host,
// dns_lookup), local listening sockets (listening_ports), and LAN device
// discovery both passively (lan_devices, from the ARP cache) and actively
// (scan_lan, a bounded ping sweep). Together they let the agent answer everyday
// connectivity questions and compose its own diagnoses ("can't reach the
// internet" → check the gateway, then a public IP, then DNS).
//
// Two of these operations — ping_host and dns_lookup — take a model-controlled
// host string into a subprocess. `dig` has no `--` end-of-options terminator and
// parses leading-dash / `@` / `+` arguments as its own options, so the defense
// here is a strict allowlist (validateNetworkHost), applied before any argv is
// assembled, per .claude/rules/darwin-execution.md §4. Every other operation
// either takes no input or only an enum, so it carries no injection surface.
package engine

import (
	"context"
	"fmt"
	"math/bits"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"mcp-server-mac-os/internal/policy"
	"mcp-server-mac-os/internal/registry"
)

// hostPattern is the allowlist a model-supplied host must match in full: ASCII
// letters, digits, dot, hyphen, and colon only. That covers hostnames, IPv4
// dotted-quads, and IPv6 colon-hex while excluding spaces, slashes, '@', '+',
// and every shell/dig metacharacter. A leading hyphen is rejected separately so
// the value can never be read as a command-line option, and a ':' that is not
// part of an IPv6 literal (i.e. a host:port pair) is rejected too — see
// validateNetworkHost.
var hostPattern = regexp.MustCompile(`^[A-Za-z0-9.:-]+$`)

// validateNetworkHost is the single input guardrail shared by ping_host and
// dns_lookup. It rejects empty, over-long, option-like, or punctuated values so
// the host can be handed to ping/dig strictly as data — never parsed as a flag,
// a dig query option ('+'), or an alternate DNS server ('@'). This is the
// security-critical seam covered by the accept/reject regression table in the
// tests.
func validateNetworkHost(host string) error {
	if host == "" {
		return fmt.Errorf("host must not be empty")
	}
	if len(host) > 253 {
		return fmt.Errorf("host %q is too long (max 253 characters)", host)
	}
	if host[0] == '-' {
		return fmt.Errorf("host %q must not begin with '-' (it would be read as a command-line option)", host)
	}
	if !hostPattern.MatchString(host) {
		return fmt.Errorf("host %q contains disallowed characters; only letters, digits, '.', '-', and ':' are permitted", host)
	}
	// A ':' is only legitimate as part of an IPv6 literal. In any other value it
	// means a host:port pair (e.g. "apple.com:443"), which ping/dig do NOT accept
	// — they would treat the whole string as a hostname and fail confusingly — and
	// it contradicts the documented "bare host" contract. So allow ':' only when
	// the value parses as an IP literal; reject host:port up front with a clear
	// message instead of letting it become a baffling resolution failure.
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return fmt.Errorf("host %q must be a bare host without a port; give just the hostname or IP (a ':' is only allowed inside an IPv6 address)", host)
	}
	return nil
}

// runCurrentNetwork reports this Mac's position on the local network. It first
// asks the routing table for the default route (which names the active
// interface and the gateway), then reads that interface's address, mask, and
// MAC from ifconfig, and finally derives how many hosts the subnet can hold.
func runCurrentNetwork(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	routeBin, err := policy.ResolveBinary("route")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, routeBin, "-n", "get", "default")
	if err != nil {
		return "", err
	}
	iface, gateway := parseDefaultRoute(res.Stdout)
	if iface == "" {
		return "No default route was found — this Mac does not currently have a usable network connection.", nil
	}

	ifBin, err := policy.ResolveBinary("ifconfig")
	if err != nil {
		return "", err
	}
	ifRes, err := runCommand(ctx, ifBin, iface)
	if err != nil {
		return "", err
	}
	ip, mask, mac := parseIfconfig(ifRes.Stdout)

	var b strings.Builder
	fmt.Fprintf(&b, "Active interface: %s\n", iface)
	if ip != "" {
		fmt.Fprintf(&b, "IPv4 address: %s\n", ip)
	} else {
		b.WriteString("IPv4 address: (none assigned)\n")
	}
	if mac != "" {
		fmt.Fprintf(&b, "MAC address: %s\n", mac)
	}
	if mask != "" {
		if prefix, usable, ok := hostCapacityFromMask(mask); ok {
			fmt.Fprintf(&b, "Subnet mask: /%d (%s) — up to %d usable host addresses\n", prefix, dottedMask(mask), usable)
		}
	}
	if gateway != "" {
		fmt.Fprintf(&b, "Default gateway (router): %s\n", gateway)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseDefaultRoute pulls the interface and gateway from `route -n get default`
// output, which prints one "key: value" pair per indented line. Either field
// may be empty when there is no default route (the Mac is offline).
func parseDefaultRoute(stdout string) (iface, gateway string) {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.SplitN(strings.TrimSpace(line), ":", 2)
		if len(fields) != 2 {
			continue
		}
		key := strings.TrimSpace(fields[0])
		val := strings.TrimSpace(fields[1])
		switch key {
		case "interface":
			iface = val
		case "gateway":
			gateway = val
		}
	}
	return iface, gateway
}

// parseIfconfig extracts the IPv4 address, netmask, and MAC (ether) address from
// `ifconfig <iface>` output. The netmask is returned exactly as ifconfig prints
// it (a hex word such as "0xffffff00"); hostCapacityFromMask and dottedMask
// interpret it. Missing fields come back empty rather than erroring.
func parseIfconfig(stdout string) (ip, mask, mac string) {
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			switch f {
			case "ether":
				if i+1 < len(fields) {
					mac = fields[i+1]
				}
			case "inet":
				if i+1 < len(fields) {
					ip = fields[i+1]
				}
			case "netmask":
				if i+1 < len(fields) {
					mask = fields[i+1]
				}
			}
		}
	}
	return ip, mask, mac
}

// parseMask converts a netmask in either ifconfig's hex form ("0xffffff00") or
// dotted-quad form ("255.255.255.0") into a 4-byte mask. ok is false when the
// value is unparseable.
func parseMask(mask string) (out [4]byte, ok bool) {
	if strings.HasPrefix(mask, "0x") || strings.HasPrefix(mask, "0X") {
		n, err := strconv.ParseUint(mask[2:], 16, 32)
		if err != nil {
			return out, false
		}
		out[0] = byte(n >> 24)
		out[1] = byte(n >> 16)
		out[2] = byte(n >> 8)
		out[3] = byte(n)
		return out, true
	}
	ip := net.ParseIP(mask).To4()
	if ip == nil {
		return out, false
	}
	copy(out[:], ip)
	return out, true
}

// hostCapacityFromMask returns the CIDR prefix length and the number of usable
// host addresses a subnet of the given mask can hold (network and broadcast
// addresses excluded). For example 0xffffff00 (/24) yields 254. ok is false for
// an unparseable mask.
func hostCapacityFromMask(mask string) (prefix, usable int, ok bool) {
	m, ok := parseMask(mask)
	if !ok {
		return 0, 0, false
	}
	asU32 := uint32(m[0])<<24 | uint32(m[1])<<16 | uint32(m[2])<<8 | uint32(m[3])
	prefix = bits.OnesCount32(asU32)
	zeros := 32 - prefix
	switch {
	case zeros >= 2:
		usable = (1 << zeros) - 2
	default:
		// /31 and /32 have no conventional usable-host range.
		usable = 0
	}
	return prefix, usable, true
}

// dottedMask renders a netmask in canonical dotted-quad form regardless of
// whether it arrived as hex or already dotted, so the report is human-readable.
func dottedMask(mask string) string {
	m, ok := parseMask(mask)
	if !ok {
		return mask
	}
	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}

// runDNSServers lists the resolver IP addresses currently in use, parsed from
// `scutil --dns`. Duplicates (scutil repeats servers across resolver scopes) are
// collapsed while preserving first-seen order.
func runDNSServers(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("scutil")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "--dns")
	if err != nil {
		return "", err
	}
	servers := parseScutilDNS(res.Stdout)
	if len(servers) == 0 {
		return "No DNS servers are currently configured.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d DNS server(s) in use:\n", len(servers))
	for _, s := range servers {
		fmt.Fprintf(&b, "  %s\n", s)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// parseScutilDNS collects the unique nameserver addresses from `scutil --dns`,
// whose relevant lines look like "  nameserver[0] : 192.168.1.1".
func parseScutilDNS(stdout string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "nameserver[") {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		addr := strings.TrimSpace(line[idx+1:])
		if addr != "" && !seen[addr] {
			seen[addr] = true
			out = append(out, addr)
		}
	}
	return out
}

// runPingHost validates the host, bounds the packet count, and runs ping,
// returning its already-concise summary. A non-zero exit (no reply) is reported
// as data — the model should see "unreachable", not a transport error.
func runPingHost(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	host, _ := getString(in, "host")
	if err := validateNetworkHost(host); err != nil {
		return "", err
	}
	count := 3
	if n, ok := getInt(in, "count"); ok {
		count = n
	}
	if count < 1 {
		count = 1
	}
	if count > 10 {
		count = 10
	}

	bin, err := policy.ResolveBinary("ping")
	if err != nil {
		return "", err
	}
	// Bound the whole probe with a context deadline rather than a ping flag.
	// ping's own -c (packet count) and -W (per-reply wait, in milliseconds on
	// macOS) give the normal-case bound and let it print its statistics summary;
	// the context timeout is the backstop for a true hang — e.g. a slow name
	// resolution, which no ping flag covers — and it ties cancellation to the
	// request. The host is validated above and placed after "--" as defence in
	// depth.
	timeout := time.Duration(count+5) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := runCommand(runCtx, bin, "-c", strconv.Itoa(count), "-W", "2000", "--", host)
	if err != nil {
		return "", err
	}
	// The backstop fired (and it wasn't the caller cancelling the request):
	// report the timeout as data rather than ping's signal-killed output.
	if ctx.Err() == nil && runCtx.Err() != nil {
		return fmt.Sprintf("Host %s did not respond within %s — treating it as unreachable.", host, timeout), nil
	}
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		out = strings.TrimSpace(res.Stderr)
	}
	reach := "reachable"
	if res.ExitCode != 0 {
		reach = "NOT reachable (no reply)"
	}
	return fmt.Sprintf("Host %s is %s.\n\n%s", host, reach, out), nil
}

// runDNSLookup resolves a hostname forward (A/AAAA) or an IP address in reverse
// (PTR) via dig's short output. The host is validated first; because dig has no
// "--" terminator, that allowlist is the sole defense against option/server
// injection.
func runDNSLookup(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	host, _ := getString(in, "host")
	if err := validateNetworkHost(host); err != nil {
		return "", err
	}
	bin, err := policy.ResolveBinary("dig")
	if err != nil {
		return "", err
	}

	// An input that parses as an IP literal is a reverse lookup; anything else
	// is treated as a name to resolve forward.
	if net.ParseIP(host) != nil {
		res, err := runCommand(ctx, bin, "+short", "-x", host)
		if err != nil {
			return "", err
		}
		names := nonEmptyLines(res.Stdout)
		if len(names) == 0 {
			return fmt.Sprintf("%s has no reverse (PTR) record.", host), nil
		}
		return fmt.Sprintf("%s resolves to:\n  %s", host, strings.Join(names, "\n  ")), nil
	}

	var addrs []string
	for _, qtype := range []string{"A", "AAAA"} {
		res, err := runCommand(ctx, bin, "+short", host, qtype)
		if err != nil {
			return "", err
		}
		addrs = append(addrs, nonEmptyLines(res.Stdout)...)
	}
	if len(addrs) == 0 {
		return fmt.Sprintf("%s did not resolve to any A/AAAA records.", host), nil
	}
	return fmt.Sprintf("%s resolves to:\n  %s", host, strings.Join(addrs, "\n  ")), nil
}

// nonEmptyLines splits text into trimmed, non-blank lines.
func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// runListeningPorts reports the sockets this Mac is accepting on, owned by which
// process. The protocol enum selects TCP listeners, bound UDP sockets, or both.
func runListeningPorts(ctx context.Context, _ registry.Capability, in map[string]any) (string, error) {
	proto, _ := getString(in, "protocol")
	if proto == "" {
		proto = "tcp"
	}
	bin, err := policy.ResolveBinary("lsof")
	if err != nil {
		return "", err
	}

	var listeners []listener
	if proto == "tcp" || proto == "both" {
		res, err := runCommand(ctx, bin, "-nP", "-iTCP", "-sTCP:LISTEN")
		if err != nil {
			return "", err
		}
		listeners = append(listeners, parseLsofListeners(res.Stdout, "tcp")...)
	}
	if proto == "udp" || proto == "both" {
		res, err := runCommand(ctx, bin, "-nP", "-iUDP")
		if err != nil {
			return "", err
		}
		listeners = append(listeners, parseLsofListeners(res.Stdout, "udp")...)
	}

	if len(listeners) == 0 {
		return fmt.Sprintf("No %s listening sockets were found.", proto), nil
	}
	sort.Slice(listeners, func(i, j int) bool {
		if listeners[i].port != listeners[j].port {
			return listeners[i].port < listeners[j].port
		}
		return listeners[i].proto < listeners[j].proto
	})
	var b strings.Builder
	fmt.Fprintf(&b, "%d listening socket(s):\n", len(listeners))
	for _, l := range listeners {
		fmt.Fprintf(&b, "  %s/%-5d %s (pid %s)\n", l.proto, l.port, l.command, l.pid)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// listener is one parsed lsof row: the listening port, owning process, and
// transport.
type listener struct {
	port    int
	proto   string
	command string
	pid     string
}

// parseLsofListeners turns `lsof -nP -i...` output into listener rows. lsof
// prints a COMMAND/PID/USER/FD/TYPE/DEVICE/SIZE/NODE/NAME table where NODE is
// the transport ("TCP"/"UDP") and the address (e.g. "*:7000", "127.0.0.1:631",
// "[::1]:5000") is the token immediately after it — note a TCP row appends a
// trailing "(LISTEN)", so the address is not the last field. Rows whose port is
// non-numeric, or that show a "local->peer" connected pair rather than a bound
// local socket, are skipped.
func parseLsofListeners(stdout, proto string) []listener {
	var out []listener
	seen := make(map[string]bool)
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || fields[0] == "COMMAND" {
			continue
		}
		addr := addressAfterNode(fields)
		if addr == "" {
			continue
		}
		// A connected socket (UDP can show one) has a "local->peer" NAME; only
		// a bound local listener is of interest here.
		if strings.Contains(addr, "->") {
			continue
		}
		colon := strings.LastIndex(addr, ":")
		if colon < 0 {
			continue
		}
		port, err := strconv.Atoi(addr[colon+1:])
		if err != nil {
			continue
		}
		key := proto + ":" + fields[1] + ":" + strconv.Itoa(port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, listener{port: port, proto: proto, command: fields[0], pid: fields[1]})
	}
	return out
}

// addressAfterNode returns the endpoint token that follows lsof's transport
// ("TCP"/"UDP") column, or "" if the row has no such column.
func addressAfterNode(fields []string) string {
	for i, f := range fields {
		if f == "TCP" || f == "UDP" {
			if i+1 < len(fields) {
				return fields[i+1]
			}
			return ""
		}
	}
	return ""
}

// runLanDevices lists hosts in the ARP cache — devices this Mac has recently
// exchanged traffic with. It is instant and generates no traffic, but is only
// as complete as the cache; scan_lan exists for a fuller picture.
func runLanDevices(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	bin, err := policy.ResolveBinary("arp")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, bin, "-an")
	if err != nil {
		return "", err
	}
	devices := parseArp(res.Stdout)
	if len(devices) == 0 {
		return "No devices are present in this Mac's ARP cache.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d device(s) in the ARP cache:\n", len(devices))
	for _, d := range devices {
		fmt.Fprintf(&b, "  %-15s  %s\n", d.ip, d.mac)
	}
	b.WriteString("\n(Only shows hosts this Mac has recently communicated with — run scan_lan for a fuller sweep.)")
	return b.String(), nil
}

// arpEntry is one resolved neighbour: an IP paired with its hardware (MAC)
// address.
type arpEntry struct {
	ip  string
	mac string
}

// arpLine matches a single `arp -an` row, e.g.
// "? (192.168.1.1) at a8:6d:aa:bb:cc:dd on en0 ifscope [ethernet]".
// Rows with an "(incomplete)" address (no reply yet) do not match and are
// skipped.
var arpLine = regexp.MustCompile(`\(([0-9.]+)\) at ([0-9a-fA-F:]+) `)

// parseArp extracts resolved IP/MAC pairs from `arp -an`, de-duplicating by IP
// and skipping incomplete entries.
func parseArp(stdout string) []arpEntry {
	var out []arpEntry
	seen := make(map[string]bool)
	for _, line := range strings.Split(stdout, "\n") {
		m := arpLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		ip, mac := m[1], m[2]
		// A bare "incomplete" entry has no colon-delimited MAC, so the regex's
		// MAC group already excludes it; guard against a stray single token too.
		if !strings.Contains(mac, ":") || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, arpEntry{ip: ip, mac: mac})
	}
	return out
}

// scanLanMaxHosts caps the active sweep at a /24's worth of addresses. A subnet
// larger than /24 (more zero bits than this) is refused rather than flooded.
const scanLanMaxHosts = 256

// scanLanTimeout bounds the whole sweep so a large or unresponsive subnet cannot
// hang the request; it is layered under the inherited request context, whichever
// fires first.
const scanLanTimeout = 20 * time.Second

// runScanLan actively discovers LAN devices: it derives the local IPv4 subnet,
// pings every address in it concurrently (which populates the ARP cache for
// hosts that answer), then reports the resolved neighbours. The IPs it pings are
// computed from the local subnet, never model input, so no host validation is
// needed here.
func runScanLan(ctx context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	ifaceBin, err := policy.ResolveBinary("route")
	if err != nil {
		return "", err
	}
	rt, err := runCommand(ctx, ifaceBin, "-n", "get", "default")
	if err != nil {
		return "", err
	}
	iface, _ := parseDefaultRoute(rt.Stdout)
	if iface == "" {
		return "No default route was found — there is no local network to scan.", nil
	}
	ifBin, err := policy.ResolveBinary("ifconfig")
	if err != nil {
		return "", err
	}
	ifRes, err := runCommand(ctx, ifBin, iface)
	if err != nil {
		return "", err
	}
	ipStr, maskStr, _ := parseIfconfig(ifRes.Stdout)
	hosts, err := subnetHosts(ipStr, maskStr)
	if err != nil {
		return "", err
	}

	pingBin, err := policy.ResolveBinary("ping")
	if err != nil {
		return "", err
	}
	sweepCtx, cancel := context.WithTimeout(ctx, scanLanTimeout)
	defer cancel()
	sweepSubnet(sweepCtx, pingBin, hosts)

	// After the sweep, the ARP cache holds every host that answered.
	arpBin, err := policy.ResolveBinary("arp")
	if err != nil {
		return "", err
	}
	res, err := runCommand(ctx, arpBin, "-an")
	if err != nil {
		return "", err
	}
	inSubnet := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		inSubnet[h] = true
	}
	var devices []arpEntry
	for _, d := range parseArp(res.Stdout) {
		if inSubnet[d.ip] {
			devices = append(devices, d)
		}
	}
	if len(devices) == 0 {
		return fmt.Sprintf("Swept %d addresses on %s; no devices responded.", len(hosts), iface), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Swept %d addresses on %s — %d device(s) responded:\n", len(hosts), iface, len(devices))
	for _, d := range devices {
		fmt.Fprintf(&b, "  %-15s  %s\n", d.ip, d.mac)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// subnetHosts enumerates the usable host addresses of the subnet that ipStr/mask
// describe, excluding the network and broadcast addresses and the Mac's own
// address. It refuses a subnet wider than /24 (more than scanLanMaxHosts
// addresses) so the sweep stays bounded.
func subnetHosts(ipStr, mask string) ([]string, error) {
	ip := net.ParseIP(ipStr).To4()
	if ip == nil {
		return nil, fmt.Errorf("could not determine this Mac's IPv4 address for scanning")
	}
	mb, ok := parseMask(mask)
	if !ok {
		return nil, fmt.Errorf("could not parse the subnet mask %q", mask)
	}
	ipU := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	maskU := uint32(mb[0])<<24 | uint32(mb[1])<<16 | uint32(mb[2])<<8 | uint32(mb[3])
	network := ipU & maskU
	broadcast := network | ^maskU
	size := broadcast - network + 1
	if size > scanLanMaxHosts {
		prefix := bits.OnesCount32(maskU)
		return nil, fmt.Errorf("subnet /%d holds %d addresses, which is larger than this scanner supports (max /24); narrow the network or use lan_devices instead", prefix, size)
	}

	var hosts []string
	for addr := network + 1; addr < broadcast; addr++ {
		if addr == ipU {
			continue // don't ping ourselves
		}
		hosts = append(hosts, u32ToIPString(addr))
	}
	return hosts, nil
}

// u32ToIPString renders a packed IPv4 address as a dotted-quad string.
func u32ToIPString(n uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d", byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// sweepSubnet pings every host concurrently with a tight per-probe deadline. The
// results are intentionally ignored: the goal is to populate the ARP cache for
// responders, which the caller reads afterwards. Concurrency is bounded and the
// whole sweep stops as soon as ctx is cancelled (request dropped or timeout).
func sweepSubnet(ctx context.Context, pingBin string, hosts []string) {
	const workers = 32
	jobs := make(chan string)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for host := range jobs {
				if ctx.Err() != nil {
					return
				}
				// One probe with a one-second cap enforced by a per-probe
				// context deadline (not a ping flag), so a host that never
				// answers can't stall a worker. host is a computed dotted-quad,
				// never model input.
				probeCtx, probeCancel := context.WithTimeout(ctx, time.Second)
				_, _ = runCommand(probeCtx, pingBin, "-c", "1", "-W", "1000", "--", host)
				probeCancel()
			}
		}()
	}
	for _, h := range hosts {
		// Select on ctx so a cancellation (after workers have already exited)
		// cannot block this send forever.
		select {
		case jobs <- h:
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		}
	}
	close(jobs)
	wg.Wait()
}
