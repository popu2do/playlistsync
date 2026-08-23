package ytmusic

import (
	"encoding/json"
	"fmt"
	"playlistsync/internal/model"
	"regexp"
	"strings"
	"unicode"
)

var (
	// reStatsText matches numeric playback counts/views across global languages
	reStatsText = regexp.MustCompile(`(?i)^(plays:|views:|播放次数[：:]|재생횟수[：:]|조회수\s*)?\s*[\d,.\s]+(k|m|b|万|萬|亿|億|천|만|백만|тыс\.?)?\s*(views|plays|次观看|次播放|回視聴|회|회\s*視聴|조회수|reproducciones|visualizzazioni|просмотров|aufrufe|vues|katselukertaa|vezes)?\s*$`)

	// typeIndicatorSet contains common music item type indicators across languages
	typeIndicatorSet = map[string]bool{
		"song": true, "video": true, "single": true, "album": true, "ep": true, "track": true, "artist": true, "playlist": true, "podcast": true,
		"歌曲": true, "视频": true, "單曲": true, "单曲": true, "專輯": true, "专辑": true, "播放列表": true, "播放清單": true, "藝人": true, "艺人": true, "歌手": true,
		"曲": true, "動画": true, "シングル": true, "アルバム": true, "トラック": true, "アーティスト": true, "プレイリスト": true,
		"노래": true, "동영상": true, "싱글": true, "앨범": true, "트랙": true, "아티스트": true, "재생목록": true, "음악": true,
		"canción": true, "chanson": true, "lied": true, "faixa": true, "brano": true, "трек": true, "песня": true,
	}

	reDurationPattern = regexp.MustCompile(`\b\d{1,2}:\d{2}(?::\d{2})?\b`)
)

// parsePlaylistResponse parses the raw Innertube browse response into a YTMPlaylist
func parsePlaylistResponse(body []byte) (*model.YTMPlaylist, string, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, "", fmt.Errorf("unmarshal browse json: %w", err)
	}

	pl := &model.YTMPlaylist{}

	if header, ok := raw["header"].(map[string]interface{}); ok {
		extractHeaderInfo(header, pl)
	}

	var continuationToken string
	if contents, ok := raw["contents"].(map[string]interface{}); ok {
		continuationToken = extractTracksFromContents(contents, pl)
	}

	if continuationContents, ok := raw["continuationContents"].(map[string]interface{}); ok {
		continuationToken = extractTracksFromContinuation(continuationContents, pl)
	}

	// Modern Innertube responses return continuation items in onResponseReceivedActions
	if actions, ok := raw["onResponseReceivedActions"].([]interface{}); ok {
		if token := extractTracksFromActions(actions, pl); token != "" {
			continuationToken = token
		}
	}

	pl.TrackCount = len(pl.Tracks)
	return pl, continuationToken, nil
}

func extractTracksFromActions(actions []interface{}, pl *model.YTMPlaylist) string {
	var nextToken string
	for _, act := range actions {
		actMap, ok := act.(map[string]interface{})
		if !ok {
			continue
		}
		appendAction, ok := actMap["appendContinuationItemsAction"].(map[string]interface{})
		if !ok {
			continue
		}
		items, _ := appendAction["continuationItems"].([]interface{})
		if token := extractShelfItems(items, nil, pl); token != "" {
			nextToken = token
		}
	}
	return nextToken
}

func extractHeaderInfo(header map[string]interface{}, pl *model.YTMPlaylist) {
	var respHeader map[string]interface{}
	if r, ok := header["musicResponsiveHeaderRenderer"].(map[string]interface{}); ok {
		respHeader = r
	} else if editable, ok := header["musicEditablePlaylistDetailHeaderRenderer"].(map[string]interface{}); ok {
		if r, ok := editable["header"].(map[string]interface{}); ok {
			respHeader, _ = r["musicResponsiveHeaderRenderer"].(map[string]interface{})
		}
	}

	if respHeader == nil {
		return
	}

	if runs, ok := getNavSlice(respHeader, "title", "runs"); ok && len(runs) > 0 {
		var parts []string
		for _, r := range runs {
			if rMap, ok := r.(map[string]interface{}); ok {
				if txt, ok := rMap["text"].(string); ok && txt != "" {
					parts = append(parts, txt)
				}
			}
		}
		if len(parts) > 0 {
			pl.Title = strings.TrimSpace(strings.Join(parts, ""))
		}
	}

	if runs, ok := getNavSlice(respHeader, "description", "musicDescriptionShelfRenderer", "description", "runs"); ok {
		var sb strings.Builder
		for _, item := range runs {
			if runMap, ok := item.(map[string]interface{}); ok {
				sb.WriteString(fmt.Sprintf("%v", runMap["text"]))
			}
		}
		pl.Description = sb.String()
	}
}

