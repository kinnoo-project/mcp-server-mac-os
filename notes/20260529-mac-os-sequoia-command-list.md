# macOS Sequoia 15.6 (Darwin 24.6) — Categorized Command Reference

> Catalog of native CLI executables found under `/bin`, `/sbin`, `/usr/bin`, and `/usr/sbin` on this machine (1,258 unique binaries enumerated). Compiled for use as a planning reference for the macOS MCP Server project.
>
> **Legend:** Commands marked with an asterisk (`*`) are among the most commonly used / highest-utility commands for everyday Mac CLI interaction and are strong candidates for early MCP tool coverage.
>
> Discovery method (per [notes/20260529-getting-list-of-mac-os-commands.md](notes/20260529-getting-list-of-mac-os-commands.md)):
> ```sh
> ls -1 $(echo $PATH | tr ':' ' ') /usr/libexec 2>/dev/null | sort -u
> ```
> Descriptions sourced from native `man`/`apropos` semantics and standard Unix/Darwin documentation.

## Categories

1. Filesystem & File Manipulation
2. Archives, Compression & Packaging
3. Disk, Volume & Storage
4. Process, Job & Resource Management
5. Networking — Configuration
6. Networking — Diagnostics & Transfer
7. System Information & Hardware
8. Power, Boot & Software Updates
9. Users, Accounts & Authentication
10. Security, Cryptography & Code Signing
11. Logging, Diagnostics & Observability
12. Shells & Scripting Languages
13. Developer Toolchain (Xcode CLT)
14. macOS Application & Workspace Control
15. Audio, Video & Media
16. Printing (CUPS)
17. Mail & Messaging
18. Editors & Pagers
19. Time, Date & Scheduling
20. Database Utilities
21. Web/HTTP Servers
22. Help & Documentation

---

## 1. Filesystem & File Manipulation

Commands for navigating, inspecting, creating, copying, moving, and deleting files and directories.

| Command | Description |
| --- | --- |
| `ls` * | Lists directory contents with optional permission, size, and ownership details. |
| `cd` * | Changes the shell's current working directory. |
| `pwd` * | Prints the absolute path of the current working directory. |
| `cp` * | Copies files or directory trees from a source to a destination. |
| `mv` * | Moves or renames files and directories. |
| `rm` * | Removes (deletes) files; with `-r` removes directories recursively. |
| `mkdir` * | Creates new directories, optionally building intermediate parents with `-p`. |
| `rmdir` | Removes empty directories. |
| `ln` * | Creates hard or symbolic links to files and directories. |
| `link` / `unlink` | Low-level wrappers around `link(2)` / `unlink(2)` syscalls. |
| `touch` * | Creates empty files or updates access/modification timestamps. |
| `stat` * | Displays detailed file metadata (inode, perms, size, timestamps). |
| `file` * | Detects file type by inspecting contents (magic numbers). |
| `find` * | Walks a directory tree to locate files matching predicates. |
| `locate` | Searches a prebuilt filesystem name index for matching paths. |
| `mdfind` * | Queries the Spotlight metadata index (the GUI search engine, from CLI). |
| `mdls` | Lists Spotlight metadata attributes for a given file. |
| `mdimport` | Forces Spotlight to (re)index specified files or directories. |
| `mdutil` | Manages Spotlight indexing state per volume. |
| `du` * | Reports disk usage of files and directories. |
| `df` * | Reports free space on mounted filesystems. |
| `realpath` | Resolves a path to its canonical absolute form. |
| `readlink` | Prints the target of a symbolic link. |
| `basename` / `dirname` | Strip directory or filename components from a path. |
| `tree`-like via `find` | (No native `tree`; use `find` or install Homebrew `tree`.) |
| `chmod` * | Modifies file permission bits. |
| `chown` * | Changes file owner and/or group. |
| `chgrp` | Changes the group ownership of files. |
| `chflags` | Sets BSD-level file flags (e.g., `uchg`, `hidden`). |
| `xattr` * | Inspects and manipulates extended attributes (incl. macOS quarantine flag). |
| `GetFileInfo` / `SetFile` | Reads/writes legacy HFS Finder metadata (creator, type, flags). |
| `mkfifo` | Creates a named pipe (FIFO). |
| `mknod` | Creates a special filesystem node (character/block device). |
| `mktemp` * | Creates a uniquely named temporary file or directory safely. |
| `truncate` | Shrinks or extends a file to a specified size. |
| `dd` | Low-level block copy/conversion utility for files and devices. |
| `cat` * | Concatenates and prints file contents to stdout. |
| `head` * | Prints the first N lines (or bytes) of a file. |
| `tail` * | Prints the last N lines, optionally following appended data (`-f`). |
| `less` * / `more` | Paginates output for interactive scrolling. |
| `wc` * | Counts lines, words, and bytes in input. |
| `sort` * | Sorts lines of text files. |
| `uniq` * | Filters or counts adjacent duplicate lines. |
| `cut` * | Extracts selected columns or byte ranges from each line. |
| `paste` | Merges corresponding lines of files into columns. |
| `tr` * | Translates or deletes characters in a stream. |
| `tee` * | Reads stdin and writes to stdout and one or more files simultaneously. |
| `comm` | Compares two sorted files line by line. |
| `diff` * | Shows differences between two files or directories. |
| `diff3` / `sdiff` | Three-way and side-by-side file diffing. |
| `patch` * | Applies a `diff` patch to a source tree. |
| `cmp` | Byte-by-byte comparison of two files. |
| `od` / `hexdump` / `xxd` | Display binary data in octal/hex/various formats. |
| `strings` | Extracts printable ASCII/Unicode strings from binaries. |
| `grep` * / `egrep` / `fgrep` | Searches text for matching lines using regex/fixed patterns. |
| `awk` * | Pattern-scanning and column-processing programming language. |
| `sed` * | Stream editor for filter/transform operations on text. |
| `fold` / `expand` / `unexpand` / `fmt` / `pr` | Reformat text (wrap, tabs, paginate). |
| `column` | Formats input into aligned columns. |
| `split` / `csplit` | Splits files into smaller chunks. |
| `nl` | Numbers the lines of a file. |
| `rev` | Reverses each input line character-by-character. |
| `dot_clean` | Merges Apple `._` resource-fork files for non-HFS volumes. |

