# 系统详细设计说明书 (System Technical Design Document)

本文档详细描述 `playlistsync` 跨平台歌单同步系统的总体架构、各子系统核心算法、数据流转逻辑、协议逆向实现与数据一致性保障机制。

---

## 目录

- [1. 系统架构与分层设计](#1-系统架构与分层设计)
  - [1.1 逻辑分层模型](#11-逻辑分层模型)
  - [1.2 核心模块职责映射](#12-核心模块职责映射)
  - [1.3 端到端生命周期时序](#13-端到端生命周期时序)
- [2. 领域模型与数据契约设计](#2-领域模型与数据契约设计)
  - [2.1 实体关系模型](#21-实体关系模型)
  - [2.2 核心结构体定义](#22-核心结构体定义)
- [3. 认证与 CDP 会话捕获子系统设计](#3-认证与-cdp-会话捕获子系统设计)
  - [3.1 架构与通信协议](#31-架构与通信协议)
  - [3.2 浏览器发现与独立 Profile 隔离](#32-浏览器发现与独立-profile-隔离)
  - [3.3 纯 Go 原生 WebSocket/CDP 客户端实现](#33-纯-go-原生-websocketcdp-客户端实现)
  - [3.4 凭证探活与健康检查状态机](#34-凭证探活与健康检查状态机)
- [4. 逆向 YouTube Music Innertube 协议适配器设计](#4-逆向-youtube-music-innertube-协议适配器设计)
  - [4.1 SAPISIDHASH 动态哈希算法实现](#41-sapisidhash-动态哈希算法实现)
  - [4.2 核心接口契约与 Payload 逆向](#42-核心接口契约与-payload-逆向)
  - [4.3 分页 Continuation Token 递归解析引擎](#43-分页-continuation-token-递归解析引擎)
- [5. 保守型模糊匹配与差异对齐引擎设计](#5-保守型模糊匹配与差异对齐引擎设计)
  - [5.1 文本清洗与归一化流水线](#51-文本清洗与归一化流水线)
  - [5.2 多维度加权打分模型数学推导](#52-多维度加权打分模型数学推导)
  - [5.3 增量差异对齐算法 (ComputeDiff)](#53-增量差异对齐算法-computediff)
- [6. 审计、不变式验证与 CLI 交互设计](#6-审计不变式验证与-cli-交互设计)
  - [6.1 四大数据完整性不变式体系](#61-四大数据完整性不变式体系)
  - [6.2 工件持久化规约](#62-工件持久化规约)
  - [6.3 CLI 交互与非交互模式支持](#63-cli-交互与非交互模式支持)
- [7. 网络容错、安全与代理自适应设计](#7-网络容错安全与代理自适应设计)
  - [7.1 三级自适应代理探测机制](#71-三级自适应代理探测机制)
  - [7.2 本地回环 CDP 强行直连隔离](#72-本地回环-cdp-强行直连隔离)
  - [7.3 凭证数据局部隔离与安全脱敏](#73-凭证数据局部隔离与安全脱敏)

---

## 1. 系统架构与分层设计

### 1.1 逻辑分层模型

`playlistsync` 严格遵循依赖倒置与清晰分层架构原则，上层依赖下层抽象，领域模型独立于具体平台协议：

```
+-------------------------------------------------------------------------+
|                        Presentation Layer (CLI)                         |
|   cmd/playlistsync/main.go  -  Flag Routing, Command Dispatcher, UX     |
+------------------------------------+------------------------------------+
                                     |
                                     v
+-------------------------------------------------------------------------+
|                          Engine Layer (Core)                            |
|   internal/engine/syncer.go  -  Workflow Orchestration & Batch Actions  |
|   internal/engine/diff.go    -  Diffing & Fuzzy Confidence Scoring      |
+------------------+-----------------+-------------------+----------------+
                   |                 |                   |
                   v                 v                   v
+--------------------+ +--------------------+ +--------------------+
|  internal/spotify  | |  internal/ytmusic  | |  internal/report   |
| (JSON/CSV Readers) | |  (Innertube API)   | | (Audit & Invariants|
+--------------------+ +--------------------+ +--------------------+
                   |                 |                   |
                   +--------+--------+-------------------+
                            |
                            v
+-------------------------------------------------------------------------+
|                       Authentication Subsystem                          |
|   internal/auth/auth.go, cdp.go, browser.go, proxy.go, validator.go     |
+-------------------------------------------------------------------------+
                            |
                            v
+-------------------------------------------------------------------------+
|                         Domain Model Layer                              |
|   internal/model/spotify.go, ytmusic.go, sync.go                        |
+-------------------------------------------------------------------------+
```

### 1.2 核心模块职责映射

| 模块包路径 | 职责定位 | 核心类型 / 入口函数 |
| :--- | :--- | :--- |
| `cmd/playlistsync` | CLI 命令行交互、子命令分发、参数校验、退出码管理 | `main()`、`runSync()`、`runLogin()`、`runInspect()`、`runVerify()` |
| `internal/engine` | 同步工作流编排、差异对比计算、多维度模糊匹配打分 | `Syncer`、`ComputeDiff()`、`CalculateScore()`、`EvaluateConfidence()` |
| `internal/ytmusic` | YouTube Music Innertube 逆向客户端、Continuation 解析 | `Client`、`GetPlaylist()`、`SearchSong()`、`AddPlaylistItems()`、`RemovePlaylistItems()` |
| `internal/spotify` | Spotify 歌单数据读取、本地 JSON/CSV 解析与持久化 | `Reader`、`Client`、`ReadPlaylistJSON()`、`WritePlaylistJSON()`、`WritePlaylistCSV()` |
| `internal/auth` | CDP 浏览器自动化拉起、会话提取、代理自适应与健康检查 | `Login()`、`ValidateSpotifyCredentials()`、`ValidateYTMCredentials()`、`DetectSystemProxy()` |
| `internal/report` | 数据不变式校验、终端审计摘要呈现、报告持久化 | `Reporter`、`Summarize()`、`Validate()`、`GenerateReport()` |
| `internal/model` | 纯净领域实体、跨平台数据模型、工件格式定义 | `SpotifyPlaylist`、`YTMPlaylist`、`SyncResult`、`DiffPlan` |

### 1.3 端到端生命周期时序

```
User (CLI)          Syncer             Auth Validator      YTM Client          Diff Engine
    │                  │                     │                  │                   │
    ├─ sync <name> ───>│                     │                  │                   │
    │                  ├─ ValidateCreds ────>│                  │                   │
    │                  │<─ Valid (Cached) ───┤                  │                   │
    │                  │                                        │                   │
    │                  ├─ Load Source Playlist (Spotify JSON) ─>│                   │
    │                  │                                        │                   │
    │                  ├─ FindOrCreatePlaylist ────────────────>│                   │
    │                  │<─ Target Playlist ID & Remote Tracks ──┤                   │
    │                  │                                        │                   │
    │                  ├─ ComputeDiff(source, target) ─────────────────────────────>│
    │                  │<─ DiffPlan (Matched, Missing, Extra) ──────────────────────┤
    │                  │                                        │                   │
    │                  ├─ For each Missing: SearchSong ────────>│                   │
    │                  │<─ Candidate Tracks ────────────────────┤                   │
    │                  │  (EvaluateConfidence >= 70)           │                   │
    │                  │                                        │                   │
    │                  ├─ AddPlaylistItems(Approved Video IDs) >│                   │
    │                  │<─ Success ─────────────────────────────┤                   │
    │                  │                                        │                   │
    │                  ├─ CleanExtra ? RemovePlaylistItems ────>│                   │
    │                  │<─ Success ─────────────────────────────┤                   │
    │                  │                                        │                   │
    │                  ├─ Fetch Target Verification ───────────>│                   │
    │                  ├─ Generate Artifacts & Verify Invariants                   │
    │<─ Summary Print ─┤                                                            │
```

---

## 2. 领域模型与数据契约设计

### 2.1 实体关系模型

```
   +----------------------+                     +----------------------+
   |   SpotifyPlaylist    |                     |     YTMPlaylist      |
   +----------------------+                     +----------------------+
   | Title: string        |                     | ID: string           |
   | SourceURL: string    |                     | Title: string        |
   | Tracks: []Track      |                     | Tracks: []YTMTrack   |
   +----------+-----------+                     +----------+-----------+
              │                                            │
              │ 1..N                                       │ 1..N
              v                                            v
   +----------------------+                     +----------------------+
   |     SpotifyTrack     |                     |       YTMTrack       |
   +----------------------+                     +----------------------+
   | Index: int           |                     | VideoID: string      |
   | Title: string        |                     | SetVideoID: string   |
   | Artists: []string    |                     | Title: string        |
   | Album: string        |                     | Artists: []string    |
   | Duration: string     |                     | Duration: string     |
   +----------------------+                     +----------------------+
              │                                            │
              +---------------------+----------------------+
                                    │
                                    v (Diff & Match)
                         +----------------------+
                         |      SyncResult      |
                         +----------------------+
                         | PlaylistID: string   |
                         | TotalSourceTracks: int    |
                         | AddedTracks: int     |
                         | SkippedTracks: int   |
                         | Skipped: []Skipped   |
                         | Added: []AddedTrack  |
                         | Removed: []Removed   |
                         | Verification: *Obj   |
                         +----------------------+
```

### 2.2 核心结构体定义

#### 2.2.1 源音轨与目标音轨 (`internal/model/spotify.go`, `ytmusic.go`)
```go
type SpotifyTrack struct {
    Index    int      `json:"index"`
    Title    string   `json:"title"`
    Artists  []string `json:"artists"`
    Album    string   `json:"album"`
    Duration string   `json:"duration"`
}

type YTMTrack struct {
    VideoID    string   `json:"videoId"`
    SetVideoID string   `json:"setVideoId,omitempty"` // 用于精确删除单项
    Title      string   `json:"title"`
    Artists    []string `json:"artists"`
    Album      string   `json:"album,omitempty"`
    Duration   string   `json:"duration,omitempty"`
}
```

#### 2.2.2 差异对齐计划 (`internal/engine/diff.go`)
```go
type DiffPlan struct {
    ExtraInYTM   []model.YTMTrack     // 目标歌单中存在但源歌单没有的歌曲
    MissingInYTM []model.SpotifyTrack // 源歌单中存在但目标歌单缺失的歌曲
    Matched      []model.AddedTrack   // 双方已完全对齐的歌曲
}
```

---

## 3. 认证与 CDP 会话捕获子系统设计

### 3.1 架构与通信协议

认证子系统基于 Chrome DevTools Protocol 实现。为了确保通信鲁棒性与高可靠性，架构设计如下：

```
+-------------------------------------------------------------------------+
|                            internal/auth                                |
|                                                                         |
|  +-----------------+    Exec     +-----------------------------------+  |
|  |   browser.go    | ----------> | chrome.exe / msedge.exe           |  |
|  |                 |             | --remote-debugging-port=9222      |  |
|  +-----------------+             | --user-data-dir=.chrome_<plat>    |  |
|                                  +-----------------+-----------------+  |
|                                                    |                    |
|                                  Direct Loopback   v                    |
|  +-----------------+   HTTP/WS   +-----------------------------------+  |
|  |     cdp.go      | <---------> | /json/version & /json/list        |  |
|  | (Raw WS Client) |             | ws://127.0.0.1:9222/devtools/page |  |
|  +--------+--------+             +-----------------------------------+  |
|           │                                                             |
|           v JSON-RPC Commands: Network.getCookies / Runtime.evaluate    |
|  +-----------------+                                                    |
|  |   storage.go    | ----------> output/auth/<platform>_credentials.json|
|  +-----------------+                                                    |
+-------------------------------------------------------------------------+
```

### 3.2 浏览器发现与独立 Profile 隔离

1. **浏览器发现逻辑 (`FindBrowserPath`)**：
   - 优先扫描 64 位及 32 位 `Program Files` 下的 `Google\Chrome\Application\chrome.exe`。
   - 降级扫描 `Microsoft\Edge\Application\msedge.exe`。
   - 兜底扫描当前 `PATH` 环境变量中的 `chrome`、`chromium`、`msedge`。
2. **Profile 隔离目录**：
   - Spotify 专属目录：`output/auth/.chrome_spotify`
   - YouTube Music 专属目录：`output/auth/.chrome_ytmusic`
   - 彻底避免污染宿主系统主浏览器，且登录会话在本地持久化，后续命令可静默秒级复用。

### 3.3 高可靠 WebSocket/CDP 通信实现

`internal/auth/cdp.go` 采用成熟轻量的 `gorilla/websocket` 客户端管理与 Chromium 调试端口的会话：
- 显式绑定本地直连 Loopback 传输，严禁任何外部代理污染 CDP 回环端口。
- 采用掩码（Client-to-Server Masking）格式化发送 JSON-RPC 数据帧：
  ```json
  {"id": 1, "method": "Network.getCookies"}
  ```
- 持续读取分帧数据并反序列化为 Cookie 键值字典。

### 3.4 凭证探活与健康检查状态机

```
               +----------------------+
               |    Start Command     |
               +----------+-----------+
                          │
                          v
               +----------------------+
               | Read Credential JSON |
               +----------+-----------+
                          │
               +----------v-----------+
        No     | File Exists & Valid? |
   ┌───────────+                      |
   │           +----------+-----------+
   │                      │ Yes
   │           +----------v-----------+
   │           | Network Health Probe |
   │           | (Fetch Current User) |
   │           +----------+-----------+
   │                      │
   │           +----------v-----------+
   │    No     |  HTTP 200 & User OK? |
   ├───────────+                      |
   │           +----------+-----------+
   │                      │ Yes
   │                      v
   │           +----------------------+
   │           | Reuse Cached Session |
   │           +----------------------+
   │
   v
+--------------------------------------+
| Launch CDP Browser & Prompt Login    |
+--------------------------------------+
```

---

## 4. 逆向 YouTube Music Innertube 协议适配器设计

### 4.1 SAPISIDHASH 动态哈希算法实现

YouTube Music 鉴权核心在于 `SAPISIDHASH`。其底层逻辑由 Google `WEB_REMIX` 客户端执行，本系统在 Go 中实现等价签名：

```go
func GenerateSAPISIDHash(sapisid string, origin string) string {
    now := time.Now().Unix()
    payload := fmt.Sprintf("%d %s %s", now, sapisid, origin)
    h := sha1.New()
    h.Write([]byte(payload))
    sha1Hex := hex.EncodeToString(h.Sum(nil))
    return fmt.Sprintf("SAPISIDHASH %d_%s", now, sha1Hex)
}
```

每次向 Innertube 发送 POST 请求前，动态生成该 Header，确保请求永不过期。

### 4.2 核心接口契约与 Payload 逆向

所有接口统一 POST 至 `https://music.youtube.com/youtubei/v1/<endpoint>?prettyPrint=false`：

#### 1. 歌曲搜索 (`/youtubei/v1/search`)
- **Params**: `Eg-KAQwIABAAGAAgACgB`（限定搜索类型为 Songs/Tracks）
- **Payload**:
  ```json
  {
    "context": { "client": { "clientName": "WEB_REMIX", "clientVersion": "1.20260822.01.00", "hl": "zh-CN", "gl": "US" } },
    "query": "晴天 周杰伦",
    "params": "Eg-KAQwIABAAGAAgACgB"
  }
  ```

#### 2. 批量添加歌曲 (`/youtubei/v1/browse/edit_playlist`)
- **Payload**:
  ```json
  {
    "context": { "client": { "clientName": "WEB_REMIX", "clientVersion": "1.20260822.01.00" } },
    "playlistId": "PLxxxxxxxxxxxxxxxxxxxx",
    "actions": [
      { "action": "ACTION_ADD_VIDEO", "addedVideoId": "video_id_1" },
      { "action": "ACTION_ADD_VIDEO", "addedVideoId": "video_id_2" }
    ]
  }
  ```

#### 3. 精准单项删除 (`/youtubei/v1/browse/edit_playlist`)
- **说明**：YouTube Music 删除操作必须同时携带 `setVideoId`（歌单条目唯一 ID）与 `removedVideoId`，否则将发生 400 Bad Request。
- **Payload**:
  ```json
  {
    "context": { "client": { "clientName": "WEB_REMIX", "clientVersion": "1.20260822.01.00" } },
    "playlistId": "PLxxxxxxxxxxxxxxxxxxxx",
    "actions": [
      { "action": "ACTION_REMOVE_VIDEO", "setVideoId": "SET_VIDEO_ID_UUID", "removedVideoId": "video_id" }
    ]
  }
  ```

### 4.3 分页 Continuation Token 递归解析引擎

当歌单超过 100 首曲目时，Innertube API 仅返回首批曲目并在底部附带 `continuationEndpoint`。

解析引擎采用状态机递归处理（`internal/ytmusic/parser.go`）：
1. 解析主响应体中的 `musicShelfRenderer.contents`。
2. 提取 `musicShelfRenderer.continuations[0].nextContinuationData.continuation` Token。
3. 携带 Continuation Token 循环调用 `/youtubei/v1/browse?continuation=<token>`。
4. 直至连续两页无新曲目或 Token 穷尽，完成全量抓取。

---

## 5. 保守型模糊匹配与差异对齐引擎设计

### 5.1 文本清洗与归一化流水线

```
Input Raw Text: "【官方MV 4K】晴天（周杰伦 / Jay Chou）[Official Audio]〜"
      │
      ▼ 1. 全角与标点规范化 (normalizeUnicode)
      │    -> "[官方MV 4K]晴天(周杰伦 / Jay Chou)[Official Audio]~"
      │
      ▼ 2. 繁体转简体 (convertT2S)
      │    -> "[官方MV 4K]晴天(周杰伦 / Jay Chou)[Official Audio]~"
      │
      ▼ 3. 噪声括号剥离 (stripNoiseBrackets)
      │    -> "晴天"
      │
      ▼ 4. 字母小写化与符号精简 (normalizeText)
      ▼
Output Clean Text: "晴天"
```

### 5.2 多维度加权打分模型数学推导

给定源音轨 S 与候选音轨 C，匹配总分定义为：

`Score(S, C) = min(100, max(0, W_title + W_artist + W_duration))`

其中各维度计算规则如下：

#### 1. 标题分 W_title ∈ [0, 55]
- 若 S_clean == C_clean 或原始标题相等：55 分
- 若 S_clean 包含 C_clean 或反之：45 分
- 若字符相似度 Sim_rune >= 0.75：floor(50 * Sim_rune) 分；若 >= 0.50：floor(40 * Sim_rune) 分
- 若分词重合度 Jaccard >= 0.8：35 分；若 >= 0.5：25 分
- 否则：0 分（标题不匹配直接否决）

#### 2. 艺术家分 W_artist ∈ [-15, 30]
- 提取源音轨与候选音轨全部变体（拆分逗号、斜杠、括号别名）
- 存在完全相同变体：30 分
- 存在包含关系或字符相似度 >= 0.60：25 分
- 候选标题中包含源艺术家名称：25 分
- 候选条目未包含艺术家信息：15 分（中性处理）
- 艺术家完全冲突且无交集：-15 分（惩罚）

#### 3. 时长惩罚与奖励 W_duration ∈ [-40, +15]
令 Δt = |t_S - t_C|（秒）：
- Δt <= 3s: +15 分
- 3s < Δt <= 8s: +10 分
- 8s < Δt <= 15s: +5 分
- 15s < Δt <= 25s: 0 分
- 25s < Δt <= 45s: -25 分
- Δt > 45s: -40 分（强力惩罚伴奏合集或专辑长视频）

#### 4. 门禁判决
`IsMatch(S, C) = (Score(S, C) >= 70)`

### 5.3 增量差异对齐算法 (ComputeDiff)

```go
func ComputeDiff(spotify *model.SpotifyPlaylist, ytm *model.YTMPlaylist, knownMapping map[int]string) *DiffPlan {
    plan := &DiffPlan{}
    ytmByVid := make(map[string]model.YTMTrack)
    for _, t := range ytm.Tracks {
        if t.VideoID != "" { ytmByVid[t.VideoID] = t }
    }
    matchedVids := make(map[string]bool)

    // 1. 优先根据持久化映射与精确匹配
    for _, st := range spotify.Tracks {
        if vid, ok := knownMapping[st.Index]; ok && vid != "" {
            if ytmTrack, exists := ytmByVid[vid]; exists {
                plan.Matched = append(plan.Matched, model.AddedTrack{
                    Index: st.Index, Title: st.Title, Artists: st.Artists, TargetTrackID: vid, DestinationTitle: ytmTrack.Title,
                })
                matchedVids[vid] = true
                continue
            }
        }
        
        // 2. 遍历现有目标曲目进行模糊置信度匹配
        var matchedYTM *model.YTMTrack
        for _, ytTrack := range ytm.Tracks {
            if ytTrack.VideoID == "" || matchedVids[ytTrack.VideoID] { continue }
            cand := model.YTMSearchResult{VideoID: ytTrack.VideoID, Title: ytTrack.Title, Artists: ytTrack.Artists, Duration: ytTrack.Duration}
            if CalculateScore(st, cand) >= ConfidenceThreshold {
                matchedYTM = &ytTrack
                break
            }
        }

        if matchedYTM != nil {
            plan.Matched = append(plan.Matched, model.AddedTrack{
                Index: st.Index, Title: st.Title, Artists: st.Artists, TargetTrackID: matchedYTM.VideoID, DestinationTitle: matchedYTM.Title,
            })
            matchedVids[matchedYTM.VideoID] = true
            if knownMapping != nil { knownMapping[st.Index] = matchedYTM.VideoID }
            continue
        }

        // 3. 确认为目标平台缺失歌曲
        plan.MissingInYTM = append(plan.MissingInYTM, st)
    }

    // 4. 提取目标平台中未被任何源歌曲匹配的多余歌曲
    for _, t := range ytm.Tracks {
        if t.VideoID != "" && !matchedVids[t.VideoID] {
            plan.ExtraInYTM = append(plan.ExtraInYTM, t)
        }
    }
    return plan
}
```

---

## 6. 审计、不变式验证与 CLI 交互设计

### 6.1 四大数据完整性不变式体系

为了向用户提供确定性保障，`internal/report/reporter.go` 建立了形式化不变式校验器：

| 不变式编号 | 数学命题 | 业务含义 | 违例处理 |
| :--- | :--- | :--- | :--- |
| **Invariant 1** | `N_source = N_added + N_skipped` | 源数据总量守恒：每首歌必须有确定的归宿（加入或跳过） | 抛出 `total tracks mismatch` 阻断流程 |
| **Invariant 2** | `N_target_final = N_added` | 目标歌单最终容量必须等于成功添加/保留的曲目总数 | 抛出 `playlist size mismatch` |
| **Invariant 3** | `∀ t ∈ Skipped: t.Reason != "" ∧ t.Index >= 1` | 任何被跳过的歌曲必须具备人类可读的归因解释与合法序号 | 抛出 `invalid skipped record` |
| **Invariant 4** | `∀ t ∈ Added: t.TargetTrackID != "" ∧ t.Title != ""` | 任何新增歌曲的目标音轨/资源 ID 必须非空且有效 | 抛出 `invalid targetTrackId in added tracks` |

### 6.2 工件持久化规约

每次同步完成后，系统在 `output/` 目录下按具体的来源与目标平台方向（`<from>_to_<to>`）固化标准化语义工件：

1. **`output/<source_platform>_<playlist>_source.json`**（如 `spotify_<playlist>_source.json`、`ytmusic_<playlist>_source.json`）：
   记录源歌单完整曲目元数据快照（包含平台类型、标题、曲目列表等）。
2. **`output/<source_platform>_to_<target_platform>_<playlist>_result.json`**（如 `spotify_to_ytmusic_<playlist>_result.json`、`ytmusic_to_spotify_<playlist>_result.json`）：
   记录执行方向（`direction`）、来源/目标平台（`sourcePlatform`/`targetPlatform`）、跳过明细、新增曲目对应关系以及最终目标歌单验证快照。
3. **`output/<source_platform>_to_<target_platform>_<playlist>_report.json`**（如 `spotify_to_ytmusic_<playlist>_report.json`、`ytmusic_to_spotify_<playlist>_report.json`）：
   记录统计指标、成功率（Precision & Coverage）、哈希校验值与审计时间戳。

### 6.3 CLI 交互与非交互模式支持

- **CLI 命令体系**：
  ```bash
  playlistsync sync <name> [--from=...] [--to=...] [--proxy=...] [--clean-extra] [--yes]
  playlistsync login [spotify|youtube-music|all] [--force] [--proxy=...]
  playlistsync inspect <name>
  playlistsync verify <name>
  playlistsync report <name>
  ```
- **交互与非交互模式**：
  - 在存在高风险操作（如修剪多首歌曲）时，默认通过终端提示 `[y/N]` 等待人工确认。
  - 在 CI 或脚本自动化调用时，可通过 `--yes` / `-y` 绕过交互提示，实现全自动化无干预执行。

---

## 7. 网络容错、安全与代理自适应设计

### 7.1 三级自适应代理探测机制

```
+-------------------------------------------------------------+
|               DetectSystemProxy Pipeline                   |
|                                                             |
|  [Step 1] Check CLI flag: --proxy                           |
|       │                                                     |
|       ├─ Found? ───> Return Proxy URL                       |
|       v                                                     |
|  [Step 2] Check Environment: HTTP_PROXY / HTTPS_PROXY       |
|       │                                                     |
|       ├─ Found? ───> Return Proxy URL                       |
|       v                                                     |
|  [Step 3] Query Windows Registry:                           |
|           HKCU\Software\Microsoft\Windows\CurrentVersion\   |
|           Internet Settings (ProxyEnable & ProxyServer)     |
|       │                                                     |
|       ├─ Found & Enabled? ───> Return Proxy URL             |
|       v                                                     |
|  [Step 4] Return Empty (Direct Connection)                  |
+-------------------------------------------------------------+
```

### 7.2 本地回环 CDP 强行直连隔离

针对某些网络代理软件（如系统全局代理或 TUN 模式）会劫持 `127.0.0.1` 流量的顽疾，系统在 Go 底层实施强行隔离：
- 构造 `http.Transport{Proxy: nil}` 专有客户端。
- 保证 CDP 与 WebSocket 探测永远直连本地内核 Socket，不受外部系统代理干扰。

### 7.3 凭证数据局部隔离与安全脱敏

- **物理存储安全**：所有会话 Cookie、用户 Profile 及生成结果均限制在 `output/` 目录中，项目 `.gitignore` 强制忽略该目录，杜绝代码提交导致凭证外泄。
- **内存与日志脱敏**：在终端打印及生成最终报告时，Cookie 中的 `SAPISID`、`HSID`、`SSID` 等敏感值均进行掩码脱敏（如 `SAPISID=***`），保障演示与共享日志时的安全性。