func extractTracksFromContents(contents map[string]interface{}, pl *model.YTMPlaylist) string {
	if twoCol, ok := contents["twoColumnBrowseResultsRenderer"].(map[string]interface{}); ok {
		initialTrackCount := len(pl.Tracks)
		tabToken := extractTabsTracks(twoCol, pl)
		if len(pl.Tracks) > initialTrackCount {
			return tabToken
		}
		return extractSecondaryTracks(twoCol, pl)
	}

	if singleCol, ok := contents["singleColumnBrowseResultsRenderer"].(map[string]interface{}); ok {
		return extractTabsTracks(singleCol, pl)
	}

	return ""
}

func extractTabsTracks(container map[string]interface{}, pl *model.YTMPlaylist) string {
	tabs, ok := getNavSlice(container, "tabs")
	if !ok || len(tabs) == 0 {
		return ""
	}
	tab, ok := tabs[0].(map[string]interface{})
	if !ok {
		return ""
	}
	secContents, ok := getNavSlice(tab, "tabRenderer", "content", "sectionListRenderer", "contents")
	if !ok {
		return ""
	}

	var continuationToken string
	for _, sec := range secContents {
		if secMap, ok := sec.(map[string]interface{}); ok {
			if pl.Title == "" {
				extractHeaderInfo(secMap, pl)
			}
			if token := extractShelfTracksAndContinuation(secMap, pl); token != "" {
				continuationToken = token
			}
		}
	}
	return continuationToken
}

func extractSecondaryTracks(twoCol map[string]interface{}, pl *model.YTMPlaylist) string {
	secContents, ok := getNavSlice(twoCol, "secondaryContents", "sectionListRenderer", "contents")
	if !ok {
		return ""
	}

	var continuationToken string
	for _, secItem := range secContents {
		if secMap, ok := secItem.(map[string]interface{}); ok {
			if token := extractShelfTracksAndContinuation(secMap, pl); token != "" {
				continuationToken = token
			}
		}
	}
	return continuationToken
}

func extractShelfItems(items []interface{}, continuations []interface{}, pl *model.YTMPlaylist) string {
	var inlineToken string
	for _, item := range items {
		if itemMap, ok := item.(map[string]interface{}); ok {
			if contItem, ok := itemMap["continuationItemRenderer"].(map[string]interface{}); ok {
				inlineToken = getNavString(contItem, "continuationEndpoint", "continuationCommand", "token")
				continue
			}
		}
		if track := parseListItem(item); track != nil {
			pl.Tracks = append(pl.Tracks, *track)
		}
	}

	if token := getContinuationToken(continuations); token != "" {
		return token
	}
	return inlineToken
}

func extractShelfTracksAndContinuation(secMap map[string]interface{}, pl *model.YTMPlaylist) string {
	if plShelf, ok := secMap["musicPlaylistShelfRenderer"].(map[string]interface{}); ok {
		items, _ := plShelf["contents"].([]interface{})
		conts, _ := plShelf["continuations"].([]interface{})
		return extractShelfItems(items, conts, pl)
	}
	if mShelf, ok := secMap["musicShelfRenderer"].(map[string]interface{}); ok {
		items, _ := mShelf["contents"].([]interface{})
		conts, _ := mShelf["continuations"].([]interface{})
		return extractShelfItems(items, conts, pl)
	}
	return ""
}

func extractTracksFromContinuation(cont map[string]interface{}, pl *model.YTMPlaylist) string {
	for _, key := range []string{"musicPlaylistShelfContinuation", "musicShelfContinuation", "sectionListContinuation"} {
		if shelf, ok := cont[key].(map[string]interface{}); ok {
			items, _ := shelf["contents"].([]interface{})
			conts, _ := shelf["continuations"].([]interface{})
			return extractShelfItems(items, conts, pl)
		}
	}
	return ""
}

