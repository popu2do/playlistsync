# Engineering Specification: playlistsync

## 1. CLI Interface Specification

### 1.1 Executable Binary

The primary CLI binary is `playlistsync` (built from `cmd/playlistsync/main.go`).

```bash
playlistsync <command> [arguments] [options]
```

### 1.2 Subcommands

| Subcommand | Aliases | Description | Positional Arguments |
| :--- | :--- | :--- | :--- |
| `sync` | - | Synchronize a playlist between platforms with differential reconciliation | `<playlist_name>` (Required) |
| `login` | - | Authenticate credentials via automated browser CDP flow | `[platform]` (Optional: `spotify`, `youtube-music`, `all`; default: `all`) |
| `inspect` | `status`, `summary` | Display human-readable migration summary and track status | `<playlist_name>` (Required) |
| `verify` | `validate` | Run invariant integrity checks on source, result, and report datasets | `<playlist_name>` (Required) |
| `report` | - | Regenerate canonical migration report JSON artifact | `<playlist_name>` (Required) |
| `web` | - | Start the local-only visual web cockpit with embedded React SPA | None |

### 1.3 Flag Matrix

| Flag / Option | Shorthand | Default Value | Applicable Subcommands | Description |
| :--- | :--- | :--- | :--- | :--- |
| `--from` | - | `""` | `sync` | Source platform (`spotify`, `youtube-music`; aliases: `spo`, `sp`, `youtube`, `ytmusic`, `ytm`, `yt`) |
| `--to` | - | `""` | `sync` | Destination platform (`youtube-music`, `spotify`) |
| `--source` | `--from-id`, `--source-id` | `""` | `sync` | Source playlist name, ID, or URL |
| `--target` | `--to-id`, `--target-id`, `--playlist-id`, `--id` | `""` | `sync` | Destination playlist name, ID, or URL |
| `--proxy` | - | System auto-detect | `sync`, `login` | Explicit HTTP/HTTPS proxy URL (e.g. `http://127.0.0.1:7890`) |
| `--clean-extra` | - | `true` | `sync` | Automatically prune unmapped extraneous tracks from the destination playlist |
| `--sync-order` | - | `true` | `sync` | Preserve and synchronize exact playlist track sequence across platforms |
| `--yes` | `-y` | `false` | `sync` | Automatically answer yes to all confirmation prompts |
| `--non-interactive` | - | `false` | `sync` | Run in non-interactive batch mode (equivalent to auto-confirm) |
| `--concurrency` | `-c` | `6` | `sync` | Number of concurrent worker goroutines for track search and candidate evaluation |
| `--output-dir` | - | `output/` | `sync`, `inspect`, `verify`, `report` | Custom working and artifact storage directory |
| `--json` | - | `""` | `sync` | Direct path to a local source playlist JSON file |
| `--force` | - | `false` | `login` | Force interactive browser re-authentication, bypassing cached credential validation |
| `--port` | `-p` | `0` | `web` | HTTP port to bind for the local web cockpit (0 = auto-select `3080-3089` or ephemeral) |

### 1.4 Exit Codes

| Exit Code | Semantic Meaning | Scenarios |
| :--- | :--- | :--- |
| `0` | Success | Command completed successfully, validation checks passed, or help information displayed |
| `1` | Failure | Execution error, network/auth failure, invalid arguments, or invariant verification failure |