---

## 2. Archives, Compression & Packaging

| Command | Description |
| --- | --- |
| `tar` * / `bsdtar` | Creates and extracts tape archives (with built-in gzip/bzip2/xz support). |
| `gzip` * / `gunzip` / `zcat` / `gzcat` | gzip compression family. |
| `bzip2` / `bunzip2` / `bzcat` / `bzip2recover` | bzip2 compression family. |
| `compress` / `uncompress` | Legacy LZW compression utilities. |
| `compression_tool` | Apple's native libcompression CLI wrapper. |
| `zip` * / `unzip` * / `zipinfo` / `zipgrep` / `zipnote` / `zipsplit` / `zipcloak` / `zipdetails` | PKZIP archive toolchain. |
| `xar` | macOS eXtensible ARchive (used for `.pkg` installers). |
| `aa` / `yaa` / `aea` | Apple Archive / Apple Encrypted Archive utilities. |
| `ditto` * | Copies files/directories faithfully preserving macOS metadata, optionally as archives. |
| `pax` | POSIX archive interchange tool. |
| `cpio` | Old-school copy-in / copy-out archiver. |
| `installer` * | Installs macOS `.pkg` files from the command line. |
| `pkgbuild` / `productbuild` / `productsign` / `pkgutil` | Build, sign, and inspect macOS installer packages. |
| `xip` | Extracts Apple-signed `.xip` archives. |
| `mkbom` / `lsbom` | Build and list Bill-Of-Materials files used by installers. |
| `bspatch` | Applies a binary diff patch (BSDiff format). |
| `streamzip` | Perl helper that creates a ZIP stream on stdout. |
| `funzip` | Extracts the first member of a ZIP stream from stdin. |

---

## 3. Disk, Volume & Storage Management

| Command | Description |
| --- | --- |
| `diskutil` * | Apple's unified disk and volume management tool (APFS, HFS+, RAID, encryption). |
| `hdiutil` * | Creates, mounts, and manipulates disk images (`.dmg`, `.sparseimage`). |
| `mount` * / `umount` * | Mount or unmount filesystems. |
| `mount_apfs` / `mount_hfs` / `mount_msdos` / `mount_exfat` / `mount_nfs` / `mount_smbfs` / `mount_webdav` / `mount_ftp` / `mount_cd9660` / `mount_udf` / `mount_9p` / `mount_virtiofs` / `mount_tmpfs` / `mount_devfs` / `mount_fdesc` / `mount_acfs` / `mount_afp` / `mount_cddafs` | Filesystem-specific mount backends. |
| `fsck` * / `fsck_apfs` / `fsck_hfs` / `fsck_msdos` / `fsck_exfat` / `fsck_udf` / `fsck_cs` / `fsck_fskit` | Filesystem consistency checkers. |
| `newfs_apfs` / `newfs_hfs` / `newfs_msdos` / `newfs_exfat` / `newfs_udf` / `newfs_fskit` | Format new filesystems on a partition. |
| `fstyp` / `fstyp_hfs` / `fstyp_msdos` / `fstyp_ntfs` / `fstyp_udf` | Detects filesystem type on a device. |
| `lsvfs` | Lists VFS modules registered with the kernel. |
| `vifs` | Edits `/etc/fstab` safely with locking. |
| `mtree` | Maps, verifies, and recreates directory hierarchies. |
| `automount` | Configures the macOS auto-mount daemon. |
| `apfs_hfs_convert` | Converts an HFS+ volume in place to APFS. |
| `apfs_unlockfv` | Unlocks an encrypted APFS FileVault volume. |
| `bless` | Sets the active boot volume for the firmware. |
| `fdesetup` | Manages FileVault 2 full-disk encryption. |
| `firmwarepasswd` | Sets/removes the Mac firmware boot password (Intel Macs). |
| `bputil` | Manages Apple Silicon LocalPolicy and boot security mode. |
| `csrutil` | Configures System Integrity Protection (must be run from Recovery). |
| `trimforce` | Forces TRIM support for third-party SSDs. |
| `pdisk` / `gpt` / `fdisk` | Low-level partition table editors. |
| `disklabel` | Modify the on-disk label of a partition. |
| `drutil` | Controls optical disc drives (burn, eject, info). |
| `mkfile` | Creates a file of a specified size filled with zeros. |
| `SafeEjectGPU` | Safely detaches an external GPU. |
| `quota` / `quotacheck` / `quotaon` / `quotaoff` / `edquota` / `repquota` / `setquota` | Disk-quota administration. |
| `snquota` | Xsan / StorNext per-directory quotas. |
| `purge` * | Forces the kernel to flush the disk cache (frees inactive RAM). |
| `dynamic_pager` | Manages the macOS swap file pager. |
| `vsdbutil` | Toggles per-volume "ignore ownership" flag. |

---

## 4. Process, Job & Resource Management