func parseListItem(item interface{}) *model.YTMTrack {
	itemMap, ok := item.(map[string]interface{})
	if !ok {
		return nil
	}
	renderer, ok := itemMap["musicResponsiveListItemRenderer"].(map[string]interface{})
	if !ok {
		return nil
	}

	track := &model.YTMTrack{}

	if plData, ok := renderer["playlistItemData"].(map[string]interface{}); ok {
		track.VideoID, _ = plData["videoId"].(string)
		track.SetVideoID, _ = plData["playlistSetVideoId"].(string)
	}

	if flexCols, ok := renderer["flexColumns"].([]interface{}); ok {
		parseFlexColumns(flexCols, track)
	}

	if fixedCols, ok := renderer["fixedColumns"].([]interface{}); ok {
		parseFixedColumns(fixedCols, track)
	}

	if track.VideoID == "" {
		track.VideoID = extractVideoIDFromRenderer(renderer)
	}

	if track.VideoID == "" && track.Title == "" {
		return nil
	}

	return track
}

type runNode struct {
	text     string
	pageType string
	browseID string
	videoID  string
}

func parseRunNode(rMap map[string]interface{}) runNode {
	node := runNode{}
	if txt, ok := rMap["text"].(string); ok {
		node.text = txt
	}
	nav, ok := rMap["navigationEndpoint"].(map[string]interface{})
	if !ok {
		return node
	}

	if watch, ok := nav["watchEndpoint"].(map[string]interface{}); ok {
		if vID, ok := watch["videoId"].(string); ok {
			node.videoID = vID
		}
	}

	if browse, ok := nav["browseEndpoint"].(map[string]interface{}); ok {
		if bID, ok := browse["browseId"].(string); ok {
			node.browseID = bID
		}
		if pt := getNavString(browse, "browseEndpointContextSupportedConfigs", "browseEndpointContextMusicConfig", "pageType"); pt != "" {
			node.pageType = pt
		}
	}
	return node
}

func extractRunNodesFromColumn(colMap map[string]interface{}, rendererKey string) []runNode {
	target := colMap
	if inner, ok := colMap[rendererKey].(map[string]interface{}); ok {
		target = inner
	}
	var results []runNode
	if runs, ok := getNavSlice(target, "text", "runs"); ok {
		for _, rItem := range runs {
			if rMap, ok := rItem.(map[string]interface{}); ok {
				results = append(results, parseRunNode(rMap))
			}
		}
	}
	if len(results) == 0 {
		// Fallback for simpleText representations (common in duration & search items)
		if st := getNavString(target, "text", "simpleText"); st != "" {
			results = append(results, runNode{text: st})
		} else if st := getNavString(target, "simpleText"); st != "" {
			results = append(results, runNode{text: st})
		}
	}
	return results
}

func isArtistNode(node runNode) bool {
	switch node.pageType {
	case "MUSIC_PAGE_TYPE_ARTIST", "MUSIC_PAGE_TYPE_USER_CHANNEL":
		return true
	}
	if strings.HasPrefix(node.browseID, "UC") {
		return true
	}
	if strings.HasPrefix(node.browseID, "FEmusic_library_privately_owned_artist") {
		return true
	}
	return false
}

func isAlbumNode(node runNode) bool {
	switch node.pageType {
	case "MUSIC_PAGE_TYPE_ALBUM", "MUSIC_PAGE_TYPE_AUDIOBOOK":
		return true
	}
	if strings.HasPrefix(node.browseID, "MPREb_") {
		return true
	}
	if strings.HasPrefix(node.browseID, "FEmusic_library_privately_owned_release") {
		return true
	}
	return false
}

