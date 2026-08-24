<div align="center">

# playlistsync

Spotify 与 YouTube Music 歌单双向同步工具

[English](README.md) | [简体中文](README_zh.md)

[![Version](https://img.shields.io/badge/version-v1.0.0-2ea44f?style=flat-square)](https://github.com/popu2do/playlistsync/releases)
[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat-square&logo=go)](https://golang.org)
[![Platform](https://img.shields.io/badge/Platform-Windows%20%7C%20macOS%20%7C%20Linux-555555?style=flat-square)](https://github.com/popu2do/playlistsync/releases)
[![License](https://img.shields.io/badge/License-CC%20BY--NC--ND%204.0-lightgrey?style=flat-square)](LICENSE)

</div>

---

## 简介

`playlistsync` 是一个用于在 Spotify 与 YouTube Music 之间双向同步歌单的命令行工具。

市面上多数歌单迁移服务普遍采用按月订阅制，或需要将账号凭据上传至第三方服务器。本项目完全在本地运行，无云端中转，直接在本地浏览器完成官方登录与歌单迁移。

针对跨平台曲目命名的差异，内置了文本清洗、繁简转换与时长校验机制，尽可能还原原版曲目与歌单顺序。

---

## 特性

- **本地运行**：通过本地浏览器完成官方页面登录，凭据仅保存在本地 `output/auth/`，绝不上传外部服务器。
- **智能模糊匹配**：自动剔除 `MV`、`Live`、`4K`、`Remastered` 等噪音后缀，内置中日韩繁简转换，结合时长校验避免匹配为翻唱或混剪。
- **保持原始顺序与增量同步**：默认按原歌单顺序同步；目标端已存在歌单时支持比对增量与修剪多余曲目。
- **交互式复核**：对低置信度曲目提供终端交互选项，可手动确认或粘贴自定义链接。
- **单文件交付**：无 Python / Node.js 等运行时依赖，下载即用，自动识别系统代理。

---

## 安装

从 [Releases](https://github.com/popu2do/playlistsync/releases) 下载：

| 平台 | 文件 | 说明 |
| :--- | :--- | :--- |
| **Windows** | `playlistsync-windows-amd64.zip` | 64 位 Windows PC |
| **macOS** | `playlistsync-darwin-arm64.tar.gz`<br>`playlistsync-darwin-amd64.tar.gz` | Apple Silicon (M 系列)<br>Intel 芯片 |
| **Linux** | `playlistsync-linux-amd64.tar.gz` | 64 位 Linux |

---

## 使用

### 1. 账号登录（首次使用）

调用系统默认浏览器打开官方登录页，登录成功后自动将授权凭据存至本地：

```bash
# 登录音乐平台获取凭证
./playlistsync login [all|spotify|youtube-music]
```

### 2. 同步歌单

```bash
# 从 Spotify 同步到 YouTube Music
./playlistsync sync spotify:"歌单名称" ytm:

# 从 YouTube Music 同步到 Spotify
./playlistsync sync ytm:"歌单名称" spotify:

# 也可以直接粘贴歌单分享链接
./playlistsync sync https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M ytm:

# 如需使用 proxy
./playlistsync sync spotify:"歌单名称" ytm: --proxy=http://127.0.0.1:7890

# 非交互式，按默认策略同步
./playlistsync sync spotify:"歌单名称" ytm: -y
```

---

## 命令一览

| 命令 | 说明 |
| :--- | :--- |
| `playlistsync login [platform]` | 账号授权登录（`spotify` / `youtube-music` / `all`） |
| `playlistsync sync <歌单名称> [options]` | 执行歌单同步迁移（支持 `--from` 与 `--to`） |
| `playlistsync inspect <歌单名称>` | 查看指定歌单的迁移状态与匹配详情 |
| `playlistsync verify <歌单名称>` | 校验数据完整性与同步一致性 |
| `playlistsync report <歌单名称>` | 重新生成本地 JSON 审计报告 |

---

## 常见问题

**Q: 账号凭据保存在哪里？如何退出？**  
A: 所有凭据均保存在本地 `output/auth/` 目录下。如需清除，直接删除该目录或执行 `playlistsync login <platform> --force` 重新授权。

**Q: 国内网络环境下连接超时怎么办？**  
A: cli默认会走系统代理。或可通过 `--proxy` 参数指定本地端口（例如 `--proxy=http://127.0.0.1:7890`）。

---

## ☕ 支持与赞助

如果这个小工具帮到了你，或者帮你省下了一笔订阅费，欢迎请我喝瓶可乐或者咖啡支持一下后续维护。

<div align="center">
  <table>
    <tr>
      <td align="center">
        <img src="assets/wechat-pay.png" width="180" alt="微信赞助" /><br />
        <sub>微信赞助支持</sub>
      </td>
      <td align="center">
        <img src="assets/ali-pay.png" width="180" alt="支付宝赞助" /><br />
        <sub>支付宝赞助支持</sub>
      </td>
    </tr>
  </table>
</div>

---

## 声明

采用 [CC BY-NC-ND 4.0](LICENSE) 协议，供个人免费使用。所有数据均在本地处理。
