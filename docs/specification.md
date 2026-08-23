# Engineering Specification: playlistsync (Go)

## 1. CLI Interface Specification

### 1.1 Binary: `playlistsync`

The primary CLI executable is `bin/playlistsync` (compiled from `cmd/playlistsync/main.go`).

```bash
playlistsync <command> [arguments] [options]
```

### 1.2 Subcommands

| Subcommand | Purpose | Parameters |
| :--- | :--- | :--- |
| `sync <playlist_name>` | Synchronize a playlist bidirectionally between platforms (`spotify` <-> `youtube-music`) | Positional: `<playlist_name>` (Required)<br>Flags: `--from`, `--to`, `--proxy`, `--clean-extra`, `--yes` / `-y`, `--non-interactive`, `--concurrency` / `-c`, `--output-dir`, `--json`, `--playlist-id` / `--id` |
| `login [platform]` | Authenticate credentials via automated browser CDP flow (`spotify`, `youtube-music`, `all`) | Positional: `[platform]` (Optional, defaults to `all`)<br>Flags: `--force`, `--proxy` |
| `inspect <playlist_name>` | Display human-readable migration summary and track status (alias: `status`, `summary`) | Positional: `<playlist_name>` (Required)<br>Flags: `--output-dir` |
| `verify <playlist_name>` | Run invariant integrity checks on source, result, and report datasets (alias: `validate`) | Positional: `<playlist_name>` (Required)<br>Flags: `--output-dir` |
| `report <playlist_name>` | Regenerate canonical migration report JSON artifact | Positional: `<playlist_name>` (Required)<br>Flags: `--output-dir` |

### 1.3 Command Options & Flags

| Flag / Option | Shorthand | Default Value | Scope | Description |
| :--- | :--- | :--- | :--- | :--- |
| `--from` | - | `spotify` | `sync` | Source platform (`spotify` or `youtube-music`; aliases: `spo`, `sp`, `youtube`, `ytmusic`, `ytm`, `yt`) |
| `--to` | - | `youtube-music` | `sync` | Destination platform (`youtube-music` or `spotify`) |
| `--proxy` | - | System auto-detect | `sync`, `login` | Explicit HTTP/HTTPS proxy URL (overrides environment and registry) |
| `--clean-extra` | - | `true` | `sync` | When true, automatically prunes unmapped extraneous tracks in destination playlist |
| `--yes` | `-y` | `false` | `sync` | Automatically answer yes to all confirmation prompts |
| `--non-interactive`| - | `false` | `sync` | Run in non-interactive batch mode (equivalent to auto-confirm) |
| `--concurrency` | `-c` | `6` | `sync` | Number of concurrent worker goroutines for track candidate search & matching |
| `--output-dir` | - | `output/` | Global | Custom working and artifact storage directory |
| `--json` | - | `""` | `sync` | Direct path to local source playlist JSON file |
| `--playlist-id` / `--id` | - | `""` | `sync` | Explicit destination playlist ID to synchronize into |
| `--force` | - | `false` | `login` | Force re-authentication bypassing cached credential validation |

### 1.4 Exit Codes

| Exit Code | Semantic Meaning |
| :--- | :--- |
| `0` | Successful execution / Validation passed / Help displayed |
| `1` | Command execution error, invariant check failure, or unrecoverable error |
