<div align="center">

# playlistsync

Two-way playlist synchronization between Spotify and YouTube Music.

[English](README.md) | [简体中文](README_zh.md)

[![Version](https://img.shields.io/badge/version-v1.0.0-2ea44f?style=flat-square)](https://github.com/popu2do/playlistsync/releases)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-555555?style=flat-square)](https://github.com/popu2do/playlistsync/releases)
[![License](https://img.shields.io/badge/License-CC%20BY--NC--ND%204.0-lightgrey?style=flat-square)](LICENSE)

</div>

---

## Overview

`playlistsync` is a lightweight command-line tool for synchronizing playlists bidirectionally between Spotify and YouTube Music.

Most commercial transfer services charge recurring monthly subscriptions or require handing over account credentials to third-party servers. `playlistsync` runs entirely on your local machine—authenticating directly through your local browser with no cloud intermediaries or data collection.

To handle metadata discrepancies across platforms, it includes automated title cleaning, CJK Traditional/Simplified conversion, and multi-factor duration/artist verification to preserve original tracks and track order.

---

## Features

- **Local Execution & Privacy**: Authenticates directly via your local browser. Credentials are saved strictly inside `output/auth/` on your own machine.
- **Conservative Fuzzy Matching**: Strips metadata noise (`MV`, `Live`, `4K`, `Remastered`, etc.), normalizes CJK text, and enforces strict duration tolerances to avoid matching covers or fan edits.
- **Preserves Original Order**: Retains source sequence by default and supports incremental synchronization and pruning extraneous destination tracks.
- **Interactive Review**: Prompts in the terminal when match confidence is borderline, allowing manual confirmation or custom URL input.
- **Self-Contained Binary**: Zero external runtime dependencies (no Python or Node.js required), with automatic system proxy detection.

---

## Installation

Download the precompiled archive for your platform from [Releases](https://github.com/popu2do/playlistsync/releases):

| Platform | Archive | Description |
| :--- | :--- | :--- |
| **Windows** | `playlistsync-windows-amd64.zip` | 64-bit Windows PC |
| **macOS** | `playlistsync-darwin-arm64.tar.gz`<br>`playlistsync-darwin-amd64.tar.gz` | Apple Silicon (M-series)<br>Intel Mac |
| **Linux** | `playlistsync-linux-amd64.tar.gz` | 64-bit Linux |

---

## Usage

### 1. Authentication (First Run)

Opens the official login page in your default browser to extract and store credentials locally:

```bash
# Authenticate platforms
./playlistsync login [all|spotify|youtube-music]
```

### 2. Synchronize Playlists

```bash
# Sync from Spotify to YouTube Music
./playlistsync sync spotify:"Playlist Name" ytm:

# Sync from YouTube Music to Spotify
./playlistsync sync ytm:"Playlist Name" spotify:

# Direct playlist share URL
./playlistsync sync https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M ytm:

# Specify local proxy if needed
./playlistsync sync spotify:"Playlist Name" ytm: --proxy=http://127.0.0.1:7890

# Non-interactive mode (default strategy)
./playlistsync sync spotify:"Playlist Name" ytm: -y
```

---

## Commands

| Command | Description |
| :--- | :--- |
| `playlistsync login [platform]` | Authenticate platform session (`spotify` / `youtube-music` / `all`) |
| `playlistsync sync <spec_from> <spec_to>` | Synchronize playlist across platforms |
| `playlistsync inspect <name>` | Display migration status and match breakdown |
| `playlistsync verify <name>` | Verify data integrity and sync invariants |
| `playlistsync report <name>` | Regenerate audit report JSON |

---

## FAQ

**Q: Where are credentials stored and how do I log out?**  
A: Session data is kept locally under `output/auth/`. To log out, delete that folder or run `playlistsync login <platform> --force`.

**Q: Connection timeout or proxy configuration?**  
A: The CLI automatically uses system proxy settings. You can also explicitly specify a proxy via `--proxy=http://127.0.0.1:7890`.

---

## ☕ Support

If this tool saved you time or a monthly subscription fee, feel free to buy me a coffee to support continued maintenance.

<div align="center">
  <table>
    <tr>
      <td align="center">
        <img src="assets/wechat-pay.png" width="180" alt="WeChat Pay" /><br />
        <sub>WeChat Pay</sub>
      </td>
      <td align="center">
        <img src="assets/ali-pay.png" width="180" alt="Alipay" /><br />
        <sub>Alipay</sub>
      </td>
    </tr>
  </table>
</div>

---

## License

[CC BY-NC-ND 4.0](LICENSE). Free for personal use. All data stays local.
