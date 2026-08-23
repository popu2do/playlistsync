package engine

import (
	"playlistsync/internal/config"
	"playlistsync/internal/model"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/liuzl/gocc"
	"golang.org/x/text/width"
)

// Confidence and Scoring Constants
const (
	// ConfidenceThreshold defines the default minimum score required for an automated match (>= 70)
	ConfidenceThreshold = 70

	// Title match weights
	ScoreTitleExact    = 55
	ScoreTitleContains = 45
	ScoreTitleSimHigh  = 50
	ScoreTitleSimMid   = 40

	// Artist match weights
	ScoreArtistExact        = 30
	ScoreArtistFuzzy        = 25
	ScoreArtistNoneProvided = 20
	ScoreArtistCandEmpty    = 0
	ScoreArtistCrossScript  = 0
	ScoreArtistMismatch     = -15

	// Duration match weights
	ScoreDurationExact         = 15
	ScoreDurationClose         = 10
	ScoreDurationAcceptable    = 5
	ScoreDurationBothMatched   = 10
	ScoreDurationPenaltyMild   = -25
	ScoreDurationPenaltySevere = -40
)

// GetConfidenceThreshold returns the dynamically configured confidence threshold
func GetConfidenceThreshold() int {
	return config.GetConfidenceScore()
}