func isSeparatorText(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return true
	}
	for _, r := range trimmed {
		if !unicode.IsPunct(r) && !unicode.IsSymbol(r) && !unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func isStatsText(txt string) bool {
	trimmed := strings.TrimSpace(txt)
	if trimmed == "" {
		return false
	}
	return reStatsText.MatchString(trimmed)
}

func isTypeIndicatorText(txt string) bool {
	trimmed := strings.TrimSpace(txt)
	if trimmed == "" {
		return false
	}
	return typeIndicatorSet[strings.ToLower(trimmed)]
}

func parseFlexColumns(flexCols []interface{}, track *model.YTMTrack) {
	if len(flexCols) == 0 {
		return
	}

	// Column 0: Track Title — aggregate all non-empty runs
	if col0, ok := flexCols[0].(map[string]interface{}); ok {
		nodes0 := extractRunNodesFromColumn(col0, "musicResponsiveListItemFlexColumnRenderer")
		var titleParts []string
		for _, n := range nodes0 {
			if t := strings.TrimSpace(n.text); t != "" {
				titleParts = append(titleParts, t)
			}
			if track.VideoID == "" && n.videoID != "" {
				track.VideoID = n.videoID
			}
		}
		if track.Title == "" && len(titleParts) > 0 {
			track.Title = strings.TrimSpace(strings.Join(titleParts, ""))
		}
	}

	// Collect run nodes across remaining columns 1..N
	var remainingNodes []runNode
	for _, col := range flexCols[1:] {
		colMap, ok := col.(map[string]interface{})
		if !ok {
			continue
		}
		remainingNodes = append(remainingNodes, extractRunNodesFromColumn(colMap, "musicResponsiveListItemFlexColumnRenderer")...)
	}

	if len(remainingNodes) == 0 {
		return
	}

	// Single-pass extraction for duration, semantic artists, and heuristic fallback artists
	var semanticArtists []string
	var fallbackArtists []string
	seenSemantic := make(map[string]bool)
	seenFallback := make(map[string]bool)

	for _, n := range remainingNodes {
		t := strings.TrimSpace(n.text)
		if t == "" {
			continue
		}
		if dur := extractDurationText(t); dur != "" {
			if track.Duration == "" {
				track.Duration = dur
			}
			continue
		}
		if isArtistNode(n) {
			if !seenSemantic[t] {
				seenSemantic[t] = true
				semanticArtists = append(semanticArtists, t)
			}
			continue
		}
		if isSeparatorText(t) || isAlbumNode(n) || isStatsText(t) || isTypeIndicatorText(t) {
			continue
		}
		if !seenFallback[t] {
			seenFallback[t] = true
			fallbackArtists = append(fallbackArtists, t)
		}
	}

	if len(semanticArtists) > 0 {
		track.Artists = semanticArtists
	} else if len(fallbackArtists) > 0 {
		track.Artists = fallbackArtists
	}
}

func parseFixedColumns(fixedCols []interface{}, track *model.YTMTrack) {
	if len(fixedCols) == 0 {
		return
	}
	for _, col := range fixedCols {
		if fcol, ok := col.(map[string]interface{}); ok {
			nodes := extractRunNodesFromColumn(fcol, "musicResponsiveListItemFixedColumnRenderer")
			if len(nodes) == 0 {
				nodes = extractRunNodesFromColumn(fcol, "musicResponsiveListItemFlexColumnRenderer")
			}
			for _, n := range nodes {
				t := strings.TrimSpace(n.text)
				if dur := extractDurationText(t); dur != "" {
					track.Duration = dur
					return
				}
			}
		}
	}
}

// parseSearchResults parses Innertube search responses into YTMSearchResult slices
func parseSearchResults(body []byte) []model.YTMSearchResult {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}

	tabs, ok := getNavSlice(raw, "contents", "tabbedSearchResultsRenderer", "tabs")
	if !ok || len(tabs) == 0 {
		return nil
	}

	firstTab, ok := tabs[0].(map[string]interface{})
	if !ok {
		return nil
	}

	secContents, ok := getNavSlice(firstTab, "tabRenderer", "content", "sectionListRenderer", "contents")
	if !ok {
		return nil
	}

	var results []model.YTMSearchResult
	for _, sec := range secContents {
		secMap, ok := sec.(map[string]interface{})
		if !ok {
			continue
		}

		collectItems := func(items []interface{}) {
			for _, it := range items {
				if track := parseListItem(it); track != nil && track.VideoID != "" {
					results = append(results, model.YTMSearchResult{
						VideoID:  track.VideoID,
						Title:    track.Title,
						Artists:  track.Artists,
						Duration: track.Duration,
					})
				}
			}
		}

		if shelf, ok := secMap["musicShelfRenderer"].(map[string]interface{}); ok {
			items, _ := shelf["contents"].([]interface{})
			collectItems(items)
		}
		if itemSec, ok := secMap["itemSectionRenderer"].(map[string]interface{}); ok {
			items, _ := itemSec["contents"].([]interface{})
			collectItems(items)
		}
		if cardShelf, ok := secMap["musicCardShelfRenderer"].(map[string]interface{}); ok {
			if track := parseListItem(cardShelf); track != nil && track.VideoID != "" {
				results = append(results, model.YTMSearchResult{
					VideoID:  track.VideoID,
					Title:    track.Title,
					Artists:  track.Artists,
					Duration: track.Duration,
				})
			}
		}
	}

	return results
}