| Command | Description |
| --- | --- |
| `ps` * | Snapshot of currently running processes. |
| `top` * | Live interactive process and resource viewer. |
| `htop`-like via `top` | (No native `htop`; install via Homebrew.) |
| `kill` * | Sends a signal (default `TERM`) to one or more processes by PID. |
| `killall` * | Sends a signal to all processes matching a name. |
| `pkill` / `pgrep` * | Signal or list processes by name/attribute regex. |
| `nice` / `renice` | Launches or re-prioritizes processes by scheduling niceness. |
| `taskpolicy` | Adjusts QoS / I/O / CPU policy of a process or new command. |
| `taskinfo` | Prints detailed Mach task accounting for a PID. |
| `lsof` * | Lists open files (and sockets) per process. |
| `fuser` | Identifies processes holding a file or socket open. |
| `jobs` / `fg` / `bg` / `wait` / `disown` (shell built-ins) | Shell job-control primitives. |
| `nohup` | Runs a command immune to hangup signals. |
| `time` | Times the execution of a command. |
| `at` / `atq` / `atrm` / `batch` | Schedules one-shot jobs for later execution. |
| `cron` / `crontab` * | Scheduled recurring jobs. |
| `launchctl` * | Loads, starts, stops, and queries launchd services (the canonical macOS service manager). |
| `launchd` | The macOS service-management super-daemon (PID 1). |
| `sample` | Profiles a running process for N seconds. |
| `spindump` | Captures stack traces for hung or wedged processes. |
| `heap` / `vmmap` / `leaks` / `malloc_history` / `footprint` / `zprint` | Memory analysis tools for Mach tasks. |
| `gcore` | Generates a core dump of a running process. |
| `dtrace` / `dtruss` | Dynamic tracing framework and `strace`-equivalent. |
| `ktrace` / `xctrace` / `trace` | Kernel/Instruments tracing utilities. |
| `fs_usage` * | Real-time filesystem syscall activity per process. |
| `sc_usage` | System-call usage statistics per process. |
| `latency` | Reports scheduling and interrupt latency. |
| `lsmp` | Lists Mach ports owned by a process. |
| `lskq` | Lists kqueue file descriptors per process. |
| `lsappinfo` * | Queries the Launch Services app database (running GUI apps, frontmost, etc.). |
| `memory_pressure` | Reports system memory pressure status. |
| `vm_stat` * | Mach virtual memory statistics. |
| `iostat` | I/O and CPU statistics over time. |
| `iotop` / `iopending` / `iopattern` / `iosnoop` | DTrace-based I/O activity views. |
| `hostinfo` | Prints kernel/host info (CPUs, memory, version). |
| `uptime` * | Shows load average and time since boot. |
| `w` * / `who` * / `users` / `whoami` * / `logname` / `id` * | Who is logged in / current identity. |
| `last` / `lastcomm` | History of logins and executed commands. |
| `sysctl` * | Reads or writes kernel state variables (e.g. `hw.ncpu`, `kern.maxfiles`). |
| `ulimit` (shell built-in) | Inspect or set resource limits for the shell. |
| `lockf` | Acquires an advisory file lock around a command. |

---

## 5. Networking — Configuration

| Command | Description |
| --- | --- |
| `networksetup` * | Apple's high-level CLI for configuring network services (Wi-Fi, Ethernet, DNS, proxies). |
| `ifconfig` * | Inspect or configure network interfaces (BSD legacy but still functional). |
| `ipconfig` * | Manages IPv4/DHCP/BootP on macOS interfaces. |
| `route` | Reads/manipulates the IP routing table. |
| `ndp` | Manages the IPv6 neighbor discovery cache. |
| `arp` | Inspects and edits the IPv4 ARP cache. |
| `scutil` * | Queries/modifies the System Configuration framework (network, DNS, location). |
| `scselect` | Selects an active network "Location". |
| `wdutil` | Wi-Fi diagnostics utility (replacement for legacy `airport`). |
| `dnctl` / `pfctl` * | Configures Dummynet shaping and the PF packet filter (macOS firewall). |
| `dns-sd` | CLI for Bonjour/mDNS service discovery and registration. |
| `dscacheutil` | Queries/flushes the DirectoryService/DNS caches. |
| `smbutil` | View/login to SMB shares. |
| `sharing` * | Configures SMB/AFP/FTP file-sharing exports. |
| `nfsd` / `nfsiod` / `nfsstat` / `nfs4mapid` | NFS server, client, and ID-mapping. |
| `showmount` | Lists NFS exports on a remote host. |
| `rpcbind` / `rpcinfo` / `rpc.lockd` / `rpc.statd` | ONC RPC infrastructure. |
| `vpnd` / `racoon` / `setkey` | VPN server and IPsec key-management daemons. |
| `pppd` / `chat` | Point-to-Point Protocol dial-up. |
| `rtadvd` | IPv6 router advertisement daemon. |
| `rarpd` | Reverse-ARP daemon. |
| `tsig-keygen` / `ddns-confgen` | Generate BIND TSIG / dynamic DNS keys. |
| `mDNSResponder` / `mDNSResponderHelper` | Bonjour daemon binaries. |
| `netbiosd` | NetBIOS name service daemon. |
| `WirelessRadioManagerd` | Manages Wi-Fi/Bluetooth radio coexistence. |

---

## 6. Networking — Diagnostics & Transfer

| Command | Description |
| --- | --- |
| `ping` * / `ping6` | Send ICMP echo requests to a host. |
| `traceroute` * / `traceroute6` | Trace the IP route to a destination. |
| `netstat` * | Show network connections, routing tables, and interface stats. |
| `nettop` * | Live per-process network connection viewer. |
| `tcpdump` * | Captures and decodes packets from the network. |
| `netcat` via `nc` * | Read/write arbitrary TCP/UDP connections. |
| `networkQuality` * | Apple's RPM-based measurement of network responsiveness/throughput. |
| `iperf3-darwin` | Network throughput measurement tool. |
| `fping` | Parallel ICMP pinger across many hosts. |
| `host` * / `dig` * / `nslookup` * / `delv` / `nsupdate` | DNS query / update tools. |
| `whois` * | Queries WHOIS registries for domain/IP info. |
| `curl` * | Transfers data over many protocols (HTTP, FTP, SCP, etc.). |
| `wget`-equivalent via `curl` | (No native `wget`; use `curl -O`.) |
| `nscurl` | Apple's hardened-network curl variant for ATS testing. |
| `scp` * / `sftp` * / `ssh` * / `ssh-add` / `ssh-agent` / `ssh-keygen` / `ssh-copy-id` / `ssh-keyscan` / `slogin` / `sshd` | SSH client/server suite. |
| `rsync` * | Efficient file-tree synchronization over SSH or daemon. |
| `ftp`-equivalent via `tnftp`/`curl` | (Legacy `ftp` removed; use `curl ftp://`.) |
| `tftp` | Trivial FTP client. |
| `telnet`-equivalent via `nc` | (No native `telnet`; use `nc host port`.) |
| `talk` / `write` / `mesg` / `wall` | Classic terminal-to-terminal messaging. |
| `cu` / `screen` | Serial / multiplexed terminal sessions. |
| `expect` / `chat` | Scripted interaction with TTY programs. |
| `uucp` / `uuxqt` / `uucico` / `uux` / `uustat` / `uuname` / `uuto` / `uupick` / `uulog` / `uusched` | UUCP networking (largely historical). |
| `snmp*` (`snmpget`, `snmpwalk`, `snmpset`, `snmpbulkget`, `snmptrap`, etc.) | Net-SNMP client/server toolset. |
| `ldapsearch` / `ldapadd` / `ldapmodify` / `ldapdelete` / `ldapcompare` / `ldappasswd` / `ldapurl` / `ldapwhoami` / `ldapexop` / `ldapmodrdn` | OpenLDAP client utilities. |
| `slapd*` family (`slapadd`, `slapcat`, `slapconfig`, ...) | OpenLDAP server admin tools. |
| `kadmin` / `kinit` * / `kdestroy` / `klist` / `kpasswd` / `kswitch` / `ktutil` / `kcc` / `kgetcred` / `krbservicesetup` / `kdcsetup` / `krb5-config` | Kerberos administration and tickets. |
| `gssd` | GSS-API daemon. |
| `sntp` / `sntpd` | (S)NTP client/server. |
| `timesyncanalyse` | Analyzes time synchronization quality. |