var (
	t2sConverter *gocc.OpenCC

	// Book title and quotation mark pairs to unwrap/space
	bookPairs = [][2]rune{
		{'《', '》'}, {'『', '』'}, {'「', '」'}, {'〈', '〉'}, {'‹', '›'}, {'«', '»'},
	}

	// Bracket pairs used for metadata extraction & stripping
	bracketPairs = []struct {
		open, close rune
	}{
		{'(', ')'},
		{'[', ']'},
		{'{', '}'},
		{'【', '】'},
		{'〖', '〗'},
		{'〔', '〕'},
		{'〘', '〙'},
		{'〚', '〛'},
	}

	// Structural Regular Expressions for Noise Pattern Matching

	// 1. Media quality, resolution, format tags: 4K, 8K, 1080p, 60fps, HD, HQ, Hi-Res, UHD, Lossless, etc.
	reQualityNoise = regexp.MustCompile(`(?i)^(4k(\s*60fps)?|8k|1080p(\s*60fps)?|720p|60fps|hd|hq|uhd|hi-?res(\s*audio)?|lossless|dolby(\s*atmos)?)$`)

	// 2. Official media indicators: Official Video, Official Music Video, Official MV, Lyric Video, Visualizer, PV, etc.
	reOfficialNoise = regexp.MustCompile(`(?i)^(official(\s+(music\s+)?(video|audio|mv|pv|lyric(\s*video)?|visualizer|clip))?|music\s*video|lyric(\s*video)?|visualizer|m/?v|pv|audio|official\s+audio|special\s+video|performance\s+video)$`)

	// 3. Featured / Collaborative artist annotations: feat. Artist, ft. Artist, with Artist, prod. by Producer
	reFeatNoise = regexp.MustCompile(`(?i)^(feat\.?|ft\.?|featuring|with|prod\.?\s*(by)?)\s+.+$`)

	// 4. Version, performance, audio type & edition metadata: Remastered 2024, Live at Wembley, Acoustic Ver, Instrumental, Remix, etc.
	reVersionNoise = regexp.MustCompile(`(?i)^((re-?)?master(ed)?(\s*\d{4})?|live(\s+(at|in|version|session|performance|concert|tour))?|acoustic(\s*ver(sion)?)?|unplugged|instrumental|inst\.?|off\s*vocal|backing\s*track|karaoke|acapella|a\s*cappella|cover|remix(\s*ver(sion)?)?|club\s*mix|radio\s*edit|extended(\s*mix)?|dance\s*mix|vip\s*mix|edit|deluxe(\s*edition)?|expanded(\s*edition)?|anniversary(\s*edition)?|reissue|bonus(\s*track)?|tv\s*(size|ver\.?)|game\s*(size(\s*inst\.?)?|ver\.?)|short\s*ver\.?|full\s*(ver\.?|album)|opening|ending|op|ed|ost|soundtrack|original\s*(soundtrack|mix)|theme\s*song)$`)

	// 5. CJK (Chinese, Japanese, Korean) semantic noise tokens and phrases
	reCJKNoise = regexp.MustCompile(`(?i)(官方(mv|音樂|音乐|音源|完整版|雙語|双语|字幕)?|双语(字幕)?|雙語(字幕)?|中字|中文字幕|字幕|歌词(版)?|歌詞(版)?|动态歌词|動態歌詞|纯享(版)?|純享(版)?|现场(版)?|現場(版)?|演唱会|演唱會|音乐会|音樂會|舞台|公演|live(版|现场|現場)?|伴奏(版|带)?|纯伴奏|純伴奏|消音伴奏|原声(带)?|原聲(帶)?|原版伴奏|无损(音质)?|無損(音質)?|超清(重制|重製)?|高清(完整版)?|完整版|重制(版)?|重製(版)?|精选(版)?|精選(版)?|特别版|特別版|典藏版|豪华版|豪華版|纪念版|紀念版|录音室版|錄音室版|cd版|首发|首發|首播|翻唱(版)?|吉他版|钢琴版|鋼琴版|清唱|阿卡贝拉|慢摇|慢遙|电音|電音|dj(版|舞曲)?|片头曲|片頭曲|片尾曲|插曲|主题曲|主題曲|推广曲|推廣曲|宣传曲|宣傳曲|影视原声|影視原聲|公式(mv|pv)?|オフィシャル|ミュージックビデオ|リリックビデオ|オーディオ|音源|ライブ(映像)?|コンサート|生演奏|アコースティック|インスト(ゥルメンタル)?|オフボーカル|カラオケ|歌ってみた|リマスター|リミックス|アレンジ|バージョン|オリジナル|フル(バージョン)?|主題歌|挿入歌|アニメ|공식|뮤직비디오|오피셜|음원|오디오|가사|리릭비디오|스페셜|라이브|콘서트|무대|어쿠스틱|직캠|인스트|반주|커버|노래방|리마스터|리믹스|버전|풀버전|티비사이즈|주제가|삽입곡)`)

	// Trailing noise regex matching separator + noise suffix at the end of title
	reTrailingNoise = regexp.MustCompile(`(?i)(?:\s*[-–—/|~～·•]\s*|\s+)(?:official(?:\s+(?:music\s+)?(?:video|audio|mv|pv|lyric(?:\s*video)?|visualizer|clip))|music\s*video|official\s+audio|official\s+mv|lyric\s*video|visualizer|performance\s+video|special\s+video|4k(?:\s*60fps|\s*video)?|8k|hd|hq|hi-?res|1080p|720p|game\s+size(?:\s*inst\.?)?|game\s*ver\.?|tv\s*size|tv\s*ver\.?|short\s*ver\.?|full\s*ver\.?|full\s*album|live(?:\s+at\s+[\w\s]+|\s+version|\s+in\s+concert|\s+session)?|audio|mv|pv|m/v|inst\.?|instrumental|remix(?:ed)?(?:\s*\d{4})?|remaster(?:ed)?(?:\s*\d{4})?|ver\.?|version|edit|mix|cover|acoustic(?:\s*version)?|unplugged|karaoke|bonus\s*track|官方(?:mv|音乐视频|音源|完整版)?|高清完整版|完整版|纯享版|現場版|现场版?|伴奏(?:版)?|纯伴奏|無損(?:音質)?|无损(?:音质)?|超清|高清|雙語字幕|双语字幕|中文字幕|歌词版|重製(?:版)?|重制(?:版)?|公式(?:mv|pv)?|ミュージックビデオ|リリックビデオ|フル|オフボーカル|インスト|ライブ|뮤직비디오|라이브|음원|풀버전|오피셜|가사)\s*$`)

	artistDelimiters = []string{
		",", "/", "&", "+", ";", "|", "、", "×", "和", "與", "与",
		" feat. ", " feat ", " ft. ", " ft ", " FEAT. ", " FT. ",
		" with ", " WITH ",
		" x ", " X ",
		" vs. ", " vs ", " VS. ", " VS ",
		" and ", " AND ",
	}
)

func init() {
	var err error
	t2sConverter, err = gocc.New("t2s")
	if err != nil {
		t2sConverter = nil
	}
}

func convertT2S(s string) string {
	if t2sConverter != nil {
		if out, err := t2sConverter.Convert(s); err == nil {
			return out
		}
	}
	return s
}