// parseLibraryPlaylists extracts playlists from library browse response
func parseLibraryPlaylists(body []byte) []model.YTMPlaylistSummary {
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil
	}

	contents, ok := raw["contents"].(map[string]interface{})
	if !ok {
		return nil
	}

	var tabs []interface{}
	if twoCol, ok := contents["twoColumnBrowseResultsRenderer"].(map[string]interface{}); ok {
		tabs, _ = twoCol["tabs"].([]interface{})
	} else if single, ok := contents["singleColumnBrowseResultsRenderer"].(map[string]interface{}); ok {
		tabs, _ = single["tabs"].([]interface{})
	}

	if len(tabs) == 0 {
		return nil
	}

	tab, ok := tabs[0].(map[string]interface{})
	if !ok {
		return nil
	}

	secContents, ok := getNavSlice(tab, "tabRenderer", "content", "sectionListRenderer", "contents")
	if !ok || len(secContents) == 0 {
		return nil
	}

	var summaries []model.YTMPlaylistSummary
	for _, sec := range secContents {
		secMap, ok := sec.(map[string]interface{})
		if !ok {
			continue
		}
		grid, ok := secMap["gridRenderer"].(map[string]interface{})
		if !ok {
			continue
		}
		items, ok := grid["items"].([]interface{})
		if !ok {
			continue
		}

		for _, it := range items {
			itMap, ok := it.(map[string]interface{})
			if !ok {
				continue
			}
			twoRow := getNavMap(itMap, "musicTwoRowItemRenderer")
			if twoRow == nil {
				continue
			}

			title := ""
			if runs, ok := getNavSlice(twoRow, "title", "runs"); ok && len(runs) > 0 {
				if r0, ok := runs[0].(map[string]interface{}); ok {
					title, _ = r0["text"].(string)
				}
			}

			browseID := getNavString(twoRow, "navigationEndpoint", "browseEndpoint", "browseId")
			id := strings.TrimPrefix(browseID, "VL")

			if id != "" && title != "" {
				summaries = append(summaries, model.YTMPlaylistSummary{
					ID:    id,
					Title: title,
				})
			}
		}
	}

	return summaries
}

// getNavMap safely navigates nested map[string]interface{} by keys, returning nil on any missing step.
func getNavMap(m map[string]interface{}, keys ...string) map[string]interface{} {
	curr := m
	for _, k := range keys {
		if curr == nil {
			return nil
		}
		next, ok := curr[k].(map[string]interface{})
		if !ok {
			return nil
		}
		curr = next
	}
	return curr
}

// getNavSlice safely navigates nested maps to retrieve a slice value
func getNavSlice(m map[string]interface{}, keys ...string) ([]interface{}, bool) {
	if len(keys) == 0 || m == nil {
		return nil, false
	}
	parent := getNavMap(m, keys[:len(keys)-1]...)
	if parent == nil {
		return nil, false
	}
	slice, ok := parent[keys[len(keys)-1]].([]interface{})
	return slice, ok
}

// getNavString safely navigates nested maps and retrieves the final string value.
func getNavString(m map[string]interface{}, keys ...string) string {
	if len(keys) == 0 {
		return ""
	}
	parent := getNavMap(m, keys[:len(keys)-1]...)
	if parent == nil {
		return ""
	}
	val, _ := parent[keys[len(keys)-1]].(string)
	return val
}

func extractVideoIDFromRenderer(renderer map[string]interface{}) string {
	if vID := getNavString(renderer, "navigationEndpoint", "watchEndpoint", "videoId"); vID != "" {
		return vID
	}
	return getNavString(renderer,
		"overlay", "musicItemThumbnailOverlayRenderer", "content",
		"musicPlayButtonRenderer", "playNavigationEndpoint", "watchEndpoint", "videoId")
}

func isDurationFormat(s string) bool {
	if len(s) < 3 || len(s) > 10 {
		return false
	}
	if strings.HasPrefix(s, ":") || strings.HasSuffix(s, ":") || strings.Contains(s, "::") {
		return false
	}
	colons := 0
	for _, ch := range s {
		if ch == ':' {
			colons++
		} else if ch < '0' || ch > '9' {
			return false
		}
	}
	return colons == 1 || colons == 2
}

func extractDurationText(s string) string {
	s = strings.TrimSpace(s)
	if isDurationFormat(s) {
		return s
	}
	if match := reDurationPattern.FindString(s); match != "" {
		return match
	}
	return ""
}

func getContinuationToken(continuations []interface{}) string {
	if len(continuations) == 0 {
		return ""
	}
	first, _ := continuations[0].(map[string]interface{})
	return getNavString(first, "nextContinuationData", "continuation")
}