---

## 7. System Information & Hardware Inspection

| Command | Description |
| --- | --- |
| `sw_vers` * | Prints macOS product name, version, and build. |
| `uname` * | Prints kernel/OS identification. |
| `system_profiler` * | Generates the full System Information report (supports `-json`/`-xml`). |
| `systemsetup` | Configures system preferences (time, sleep, remote login). |
| `profiles` | Manages Configuration Profiles (MDM-style). |
| `ioreg` * | Dumps the IOKit registry of devices and drivers. |
| `ioalloccount` / `ioclasscount` | IOKit memory/class accounting. |
| `kextstat` * / `kextload` / `kextunload` / `kextfind` / `kextcache` / `kextlibs` / `kextutil` / `kmutil` | Kernel-extension management. |
| `nvram` * | Reads/writes Open Firmware / EFI NVRAM variables. |
| `pmset` * | Manages power-management settings (sleep, hibernate, schedules). |
| `caffeinate` * | Prevents the system from sleeping for the duration of a command. |
| `powermetrics` * | Detailed CPU/GPU/thermal/power telemetry. |
| `thermal` | Queries the thermal-pressure state. |
| `bioutil` | Manage Touch ID and biometric policies. |
| `hidutil` | HID (keyboard/mouse) device inspection and remapping. |
| `BlueTool` / `bluetoothd` / `BTLEServer` | Bluetooth stack control and daemons. |
| `arch` | Reports or runs a command under a specific architecture (`x86_64`/`arm64`). |
| `machine` | Prints the hardware architecture name. |
| `pagesize` | Prints the system VM page size. |
| `sysdiagnose` * | Collects a full diagnostic snapshot of the system. |
| `tailspin` | Lightweight stackshot-based system profiler. |
| `tmdiagnose` / `mddiagnose` / `csfdiagnose` / `dddiagnose` / `hpmdiagnose` / `smbdiagnose` / `tbtdiagnose` / `usbdiagnose` / `xcsdiagnose` / `uasysdiagnose` / `avbdiagnose` | Component-specific diagnostic collectors. |
| `assetutil` | Inspects compiled `.car` Asset Catalogs. |
| `serverinfo` | Reports macOS Server install state. |
| `update_dyld_shared_cache` | Rebuilds the system dyld shared cache. |
| `usbcfwflasher` / `update_mcdp29xx` | Low-level firmware updaters. |
| `bm4` / `psm` | (Internal Apple hardware utilities.) |
| `bitesize.d` / `cpuwalk.d` / `dispqlen.d` / many `*.d` | Bundled DTrace one-liner scripts under `/usr/bin`. |

---

## 8. Power, Boot & Software Updates

| Command | Description |
| --- | --- |
| `softwareupdate` * | Apple's CLI to list/download/install macOS and security updates. |
| `xprotect` | Manages Apple's XProtect malware-signature engine. |
| `reboot` * | Reboots the system. |
| `shutdown` * | Shuts down or restarts at a scheduled time. |
| `halt` | Halts the system without power-off. |
| `sleep` (shell command name conflicts with util) | Pauses execution for N seconds. |
| `pmset` * | (See System Info) — also schedules wake/sleep/restart events. |
| `caffeinate` * | Keeps the system awake while a command runs. |
| `systemextensionsctl` | Manage user-space System Extensions (replacement for many kexts). |
| `spctl` * | Manage Gatekeeper assessment policies. |
| `gktool` | Gatekeeper "ticket" tool for notarized binaries. |
| `stapler` | Staples a notarization ticket to a signed bundle. |
| `syspolicy_check` | Evaluates a binary against system security policy. |
| `system-override` | Apply user-confirmed system-policy overrides. |

---

## 9. Users, Accounts & Authentication

| Command | Description |
| --- | --- |
| `dscl` * | Directory Service command line — read/edit local users/groups in Open Directory. |
| `dsconfigad` / `dsconfigldap` | Bind/unbind the Mac to Active Directory or LDAP. |
| `dseditgroup` * | Add/remove/inspect group membership. |
| `dsenableroot` | Enable or disable the `root` account. |
| `dsmemberutil` | Resolve UID/GID/SID/group-membership queries. |
| `dsexport` / `dsimport` | Bulk export/import directory records. |
| `sysadminctl` * | Apple's recommended tool to create/delete users, set passwords, manage admin status. |
| `createhomedir` | Generates a home directory for a directory user. |
| `mnthome` / `repairHomePermissions` | Mount or repair user home directories. |
| `passwd` * / `chpass` / `chfn` / `chsh` | Change password / GECOS / login shell. |
| `pwpolicy` | View/edit local password policies. |
| `mkpassdb` / `pwd_mkdb` / `vipw` | Rebuild local password database / edit master.passwd. |
| `newgrp` | Start a new shell with a different primary group. |
| `groups` * | List groups for a user. |
| `id` * | Print user/group IDs of the current or specified user. |
| `su` * / `sudo` * / `visudo` | Switch user / run-as-root / safely edit sudoers. |
| `login` / `logname` / `nologin` | Login shell helpers. |
| `unsetpassword` | Reset a user's keychain-stored sync password. |
| `kerberos kinit` family | (See Networking — Kerberos.) |
| `sc_auth` | Manage smart-card authentication binding. |
| `PasswordService` / `authserver` | macOS authentication background services. |
| `sso_util` / `app-sso` | Manage Apple Single Sign-On extensions. |
| `securityd` / `security` * | Keychain & cryptographic services daemon and CLI. |
| `systemkeychain` | Manage the system keychain. |
| `keychain-access` | CLI front-end for keychain operations. |

