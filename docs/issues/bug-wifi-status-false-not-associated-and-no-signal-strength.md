**bug**
`wifi_status` (`runWifiStatus` in `internal/engine/builtins_system.go`) reports
`networksetup -getairportnetwork <dev>` verbatim as the joined network. On a Mac
where the calling process lacks Location Services authorization, that command
prints "You are not associated with an AirPort network." even while the Mac is
genuinely joined to a network — macOS has gated SSID/BSSID visibility behind the
caller's Location Services permission since Big Sur, and `networksetup` degrades
to this false negative instead of surfacing a permission error.

Reproduced live: `networksetup -getairportnetwork en0` printed "You are not
associated with an AirPort network." while `system_profiler SPAirPortDataType`
on the same machine, at the same time, showed `Current Network Information:
NETGEAR11_2GEXT` (802.11n, channel 7). The model relayed the tool's false answer
to the user, who was in fact connected to `NETGEAR11_2GEXT`.

Impact: any user whose Terminal/parent process hasn't been granted Location
Services will get an incorrect "not connected" answer from `wifi_status` even
though they are connected — with no indication that the answer is unreliable
rather than authoritative.

Secondary, independent gap: `wifi_status` only ever reports radio power and
network name — it has no signal-strength (RSSI/dBm) field at all, so even when
the network name resolves correctly there is no way to answer "is my signal
good?" `system_profiler SPAirPortDataType` exposes this data (visible in the
same output used to reproduce the false negative above), so it wasn't wired in
anywhere.

**fixed**
`runWifiStatus` (`internal/engine/builtins_system.go`) no longer relies on
`networksetup -getairportnetwork` for the joined-network name. It now reads the
current network and its signal from `system_profiler SPAirPortDataType -json`,
whose SSID visibility is not gated behind Location Services, so the "not
associated" false negative no longer occurs on a machine that is in fact
connected. The radio's power state and the interface name are still read from
`networksetup` (both report correctly without Location Services and give an
authoritative on/off answer).

Signal strength is now reported: the fix parses the RSSI out of
system_profiler's `spairport_signal_noise` field ("-42 dBm / -88 dBm") and adds
a plain-language quality rating (excellent ≥ -50, good ≥ -60, fair ≥ -70, weak ≥
-80, else very weak), so "is my signal good?" is answerable.

Robustness: the network name/signal come only from the interface whose `_name`
matches the resolved Wi-Fi device, which excludes the peer-to-peer `awdl0`
(AirDrop) interface; only the *current* network is read, so the neighbor SSIDs
system_profiler also lists are never emitted. The renderer distinguishes three
network states rather than two, so a failed/unreadable probe never masquerades
as "not connected" (which would recreate this very bug): a populated interface
with an SSID reads as joined; the interface present but SSID-less reads as a
trustworthy "Not currently joined to a Wi-Fi network"; and an empty, unparseable,
or interface-absent profiler result reads as "Unable to determine the current
Wi-Fi network (system_profiler data unavailable)" while still reporting the
authoritative radio power.

Verified end-to-end on a live Mac connected to a real network: the tool now
reports `Connected to: <SSID>` with `Signal strength: -42 dBm (excellent)` where
it previously said "You are not associated with an AirPort network." Regression
coverage: `TestRenderWifiStatus_Connected` / `_NotConnected` / `_InterfaceAbsent`
/ `_RadioOff` / `_ProfilerUnavailable` / `_ProfilerUnparseable`, `TestParseRSSI`,
and `TestDescribeSignal` in `internal/engine/builtins_system_test.go`.