func makeExactKey(title string, artists []string) string {
	k := normalizeText(title)
	if len(artists) > 0 {
		k += "#" + normalizeText(artists[0])
	}
	return k
}

// DiffPlan contains the differences computed between Spotify and YouTube Music
type DiffPlan struct {
	ExtraInYTM   []model.YTMTrack
	MissingInYTM []model.SpotifyTrack
	Matched      []model.AddedTrack
}

// ComputeDiff calculates the extra tracks to remove and missing tracks to add
func ComputeDiff(
	spotify *model.SpotifyPlaylist,
	ytm *model.YTMPlaylist,
	knownMapping map[int]string,
) *DiffPlan {
	plan := &DiffPlan{}

	ytmByVid := make(map[string]model.YTMTrack)
	ytmByExactKey := make(map[string]model.YTMTrack)
	for _, t := range ytm.Tracks {
		if t.VideoID != "" {
			ytmByVid[t.VideoID] = t
			ytmByExactKey[makeExactKey(t.Title, t.Artists)] = t
		}
	}

	matchedVids := make(map[string]bool)

	addMatch := func(st model.SpotifyTrack, vid, ytmTitle string) {
		plan.Matched = append(plan.Matched, model.AddedTrack{
			Index:            st.Index,
			Title:            st.Title,
			Artists:          st.Artists,
			TargetTrackID:    vid,
			DestinationTitle: ytmTitle,
		})
		matchedVids[vid] = true
		if knownMapping != nil {
			knownMapping[st.Index] = vid
		}
	}

	for _, st := range spotify.Tracks {
		// 1. Check known persistent/previous mapping
		if vid, hasMap := knownMapping[st.Index]; hasMap && vid != "" {
			if ytmTrack, exists := ytmByVid[vid]; exists {
				addMatch(st, vid, ytmTrack.Title)
				continue
			}
		}

		// 2. Fast O(1) exact match lookup first
		stExactKey := makeExactKey(st.Title, st.Artists)
		if exactMatch, ok := ytmByExactKey[stExactKey]; ok && !matchedVids[exactMatch.VideoID] {
			addMatch(st, exactMatch.VideoID, exactMatch.Title)
			continue
		}

		// 3. Check if any existing track in YTM matches this Spotify track via fuzzy score
		var matchedYTM *model.YTMTrack
		for _, ytTrack := range ytm.Tracks {
			if ytTrack.VideoID == "" || matchedVids[ytTrack.VideoID] {
				continue
			}
			cand := model.YTMSearchResult{
				VideoID:  ytTrack.VideoID,
				Title:    ytTrack.Title,
				Artists:  ytTrack.Artists,
				Duration: ytTrack.Duration,
			}
			if CalculateScore(st, cand) >= ConfidenceThreshold {
				matchedYTM = &ytTrack
				break
			}
		}

		if matchedYTM != nil {
			addMatch(st, matchedYTM.VideoID, matchedYTM.Title)
			continue
		}

		plan.MissingInYTM = append(plan.MissingInYTM, st)
	}

	for _, t := range ytm.Tracks {
		if t.VideoID != "" && !matchedVids[t.VideoID] {
			plan.ExtraInYTM = append(plan.ExtraInYTM, t)
		}
	}

	return plan
}

// EvaluateConfidence checks if a search candidate meets the conservative confidence threshold (>= 70)
func EvaluateConfidence(track model.SpotifyTrack, candidate model.YTMSearchResult) bool {
	return CalculateScore(track, candidate) >= ConfidenceThreshold
}

// CalculateScore computes a comprehensive confidence score (0-100) based on title similarity,
// artist containment, and duration delta tolerance.
func CalculateScore(track model.SpotifyTrack, candidate model.YTMSearchResult) int {
	if candidate.VideoID == "" {
		return 0
	}
	return CalculateTrackScore(track.Title, track.Artists, track.Duration, candidate.Title, candidate.Artists, candidate.Duration)
}