---

## 10. Security, Cryptography & Code Signing

| Command | Description |
| --- | --- |
| `security` * | Swiss-army CLI for keychains, certificates, identities, and trust settings. |
| `codesign` * | Signs, verifies, and inspects code signatures on Mach-O binaries and bundles. |
| `codesign_allocate` | Helper that allocates code-signature load commands. |
| `csreq` | Compiles/decompiles Code Signing Requirements language. |
| `spctl` * | Gatekeeper policy assessor. |
| `notarytool`-like via `xcrun notarytool` | Notarization (invoked via `xcrun`, not a top-level binary). |
| `pluginkit` | Manage NSExtension / PlugInKit registrations. |
| `tccutil` * | Reset privacy (TCC) permissions per service or bundle. |
| `sandbox-exec` | Run a command inside a sandbox profile. |
| `sfltool` | Manage Shared File Lists (Login Items, Recent Items). |
| `xartutil` | Manage the Apple Secure Token "XART" trust store. |
| `ckksctl` | iCloud Keychain CKKS service control. |
| `crlrefresh` | Refresh local CRL/OCSP caches. |
| `ocspcheck` / `ocspd` | OCSP validation client and daemon. |
| `trustcachectl` | Manage immutable trust caches (Apple Silicon). |
| `weakpass_edit` | Edit the system "weak passwords" deny list. |
| `DevToolsSecurity` | Toggle Xcode developer-tools authorization. |
| `certtool` / `crlutil`-via-`security` | Certificate management. |
| `openssl` * | General-purpose TLS/crypto toolkit (LibreSSL on macOS). |
| `ssh-keygen` * | Generate/manage SSH keys. |
| `gpg`-equivalent | (Not native; install via Homebrew.) |
| `shasum` * / `sha1sum` / `sha256sum` / `sha512sum` / `sha1` / `sha256` / `md5` / `md5sum` / `cksum` / `sum` / `crc32` | Compute message digests/checksums. |
| `base64` * / `b64encode` / `b64decode` / `uuencode` / `uudecode` / `binhex` / `binhex.pl` / `debinhex.pl` | Binary↔text encoding utilities. |
| `pcsctest` | Tests the PC/SC smart-card framework. |
| `audit` / `auditd` / `auditreduce` / `praudit` | macOS audit subsystem (BSM). |
| `eslogger` | Stream EndpointSecurity events to stdout. |
| `xscertadmin` | Xcode Server certificate admin. |

---

## 11. Logging, Diagnostics & Observability

| Command | Description |
| --- | --- |
| `log` * | Apple unified logging CLI (`log show`, `log stream`, `log collect`). |
| `syslog` * / `syslogd` | Legacy ASL log query and daemon. |
| `logger` * | Write a message into the system log. |
| `newsyslog` | Rotate log files based on `/etc/newsyslog.conf`. |
| `aslmanager` | Manage the on-disk Apple System Log database. |
| `dmesg` * | Print kernel-ring-buffer messages. |
| `notifyutil` / `notifyd` | Send/observe `notify(3)` keys. |
| `distnoted` | Distributed notification daemon. |
| `errinfo` / `macerror` / `macoserror` / `dserr` | Look up numeric error codes to symbolic names. |
| `taskinfo` / `lsmp` / `lskq` | (See Process Management.) |
| `instruments`-equiv via `xctrace` | Run Instruments traces from CLI. |
| `viewdiagnostic` | Inspect a `.ips` crash report. |
| `symbols` / `symbolscache` | Resolve symbols from the symbol cache. |
| `atos` * | Convert numeric addresses into symbol names for a binary. |
| `stringdups` | Find duplicate strings across binaries. |
| `filtercalltree` | Filter Instruments call-tree output. |
| `traptoemail` | SNMP-trap-to-email gateway helper. |

---

## 12. Shells, Scripting & Built-in Languages

| Command | Description |
| --- | --- |
| `zsh` * | The default macOS interactive shell. |
| `bash` * | GNU Bourne-Again shell (legacy 3.2 on macOS). |
| `sh` * | POSIX shell (links to `bash`). |
| `dash` | Lightweight POSIX shell. |
| `csh` / `tcsh` / `ksh` | Additional Unix shells bundled with macOS. |
| `env` * | Run a program in a modified environment (also `#!/usr/bin/env`). |
| `printenv` * / `export` (built-in) | View or set environment variables. |
| `alias` / `unalias` / `command` / `type` / `which` * / `whereis` / `hash` | Shell command-resolution helpers. |
| `set` / `unset` / `read` (built-ins) | Shell variable manipulation. |
| `getopt` / `getopts` | Argument parsing helpers. |
| `expr` / `test` / `true` / `false` / `[` | Shell conditional/arithmetic primitives. |
| `printf` * / `echo` * | Formatted output. |
| `seq` / `jot` / `yes` | Generators for sequences/streams. |
| `xargs` * | Build and execute command lines from stdin. |
| `parallel`-equivalent | (Not native; install via Homebrew.) |
| `perl` * (5.34) and `perl5.34` | Perl interpreter + companion tools (`prove`, `cpan`, `pod2*`, etc.). |
| `python3` * | Apple-provided Python 3 (Command Line Tools). |
| `ruby` * + `gem`, `irb`, `rake`, `erb`, `rails`, `rdoc`, `ri`, `bundle`, `bundler` | Ruby toolchain (legacy 2.6). |
| `tclsh` / `tclsh8.5` / `wish` / `wish8.5` / `tkcon` | Tcl/Tk interpreters. |
| `awk` * / `sed` * / `m4` / `gm4` | Text-processing mini-languages. |
| `bc` / `dc` / `units` | Arbitrary-precision and unit-conversion calculators. |
| `jq` * | JSON command-line processor. |
| `plutil` * | Convert/validate/edit `.plist` files between binary/XML/JSON formats. |
| `defaults` * | Read/write the macOS user-defaults (preferences) database. |
| `osascript` * / `osacompile` / `osadecompile` / `osalang` | Run/compile AppleScript and JavaScript-for-Automation. |
| `shortcuts` * | Run macOS Shortcuts from the command line. |
| `automator` | Run Automator workflows. |
| `expect` | Automate interactive TTY programs. |
| `lex` / `flex` / `flex++` / `yacc` / `bison` / `gperf` / `indent` | Compiler-construction helpers. |

