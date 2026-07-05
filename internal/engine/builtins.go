// builtins.go implements "builtin" capabilities: ones the server answers itself,
// in Go, without launching any external program.
//
// The name is borrowed from shells, where a "builtin" such as cd or pwd is
// handled by the shell directly rather than by executing a separate binary. The
// sole builtin today is pwd: the current working directory is a property of THIS
// process that we already hold, so the right answer comes from a one-line
// standard-library call. Spawning /bin/pwd would only re-report the very
// directory the child inherited from us — the same answer at the cost of a
// subprocess. A builtin capability therefore has no Binary and produces its
// output text directly.
package engine

import (
	"context"
	"os"

	"mcp-server-mac-os/internal/registry"
)

// BuiltinFunc produces a capability's output directly, with no subprocess. Like
// an ArgBuilder it receives the normalized parameters, but it returns finished
// output text rather than an argument vector.
type BuiltinFunc func(ctx context.Context, c registry.Capability, in map[string]any) (string, error)

// builtins maps a capability's builder name to its builtin implementation. A
// name present here is served in-process; a name present in the argv `builders`
// map is run as a subprocess. The two sets are disjoint.
var builtins = map[string]BuiltinFunc{
	"pwd":                runPwd,
	"read_setting":       runReadSetting,
	"largest_files":      runLargestFiles,
	"image_info":         runImageInfo,
	"spotlight_search":   runSpotlightSearch,
	"read_clipboard":     runReadClipboard,
	"search_mail":        runSearchMail,
	"list_inbox":         runListInbox,
	"read_message":       runReadMessage,
	"list_tabs":          runListTabs,
	"current_tab":        runCurrentTab,
	"now_playing":        runNowPlaying,
	"list_calendars":     runListCalendars,
	"query_events":       runQueryEvents,
	"list_reminders":     runListReminders,
	"find_contact":       runFindContact,
	"get_contact":        runGetContact,
	"check_messages":     runCheckMessages,
	"search_messages":    runSearchMessages,
	"read_conversation":  runReadConversation,
	"list_conversations": runListConversations,

	"list_notes":   runListNotes,
	"search_notes": runSearchNotes,
	"read_note":    runReadNote,
	"list_folders": runListFolders,

	"search_photos":         runSearchPhotos,
	"list_photos":           runListPhotos,
	"get_photo":             runGetPhoto,
	"export_photo":          runExportPhoto,
	"list_albums":           runListAlbums,
	"list_photo_folders":    runListPhotoFolders,
	"get_album_photos":      runGetAlbumPhotos,
	"list_favorites":        runListFavorites,
	"list_recently_deleted": runListRecentlyDeleted,
	"get_selection":         runGetSelection,
	"library_stats":         runLibraryStats,

	"list_applications":         runListApplications,
	"search_applications":       runSearchApplications,
	"search_app_store":          runSearchAppStore,
	"list_running_applications": runListRunningApplications,
	"list_windows":              runListWindows,

	"list_printers":   runListPrinters,
	"list_print_jobs": runListPrintJobs,

	"wifi_status":         runWifiStatus,
	"list_preferred_wifi": runListPreferredWifi,
	"bluetooth_status":    runBluetoothStatus,
	"power_status":        runPowerStatus,

	"about_this_mac":        runAboutThisMac,
	"disk_usage":            runDiskUsage,
	"software_update_check": runSoftwareUpdateCheck,
	"sleep_assertions":      runSleepAssertions,
	"system_log":            runSystemLog,
	"thermal_state":         runThermalState,

	"list_airplay_devices": runListAirplayDevices,
	"list_input_sources":   runListInputSources,

	"capture_screen": runCaptureScreen,
	"capture_region": runCaptureRegion,
	"capture_window": runCaptureWindow,

	"current_network":  runCurrentNetwork,
	"dns_servers":      runDNSServers,
	"ping_host":        runPingHost,
	"dns_lookup":       runDNSLookup,
	"listening_ports":  runListeningPorts,
	"lan_devices":      runLanDevices,
	"scan_lan":         runScanLan,
	"trace_route":      runTraceRoute,
	"whois_lookup":     runWhoisLookup,
	"route_table":      runRouteTable,
	"interface_stats":  runInterfaceStats,
	"dns_cache_lookup": runDNSCacheLookup,

	"list_processes": runListProcesses,
	"process_info":   runProcessInfo,
	"cpu_load":       runCpuLoad,
	"memory_stats":   runMemoryStats,
	"gpu_stats":      runGPUStats,
	"startup_items":  runStartupItems,
	"top_processes":  runTopProcesses,

	"verify_signature": runVerifySignature,
	"gatekeeper_check": runGatekeeperCheck,
	"sip_status":       runSipStatus,
	"quarantine_info":  runQuarantineInfo,

	"find_credential":          runFindCredential,
	"find_internet_credential": runFindInternetCredential,
	"list_keychains":           runListKeychains,

	"time_machine_status": runTimeMachineStatus,
	"list_backups":        runListBackups,
	"list_volumes":        runListVolumes,
	"volume_info":         runVolumeInfo,
	"eject_volume":        runEjectVolume,

	"list_shortcuts": runListShortcuts,
}

// lookupBuiltin returns the builtin for a builder name and whether one exists.
func lookupBuiltin(name string) (BuiltinFunc, bool) {
	f, ok := builtins[name]
	return f, ok
}

// runPwd returns the server process's current working directory.
func runPwd(_ context.Context, _ registry.Capability, _ map[string]any) (string, error) {
	return os.Getwd()
}