// CalculateTrackScore computes confidence score between two arbitrary tracks
func CalculateTrackScore(title1 string, artists1 []string, dur1 string, title2 string, artists2 []string, dur2 string) int {
	sTitle := normalizeText(title1)
	cTitle := normalizeText(title2)
	sTitleClean := normalizeText(stripNoiseBrackets(title1))
	cTitleClean := normalizeText(stripNoiseBrackets(title2))

	if (sTitle == "" && sTitleClean == "") || (cTitle == "" && cTitleClean == "") {
		return 0
	}

	titleScore, titleMatched := matchTitle(sTitle, cTitle, sTitleClean, cTitleClean, artists1)
	if !titleMatched {
		return 0
	}

	artistScore, artistMatched := matchArtists(artists1, artists2, cTitle, cTitleClean)

	durationScore := matchDuration(dur1, dur2, titleMatched, artistMatched)
	versionPenalty := matchVersionSemantics(title1, title2)

	score := titleScore + artistScore + durationScore + versionPenalty
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}

func matchVersionSemantics(title1, title2 string) int {
	// If source track is not a live track, but candidate track is live, apply severe penalty (-40)
	if !isLiveTrack(title1) && isLiveTrack(title2) {
		return -40
	}
	return 0
}

func isLiveTrack(s string) bool {
	norm := strings.ToLower(s)
	liveKeywords := []string{
		" live", "(live", "[live", "- live", "/ live", "| live",
		"现场", "現場", "라이브", "生演奏", "concert", "unplugged",
	}
	for _, kw := range liveKeywords {
		if strings.Contains(norm, kw) {
			return true
		}
	}
	return false
}

func matchTitle(sTitle, cTitle, sTitleClean, cTitleClean string, trackArtists []string) (int, bool) {
	if sTitle == cTitle || (sTitleClean != "" && sTitleClean == cTitleClean) {
		return ScoreTitleExact, true
	}
	if isValidTitleContains(sTitle, cTitle, trackArtists) ||
		(sTitleClean != "" && cTitleClean != "" && isValidTitleContains(sTitleClean, cTitleClean, trackArtists)) {
		return ScoreTitleContains, true
	}

	sim := runeSimilarity(sTitleClean, cTitleClean)
	if rawSim := runeSimilarity(sTitle, cTitle); rawSim > sim {
		sim = rawSim
	}
	if sim >= 0.75 {
		return int(sim * ScoreTitleSimHigh), true
	}
	if sim >= 0.50 {
		return int(sim * ScoreTitleSimMid), true
	}
	return 0, false
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r)
}