---

## 13. Developer Toolchain (Xcode Command Line Tools)

| Command | Description |
| --- | --- |
| `xcode-select` * | Sets/queries the active developer directory. |
| `xcrun` * | Locates and invokes tools inside the active Xcode SDK. |
| `xcodebuild` * | Builds Xcode projects/workspaces from the command line. |
| `xcdebug` / `xctrace` | Debug/trace helpers paired with Xcode. |
| `xcscontrol` / `xcsdiagnose` / `xscertadmin` | Xcode Server administration. |
| `clang` * / `clang++` / `cc` / `c++` / `cpp` / `gcc` / `g++` / `llvm-gcc` / `llvm-g++` / `c89` / `c99` | C/C++ compiler front-ends. |
| `clangd` | clang language-server (LSP). |
| `swift` * / `swiftc` / `swift-inspect` | Swift REPL/compiler. |
| `sourcekit-lsp` | Swift language server. |
| `ld` * / `lipo` * / `ar` / `ranlib` / `nm` / `strip` / `size` / `otool` * / `dyld_info` / `install_name_tool` / `dwarfdump` / `dsymutil` / `objdump` / `vtool` / `segedit` / `pagestuff` / `cmpdylib` / `ctf_insert` / `libtool` / `c++filt` | Mach-O linker, archive, and binary-inspection utilities. |
| `as` | Assembler. |
| `make` * / `gnumake` | Build automation. |
| `lldb` * | The LLVM debugger. |
| `git` * (+ `git-receive-pack`, `git-shell`, `git-upload-archive`, `git-upload-pack`) | Distributed version control. |
| `ctags` | Generates tag indexes for source code. |
| `diff` / `diff3` / `sdiff` / `patch` | (See Filesystem.) |
| `pip3` * | Python package manager. |
| `java` * / `javac` / `javap` / `javadoc` / `jar` / `jarsigner` / `jcmd` / `jconsole` / `jdb` / `jdeps` / `jhsdb` / `jimage` / `jinfo` / `jlink` / `jmap` / `jpackage` / `jps` / `jrunscript` / `jshell` / `jstack` / `jstat` / `jstatd` / `keytool` / `pack200` / `unpack200` / `policytool` / `rmic` / `rmid` / `rmiregistry` / `serialver` / `servertool` / `jjs` / `orbd` / `tnameserv` / `javaws` | Bundled JDK toolchain (if Java is installed). |
| `actool` / `ibtool` / `iconutil` / `tiff2icns` / `layerutil` / `gen_bridge_metadata` / `mig` / `genstrings` / `sdef` / `sdp` / `xed` | Asset, interface-builder, and Cocoa SDK helpers. |
| `safaridriver` | WebDriver server for Safari. |
| `xartutil` / `assetutil` / `derq` / `dyld_usage` | Additional dev/diagnostics tools. |
| `headerdoc2html` / `gatherheaderdoc` / `hdxml2manxml` / `xml2man` / `tidy` / `tidy_changelog` | Documentation helpers. |
| `swiftdriver` aliases via `xcrun` | (Driver routing via `xcrun`.) |
| `productbuild` / `productsign` / `pkgbuild` / `pkgutil` | (See Packaging.) |
| `instmodsh` / `cpan` / `perlbug` / `perldoc` | Perl ecosystem helpers. |
| `xcdebug` | Xcode debug helper. |

---

## 14. macOS Application & Workspace Control

| Command | Description |
| --- | --- |
| `open` * | Opens files, URLs, or `.app` bundles using Launch Services. |
| `defaults` * | Read/write app preferences (`~/Library/Preferences/*.plist`). |
| `pluginkit` | Inspect/enable/disable app extensions. |
| `shortcuts` * | Run Shortcuts workflows. |
| `automator` | Execute Automator workflows. |
| `osascript` * | Run AppleScript or JXA scripts. |
| `pbcopy` * / `pbpaste` * | Read/write the macOS pasteboard (clipboard). |
| `say` * | Speak text using macOS TTS voices. |
| `afplay` * | Play an audio file. |
| `screencapture` * | Capture screenshots (file or clipboard) from the CLI. |
| `qlmanage` | Generate Quick Look thumbnails/previews. |
| `sips` * | Scriptable Image Processing (resize/convert images, edit ICC profiles). |
| `textutil` * | Convert documents between rtf/txt/html/doc/docx/odt. |
| `tiffutil` | Manipulate multi-page/multi-DPI TIFFs. |
| `iconutil` | Convert between `.iconset` folders and `.icns` files. |
| `xattr` * | Manage extended attributes incl. quarantine flag for downloaded apps. |
| `mdfind` * / `mdls` / `mdimport` / `mdutil` | Spotlight queries and indexing. |
| `tmutil` * | Time Machine backups CLI (snapshots, exclusions, restore). |
| `fileproviderctl` | Manage File Provider extensions (iCloud Drive, Dropbox, etc.). |
| `brctl` | iCloud (Brain Cloud) sync diagnostics. |
| `swcutil` | Inspect Universal Links / Apple App Site Association cache. |
| `usernoted` | User Notification Center daemon. |
| `keychain-access` | (See Security.) |
| `appleh13camerad` / `appleh16camerad` / `coreaudiod` / `systemsoundserverd` / `graphicssession` / `filecoordinationd` / `cfprefsd` | Background daemons (informational, not for direct user invocation). |
| `automationmodetool` | Toggle the "Automation" privacy state. |
| `tccutil` * | Reset TCC privacy permissions. |
| `mcxquery` / `mcxrefresh` | Query/refresh Managed Client (MDM) preferences. |
| `localemanager` / `languagesetup` / `setregion` | Manage system locale, language, and region. |
| `locale` / `localedef` / `mklocale` / `colldef` | POSIX locale management. |