func hasCJKRune(s string) bool {
	for _, r := range s {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

func hasLatinRune(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
}

func isValidTitleContains(sTitle, cTitle string, trackArtists []string) bool {
	if sTitle == "" || cTitle == "" {
		return false
	}
	if sTitle == cTitle {
		return true
	}

	rS := []rune(sTitle)
	rC := []rune(cTitle)

	artistVariants := extractAllArtistVariants(trackArtists)

	checkContainment := func(sub, full string, rSub, rFull []rune) bool {
		if !strings.Contains(full, sub) {
			return false
		}
		// 1. CJK / Hangul / Kana titles are complete semantic entities
		if hasCJKRune(sub) && len(rSub) >= 2 {
			if len(rSub) >= 4 {
				return true
			}
			// For 2-3 char short CJK titles, ensure prefix/suffix is followed by a boundary delimiter
			if strings.HasPrefix(full, sub) {
				rem := full[len(sub):]
				if rem == "" || strings.HasPrefix(rem, " ") || strings.HasPrefix(rem, "-") || strings.HasPrefix(rem, "—") || strings.HasPrefix(rem, "(") || strings.HasPrefix(rem, "（") || strings.HasPrefix(rem, "[") || strings.HasPrefix(rem, "【") {
					return true
				}
			}
			if strings.HasSuffix(full, sub) {
				rem := full[:len(full)-len(sub)]
				if rem == "" || strings.HasSuffix(rem, " ") || strings.HasSuffix(rem, "-") || strings.HasSuffix(rem, "—") || strings.HasSuffix(rem, ")") || strings.HasSuffix(rem, "）") || strings.HasSuffix(rem, "]") || strings.HasSuffix(rem, "】") {
					return true
				}
			}
			accounted := len(rSub)
			for _, a := range artistVariants {
				if normA := normalizeText(a); normA != "" && strings.Contains(full, normA) {
					accounted += len([]rune(normA))
				}
			}
			if float64(accounted)/float64(len(rFull)) >= 0.45 {
				return true
			}
		}
		accounted := len(rSub)
		for _, a := range artistVariants {
			if normA := normalizeText(a); normA != "" && strings.Contains(full, normA) {
				accounted += len([]rune(normA))
			}
		}
		if float64(accounted)/float64(len(rFull)) >= 0.50 {
			return true
		}
		return len(rSub) >= 5 && float64(len(rSub))/float64(len(rFull)) >= 0.45
	}

	return checkContainment(sTitle, cTitle, rS, rC) || checkContainment(cTitle, sTitle, rC, rS)
}

func matchArtists(trackArtists, candArtists []string, cTitle, cTitleClean string) (int, bool) {
	if len(trackArtists) == 0 {
		return ScoreArtistNoneProvided, true
	}

	trackVariants := extractAllArtistVariants(trackArtists)

	// If candidate artist metadata is not provided (e.g. from basic playlist listings),
	// do not penalize if the title or clean title might already contain the song or artist.
	if len(candArtists) == 0 {
		for _, ta := range trackVariants {
			if len([]rune(ta)) >= 3 && (strings.Contains(cTitle, ta) || (cTitleClean != "" && strings.Contains(cTitleClean, ta))) {
				return ScoreArtistFuzzy, true
			}
		}
		return ScoreArtistCandEmpty, true
	}

	candVariants := extractAllArtistVariants(candArtists)

	// 1. Exact artist variant match
	for _, ta := range trackVariants {
		for _, ca := range candVariants {
			if ta == ca {
				return ScoreArtistExact, true
			}
		}
	}

	// 2. Fuzzy artist match
	for _, ta := range trackVariants {
		for _, ca := range candVariants {
			if isArtistFuzzyMatch(ta, ca) {
				return ScoreArtistFuzzy, true
			}
		}
	}

	// 3. Artist contained in candidate title
	for _, ta := range trackVariants {
		if len([]rune(ta)) >= 3 && (strings.Contains(cTitle, ta) || (cTitleClean != "" && strings.Contains(cTitleClean, ta))) {
			return ScoreArtistFuzzy, true
		}
	}

	// 4. Cross-script disjoint check (e.g. Latin vs CJK/Kana/Hangul)
	if isCrossScriptDisjoint(trackVariants, candVariants) {
		return ScoreArtistCrossScript, false
	}

	// 5. Mismatch penalty
	return ScoreArtistMismatch, false
}

func isCrossScriptDisjoint(artists1, artists2 []string) bool {
	checkScripts := func(artists []string) (bool, bool) {
		hasLatin, hasCJK := false, false
		for _, a := range artists {
			if !hasLatin && hasLatinRune(a) {
				hasLatin = true
			}
			if !hasCJK && hasCJKRune(a) {
				hasCJK = true
			}
		}
		return hasLatin, hasCJK
	}

	hasLatin1, hasCJK1 := checkScripts(artists1)
	hasLatin2, hasCJK2 := checkScripts(artists2)

	return (hasLatin1 && !hasCJK1 && hasCJK2 && !hasLatin2) ||
		(hasCJK1 && !hasLatin1 && hasLatin2 && !hasCJK2)
}

func matchDuration(trackDur, candDur string, titleMatched, artistMatched bool) int {
	sSec, sHasDur := ParseDurationSeconds(trackDur)
	cSec, cHasDur := ParseDurationSeconds(candDur)

	if sHasDur && cHasDur && sSec > 0 && cSec > 0 {
		delta := absDiff(sSec, cSec)
		switch {
		case delta <= 3:
			return ScoreDurationExact
		case delta <= 8:
			return ScoreDurationClose
		case delta <= 15:
			return ScoreDurationAcceptable
		case delta > 45:
			return ScoreDurationPenaltySevere
		case delta > 25:
			return ScoreDurationPenaltyMild
		default:
			return 0
		}
	}

	if titleMatched && artistMatched {
		return ScoreDurationBothMatched
	}
	return 0
}

// ParseDurationSeconds parses duration strings like "3:45", "03:45", "1:02:15", "225" into seconds
func ParseDurationSeconds(d string) (int, bool) {
	d = strings.TrimSpace(d)
	if d == "" {
		return 0, false
	}

	d = strings.TrimSuffix(d, " min")
	d = strings.TrimSuffix(d, "s")
	d = strings.TrimSpace(d)

	parts := strings.Split(d, ":")
	switch len(parts) {
	case 1:
		sec, err := strconv.Atoi(parts[0])
		if err == nil && sec > 0 && !strings.Contains(parts[0], "-") {
			return sec, true
		}
	case 2:
		if strings.Contains(parts[0], "-") || strings.Contains(parts[1], "-") {
			return 0, false
		}
		min, err1 := strconv.Atoi(parts[0])
		sec, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil && min >= 0 && sec >= 0 && sec < 60 {
			return min*60 + sec, true
		}
	case 3:
		if strings.Contains(parts[0], "-") || strings.Contains(parts[1], "-") || strings.Contains(parts[2], "-") {
			return 0, false
		}
		hour, err1 := strconv.Atoi(parts[0])
		min, err2 := strconv.Atoi(parts[1])
		sec, err3 := strconv.Atoi(parts[2])
		if err1 == nil && err2 == nil && err3 == nil && hour >= 0 && min >= 0 && min < 60 && sec >= 0 && sec < 60 {
			return hour*3600 + min*60 + sec, true
		}
	}

	return 0, false
}

// NormalizeText normalizes unicode characters, case, and strips punctuation
func NormalizeText(s string) string {
	return normalizeText(s)
}

// StripNoiseBrackets removes extraneous video tags and bracketed annotations from titles
func StripNoiseBrackets(s string) string {
	return stripNoiseBrackets(s)
}

func normalizeUnicode(s string) string {
	folded := width.Fold.String(s)

	var sb strings.Builder
	sb.Grow(len(folded))
	for _, r := range folded {
		switch r {
		case '’', '‘', '`', '´', 'ʻ', 'ʼ', 'ʽ':
			r = '\''
		case '“', '”', '„', '«', '»', '″', '‟', '❝', '❞':
			r = '"'
		case '·', '・', '•', '･', '‧', '∙', 'ᐧ':
			r = ' '
		case '、', '，', '﹐', '‚':
			r = ','
		case '—', '–', '―', '‐', '‑', '‒', '⁃', '➖':
			r = '-'
		case '〜', '～', '∼', '〰':
			r = '~'
		}
		sb.WriteRune(r)
	}
	return sb.String()
}

func normalizeText(s string) string {
	s = normalizeUnicode(s)
	s = convertT2S(s)
	s = strings.ToLower(s)
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func isNoiseContent(s string) bool {
	norm := strings.TrimSpace(strings.ToLower(normalizeUnicode(s)))
	if norm == "" {
		return false
	}

	// 1. Structural pattern matching for English / resolution / audio type
	if reQualityNoise.MatchString(norm) ||
		reOfficialNoise.MatchString(norm) ||
		reFeatNoise.MatchString(norm) ||
		reVersionNoise.MatchString(norm) {
		return true
	}

	// 2. CJK semantic matching (only when CJK runes are present)
	if hasCJKRune(norm) && reCJKNoise.MatchString(norm) {
		return true
	}

	return false
}

func stripTrailingNoise(s string) string {
	changed := true
	for changed {
		changed = false
		trimmed := strings.TrimSpace(s)
		if loc := reTrailingNoise.FindStringIndex(trimmed); loc != nil {
			if rem := strings.TrimSpace(trimmed[:loc[0]]); rem != "" {
				s = rem
				changed = true
			}
		}
	}
	return strings.TrimSpace(s)
}

// stripNoiseBrackets removes extraneous video tags and bracketed commentary from titles
func stripNoiseBrackets(s string) string {
	s = normalizeUnicode(s)
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	runes := []rune(s)
	var unwrappedBook []rune
	for _, r := range runes {
		isBookRune := false
		for _, bp := range bookPairs {
			if r == bp[0] || r == bp[1] {
				isBookRune = true
				break
			}
		}
		if !isBookRune {
			unwrappedBook = append(unwrappedBook, r)
		} else {
			unwrappedBook = append(unwrappedBook, ' ')
		}
	}
	s = strings.TrimSpace(string(unwrappedBook))
	if s == "" {
		return strings.TrimSpace(string(runes))
	}

	s = stripTrailingNoise(s)

	r := []rune(s)
	var out []rune
	i := 0
	for i < len(r) {
		var matchedOpen, matchedClose rune
		for _, p := range bracketPairs {
			if r[i] == p.open {
				matchedOpen = p.open
				matchedClose = p.close
				break
			}
		}
		if matchedOpen != 0 {
			depth := 1
			j := i + 1
			for j < len(r) {
				if r[j] == matchedOpen {
					depth++
				} else if r[j] == matchedClose {
					depth--
					if depth == 0 {
						break
					}
				}
				j++
			}
			if j < len(r) && depth == 0 {
				inner := string(r[i+1 : j])
				if isNoiseContent(inner) {
					i = j + 1
					continue
				}
				remaining := string(out) + string(r[j+1:])
				if strings.TrimSpace(normalizeText(remaining)) == "" {
					out = append(out, []rune(inner)...)
					i = j + 1
					continue
				}
				i = j + 1
				continue
			}
		}
		out = append(out, r[i])
		i++
	}

	cleaned := strings.TrimSpace(string(out))
	if normalizeText(cleaned) == "" {
		var fallback []rune
		for _, ru := range r {
			isBracket := false
			for _, p := range bracketPairs {
				if ru == p.open || ru == p.close {
					isBracket = true
					break
				}
			}
			if !isBracket {
				fallback = append(fallback, ru)
			}
		}
		cleaned = strings.TrimSpace(string(fallback))
	}

	cleaned = stripTrailingNoise(cleaned)
	return strings.TrimSpace(cleaned)
}

func extractAllArtistVariants(artists []string) []string {
	var variants []string
	seen := make(map[string]bool)

	add := func(v string) {
		v = normalizeText(v)
		if v != "" && !seen[v] {
			seen[v] = true
			variants = append(variants, v)
		}
	}

	for _, a := range artists {
		add(a)
	}

	if len(artists) > 1 {
		add(strings.Join(artists, " "))
	}

	for _, a := range artists {
		normA := normalizeUnicode(a)
		subs := []string{normA}
		for _, delim := range artistDelimiters {
			var newSubs []string
			for _, sub := range subs {
				for _, p := range strings.Split(sub, delim) {
					if p = strings.TrimSpace(p); p != "" {
						newSubs = append(newSubs, p)
					}
				}
			}
			subs = newSubs
		}
		for _, sub := range subs {
			add(sub)
		}
	}

	return variants
}

// isArtistFuzzyMatch checks whether two normalized artist name variants match,
// applying length-aware thresholds to prevent short-name false positives.
func isArtistFuzzyMatch(ta, ca string) bool {
	if ta == ca {
		return true
	}
	lenTa, lenCa := len([]rune(ta)), len([]rune(ca))
	// Extremely short names (≤2 chars) require exact match to avoid false positives like "J" ⊂ "Jay"
	if lenTa <= 2 || lenCa <= 2 {
		return ta == ca
	}
	// Containment match is only valid when the substring is at least 60% of the parent
	if strings.Contains(ca, ta) && float64(lenTa)/float64(lenCa) >= 0.6 {
		return true
	}
	if strings.Contains(ta, ca) && float64(lenCa)/float64(lenTa) >= 0.6 {
		return true
	}
	// Short names (≤4 chars) get a stricter similarity threshold (0.85 vs 0.75)
	threshold := 0.75
	if lenTa <= 4 || lenCa <= 4 {
		threshold = 0.85
	}
	return runeSimilarity(ta, ca) >= threshold
}

// runeSimilarity computes the Longest Common Subsequence ratio (0.0 - 1.0) between two strings
func runeSimilarity(s1, s2 string) float64 {
	r1 := []rune(normalizeText(s1))
	r2 := []rune(normalizeText(s2))
	if len(r1) == 0 && len(r2) == 0 {
		return 1.0
	}
	if len(r1) == 0 || len(r2) == 0 {
		return 0.0
	}

	// 1D rolling LCS
	n, m := len(r1), len(r2)
	dp := make([]int, m+1)
	for i := 1; i <= n; i++ {
		prevDiag := 0
		for j := 1; j <= m; j++ {
			temp := dp[j]
			if r1[i-1] == r2[j-1] {
				dp[j] = prevDiag + 1
			} else if dp[j] < dp[j-1] {
				dp[j] = dp[j-1]
			}
			prevDiag = temp
		}
	}
	lcs := dp[m]
	maxLen := n
	if m > maxLen {
		maxLen = m
	}
	return float64(lcs) / float64(maxLen)
}

func absDiff(a, b int) int {
	if a < b {
		return b - a
	}
	return a - b
}