---

## 15. Audio, Video & Media

| Command | Description |
| --- | --- |
| `afplay` * | Play audio files. |
| `afconvert` * | Convert audio between formats / sample rates. |
| `afinfo` | Print audio file metadata. |
| `afclip` / `afhash` / `afida` / `afktool` / `afscexpand` | Specialty audio utilities. |
| `auval` / `auvaltool` | Validate Audio Unit plug-ins. |
| `say` * | Text-to-speech synthesis. |
| `shazam` | Identify songs via the Shazam framework. |
| `avconvert` | Convert AVFoundation media (e.g., video transcoding). |
| `avmediainfo` | Inspect AV media files. |
| `avmetareadwrite` | Read/write metadata on AV files. |
| `avbutil` / `avbanalyse` / `avbdeviced` / `avbdiagnose` | Audio Video Bridging (Ethernet AV) tools. |
| `sips` * | Image processing/conversion. |
| `tiff2icns` / `tiffutil` / `iconutil` | Image format converters. |
| `mpsgraphtool` | Metal Performance Shaders graph utility. |
| `usdcat` / `usdchecker` / `usdcrush` / `usdextract` / `usdrecord` / `usdtree` / `usdzip` | Pixar USD (3D scene) toolchain (used for AR Quick Look). |

---

## 16. Printing (CUPS)

| Command | Description |
| --- | --- |
| `lp` / `lpr` * | Submit a file to a printer. |
| `lpq` / `lpstat` * | Query print queue status. |
| `lprm` | Cancel print jobs. |
| `lpadmin` / `lpc` / `lpinfo` / `lpmove` / `lpoptions` / `cupsaccept` / `cupsreject` / `cupsenable` / `cupsdisable` / `cupsctl` / `cupsd` / `cupsfilter` / `cupstestppd` / `cups-config` / `ppdc` / `ppdhtml` / `ppdi` / `ppdmerge` / `ppdpo` / `ippeveprinter` / `ippfind` / `ipptool` | CUPS administration, driver, and IPP tools. |
| `cancel` | Cancel a print job (CUPS). |

---

## 17. Mail & Messaging

| Command | Description |
| --- | --- |
| `mail` / `mailx` * | Send/receive email from the terminal. |
| `sendmail` * / `newaliases` / `mailq` | Mail transfer agent front-end. |
| `postfix` (+ `postalias`, `postcat`, `postconf`, `postdrop`, `postkick`, `postlock`, `postlog`, `postmap`, `postmulti`, `postqueue`, `postsuper`) | Postfix MTA administration. |

---

## 18. Editors & Pagers

| Command | Description |
| --- | --- |
| `vi` * / `vim` * / `view` / `vimdiff` / `rvim` / `rview` / `vimtutor` / `ex` | Vi/Vim editor family. |
| `nano` * / `pico` | Beginner-friendly modeless editor. |
| `ed` / `mg` | Classic line editor / lightweight micro-Emacs. |
| `less` * / `more` / `bzless` / `zless` / `lessecho` | Pagers (incl. compressed-file aware variants). |
| `head` / `tail` / `cat` | (See Filesystem.) |
| `opendiff` | Launch FileMerge for graphical diff. |
| `xed` | Open files in Xcode from CLI. |

---

## 19. Time, Date & Scheduling

| Command | Description |
| --- | --- |
| `date` * | Display or set the system date and time. |
| `cal` * / `ncal` / `calendar` | Display calendars / reminders. |
| `zdump` / `zic` | Print/compile timezone data. |
| `at` / `atq` / `atrm` / `batch` | One-shot job scheduling. |
| `cron` / `crontab` * | Recurring scheduled jobs. |
| `launchctl` * | Modern scheduled/agent jobs (`launchd`). |
| `leave` | Remind you to leave at a given time. |
| `sntp` / `sntpd` | Simple NTP client/daemon. |
| `systemsetup -setnetworktimeserver` | Configure NTP server via `systemsetup`. |

---

## 20. Database & Data-Store Utilities

| Command | Description |
| --- | --- |
| `sqlite3` * | Interactive CLI for SQLite databases (used heavily by macOS internals). |
| `db_*` (`db_dump`, `db_load`, `db_recover`, `db_stat`, `db_verify`, `db_checkpoint`, `db_archive`, `db_codegen`, `db_deadlock`, `db_hotbackup`, `db_printlog`, `db_upgrade`) | Berkeley DB administration tools. |
| `dbmmanage` | Manage Apache HTTP basic-auth DBM files. |
| `cap_mkdb` / `dev_mkdb` / `pwd_mkdb` | Build hashed termcap/devices/password databases. |
| `gencat` | Generate formatted message catalog. |

---

## 21. Web / HTTP Servers

| Command | Description |
| --- | --- |
| `httpd` * | Apache HTTP Server binary (bundled, not started by default). |
| `apachectl` * | Apache control script (start/stop/graceful). |
| `htpasswd` / `htdigest` / `htdbm` / `htcacheclean` / `httxt2dbm` / `rotatelogs` / `fcgistarter` / `httpd-wrapper` / `envvars` / `envvars-std` / `logresolve` / `ab` | Apache helper utilities. |

---

## 22. Help, Documentation & Discovery

| Command | Description |
| --- | --- |
| `man` * / `mandoc` / `apropos` * / `whatis` / `manpath` / `demandoc` / `mandoc_soelim` | Manual-page system. |
| `info`-equivalent | (Not bundled; use `man`.) |
| `help` (shell built-in) | Built-in help for shell commands. |
| `tldr`-equivalent | (Not bundled; install via Homebrew.) |

---

## Bundled Third-Party / Background Daemons (Reference)

Many binaries under `/usr/sbin` and `/usr/libexec` are background daemons or component-specific tools never meant for direct user invocation (e.g., `coreaudiod`, `bluetoothd`, `securityd`, `cfprefsd`, `usernoted`, `IOMFB_FDR_Loader`, `appleh13camerad`). The MCP server should **deny-list** these from being directly invokable.

A representative (non-exhaustive) list: `appsleepd`, `automountd`, `BTLEServer`, `BTLEServerAgent`, `coreaudiod`, `cfprefsd`, `cron`, `cupsd`, `distnoted`, `DirectoryService`, `filecoordinationd`, `graphicssession`, `gssd`, `httpd`, `KernelEventAgent`, `launchd`, `mDNSResponder`, `mDNSResponderHelper`, `netbiosd`, `nfsd`, `notifyd`, `ocspd`, `PasswordService`, `rpcbind`, `securityd`, `smbd`, `snmpd`, `sntpd`, `spfd`, `sshd`, `syslogd`, `systemsoundserverd`, `universalaccessd`, `usernoted`, `WirelessRadioManagerd`.

---

# Suggested MCP Tooling Roadmap

The macOS MCP server should expose tightly typed, schema-validated tools — not a raw "run any shell command" tool. Prioritize categories where (a) the user value is high, (b) commands accept well-defined arguments, (c) machine-readable output (`-json`, `-xml`, `-plist`) is available, and (d) the risk surface is manageable through the two-phase Stage/Commit pattern defined in [.claude/rules/transactional-state.md](.claude/rules/transactional-state.md).

## Phase 1 — Start here (high value, low risk, read-mostly)

These are mostly read-only or fully reversible operations with native machine-readable output. They form a strong demo surface and prove the tokenized execution pipeline end-to-end.

1. **System Information** — `sw_vers`, `uname`, `system_profiler -json`, `sysctl`, `ioreg -a`, `hostinfo`, `uptime`, `vm_stat`, `pmset -g`, `powermetrics` (capped), `arch`, `machine`.
   *Why:* Stable schemas, supports `-json`/`-xml`, no mutation risk.
2. **Filesystem Inspection** — `ls`, `stat`, `file`, `find`, `du`, `df`, `mdfind`, `mdls`, `xattr`, `readlink`, `realpath`.
   *Why:* Most-asked-for capability for AI agents; easy to harden via path validation.
3. **Process & Resource Inspection** — `ps`, `top -l 1`, `lsof`, `pgrep`, `nettop -L 1 -J`, `fs_usage` (timeboxed), `lsappinfo`.
   *Why:* Common diagnostic prompts ("what's using my CPU?"). Read-only.
4. **Networking — Diagnostics** — `ping`, `traceroute`, `dig`, `host`, `nslookup`, `whois`, `netstat`, `networkQuality`, `ifconfig`, `ipconfig getifaddr`, `scutil --dns`, `arp -a`.
   *Why:* Bounded outputs, classic agent use-cases ("is github.com reachable?").
5. **Pasteboard / Speech / Screenshot Convenience** — `pbcopy`, `pbpaste`, `say`, `screencapture`, `open`, `qlmanage`.
   *Why:* Delightful UX wins; pasteboard and `open` are the single most natural bridge between an LLM and the desktop.
6. **macOS Preferences & Defaults (read)** — `defaults read`, `plutil -convert json`, `profiles list`.
   *Why:* JSON-native via `plutil`; lays the foundation for future write tools.

## Phase 2 — Add once Phase 1 is solid (moderate risk, mutating, requires Stage/Commit)

1. **App & Workspace Control** — `open`, `osascript`, `shortcuts run`, `automator`, `tccutil reset`, `pluginkit`.
2. **Media Conversion** — `sips`, `afconvert`, `iconutil`, `textutil`, `tiffutil`.
3. **Archives & Packaging** — `tar`, `ditto`, `zip`/`unzip`, `xar`, `pkgutil`, `installer -pkginfo`.
4. **Power & Sleep Controls** — `caffeinate`, `pmset` write subcommands, `systemsetup` (read-then-write), `softwareupdate --list`.
5. **Spotlight Index Control** — `mdutil`, `mdimport`.
6. **Time Machine** — `tmutil` (snapshots and listing first; deletion only via Stage/Commit with Trash fallback).
7. **Network Configuration (read+limited write)** — `networksetup -listallnetworkservices`, set DNS / proxy with confirmation, `wdutil info`.

## Phase 3 — Highly destructive / privileged (defer; require explicit elevated trust + dry-run preview)

1. **Disk & Volume Management** — `diskutil` (especially `eraseDisk`, `apfs deleteContainer`), `hdiutil`, `fsck_*`, `newfs_*`, `bless`, `fdesetup`.
2. **Kernel Extensions / System Extensions** — `kextload`, `kextunload`, `kmutil`, `systemextensionsctl`.
3. **Security Posture** — `spctl`, `csrutil` (Recovery-only), `bputil`, `firmwarepasswd`, `nvram` writes, `xprotect`.
4. **User / Directory Mutations** — `sysadminctl`, `dscl . -create/-delete`, `dseditgroup`, `passwd`, `pwpolicy`.
5. **Filesystem Destructive Ops** — `rm -rf`, `chflags`, `chown`, `chmod -R` — route deletions through `~/.Trash` (see [.claude/rules/transactional-state.md](.claude/rules/transactional-state.md)) and require Stage/Commit.

## Cross-cutting recommendations

- **Prefer structured-output flags whenever they exist:** `-json`, `-xml`, `-plist` (then convert via `plutil -convert json -o - -- -`). Avoid `grep`/`sed`/`awk` scraping.
- **Pin absolute binary paths** (`/bin`, `/sbin`, `/usr/bin`, `/usr/sbin`) per [.claude/rules/darwin-execution.md](.claude/rules/darwin-execution.md) so a poisoned `PATH` cannot substitute a rogue executable.
- **Cap stdout payloads** at ~8 KB (head 4K + tail 4K + truncation marker) to protect the LLM context window.
- **Tokenized arguments only** — never assemble shell strings; pass each flag/value as a discrete element of `[]string`.
- **Allow-list, not deny-list.** Maintain an explicit registry of supported commands keyed to typed argument structs (`jsonschema` tags) rather than letting the LLM specify arbitrary binary paths.
- **Bake Stage/Commit into every mutating tool** so the LLM proposes an action, the server returns a `req_…` token + risk score, and the user/client explicitly commits.

Recommended first concrete tools to implement (in order):
`system_info` → `filesystem_list` → `filesystem_search` (Spotlight) → `process_list` → `network_ping` → `network_dns_lookup` → `pasteboard_read` / `pasteboard_write` → `defaults_read` → `open_url_or_path` → `screenshot_capture`.
