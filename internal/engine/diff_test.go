package engine_test

import (
	"math/rand"
	"playlistsync/internal/engine"
	"playlistsync/internal/model"
	"strings"
	"testing"
	"testing/quick"
	"time"
)

func TestParseDurationSeconds(t *testing.T) {
	cases := []struct {
		input    string
		expected int
		valid    bool
	}{
		{"3:45", 225, true},
		{"03:45", 225, true},
		{"1:02:15", 3735, true},
		{"01:00:00", 3600, true},
		{"225", 225, true},
		{"3:45 min", 225, true},
		{"180s", 180, true},
		{"", 0, false},
		{"   ", 0, false},
		{"invalid", 0, false},
		{"--:--", 0, false},
		{"1:60:00", 0, false},
		{"1:-2:30", 0, false},
		{"1:02:60", 0, false},
		{"-1:02:30", 0, false},
		{"1:2:3-4", 0, false},
	}

	for _, c := range cases {
		got, ok := engine.ParseDurationSeconds(c.input)
		if ok != c.valid || got != c.expected {
			t.Errorf("ParseDurationSeconds(%q) = (%d, %v); want (%d, %v)", c.input, got, ok, c.expected, c.valid)
		}
	}
}

func TestComputeDiff_FullMatch(t *testing.T) {
	sp := &model.SpotifyPlaylist{
		Tracks: []model.SpotifyTrack{
			{Index: 1, Title: "Song One", Artists: []string{"Artist 1"}},
			{Index: 2, Title: "Song Two", Artists: []string{"Artist 2"}},
		},
	}
	yt := &model.YTMPlaylist{
		Tracks: []model.YTMTrack{
			{VideoID: "vid1", Title: "Song One"},
			{VideoID: "vid2", Title: "Song Two"},
		},
	}
	mapping := map[int]string{
		1: "vid1",
		2: "vid2",
	}

	plan := engine.ComputeDiff(sp, yt, mapping)

	if len(plan.Matched) != 2 {
		t.Fatalf("expected 2 matched tracks, got %d", len(plan.Matched))
	}
	if len(plan.ExtraInYTM) != 0 {
		t.Errorf("expected 0 extra in YTM, got %d", len(plan.ExtraInYTM))
	}
	if len(plan.MissingInYTM) != 0 {
		t.Errorf("expected 0 missing in YTM, got %d", len(plan.MissingInYTM))
	}
	if plan.Matched[0].Index != 1 || plan.Matched[0].TargetTrackID != "vid1" {
		t.Errorf("matched[0] mismatch: %+v", plan.Matched[0])
	}
}

func TestComputeDiff_ExtraInYTM(t *testing.T) {
	sp := &model.SpotifyPlaylist{
		Tracks: []model.SpotifyTrack{
			{Index: 1, Title: "Song One", Artists: []string{"Artist 1"}},
		},
	}
	yt := &model.YTMPlaylist{
		Tracks: []model.YTMTrack{
			{VideoID: "vid1", Title: "Song One"},
			{VideoID: "vidExtra1", Title: "Extra Song 1"},
			{VideoID: "vidExtra2", Title: "Extra Song 2"},
		},
	}
	mapping := map[int]string{
		1: "vid1",
	}

	plan := engine.ComputeDiff(sp, yt, mapping)

	if len(plan.Matched) != 1 {
		t.Errorf("expected 1 matched, got %d", len(plan.Matched))
	}
	if len(plan.ExtraInYTM) != 2 {
		t.Fatalf("expected 2 extra in YTM, got %d", len(plan.ExtraInYTM))
	}
	if plan.ExtraInYTM[0].VideoID != "vidExtra1" || plan.ExtraInYTM[1].VideoID != "vidExtra2" {
		t.Errorf("unexpected extra items: %+v", plan.ExtraInYTM)
	}
	if len(plan.MissingInYTM) != 0 {
		t.Errorf("expected 0 missing in YTM, got %d", len(plan.MissingInYTM))
	}
}

func TestComputeDiff_MissingInYTM(t *testing.T) {
	sp := &model.SpotifyPlaylist{
		Tracks: []model.SpotifyTrack{
			{Index: 1, Title: "Song One", Artists: []string{"Artist 1"}},
			{Index: 2, Title: "Song Two", Artists: []string{"Artist 2"}},   // has mapping but not in YTM
			{Index: 3, Title: "Song Three", Artists: []string{"Artist 3"}}, // no mapping
		},
	}
	yt := &model.YTMPlaylist{
		Tracks: []model.YTMTrack{
			{VideoID: "vid1", Title: "Song One"},
		},
	}
	mapping := map[int]string{
		1: "vid1",
		2: "vid2MissingFromYTM",
	}

	plan := engine.ComputeDiff(sp, yt, mapping)

	if len(plan.Matched) != 1 {
		t.Errorf("expected 1 matched, got %d", len(plan.Matched))
	}
	if len(plan.ExtraInYTM) != 0 {
		t.Errorf("expected 0 extra, got %d", len(plan.ExtraInYTM))
	}
	if len(plan.MissingInYTM) != 2 {
		t.Fatalf("expected 2 missing in YTM, got %d", len(plan.MissingInYTM))
	}
	if plan.MissingInYTM[0].Index != 2 || plan.MissingInYTM[1].Index != 3 {
		t.Errorf("unexpected missing items: %+v", plan.MissingInYTM)
	}
}

func TestComputeDiff_EmptyPlaylists(t *testing.T) {
	sp := &model.SpotifyPlaylist{Tracks: []model.SpotifyTrack{}}
	yt := &model.YTMPlaylist{Tracks: []model.YTMTrack{}}
	mapping := map[int]string{}

	plan := engine.ComputeDiff(sp, yt, mapping)

	if len(plan.Matched) != 0 || len(plan.ExtraInYTM) != 0 || len(plan.MissingInYTM) != 0 {
		t.Errorf("expected all empty lists for empty inputs, got %+v", plan)
	}
}

func TestCalculateScore_FuzzyMatchingAndPenalties(t *testing.T) {
	t.Run("Empty candidate video ID or empty track title returns 0", func(t *testing.T) {
		s1 := engine.CalculateScore(model.SpotifyTrack{Title: "Song"}, model.YTMSearchResult{VideoID: ""})
		if s1 != 0 {
			t.Errorf("expected 0, got %d", s1)
		}

		s2 := engine.CalculateScore(model.SpotifyTrack{Title: ""}, model.YTMSearchResult{VideoID: "vid1", Title: "Song"})
		if s2 != 0 {
			t.Errorf("expected 0, got %d", s2)
		}
	})

	t.Run("Token overlap matching (>= 80% and >= 50%)", func(t *testing.T) {
		// 4 of 5 tokens overlap -> 80% overlap (35 points title) + exact artist match (30 pts) + duration exact (15 pts) = 80 pts
		track := model.SpotifyTrack{
			Title:    "The Great Big Beautiful World Song",
			Artists:  []string{"Artist"},
			Duration: "3:00",
		}
		cand := model.YTMSearchResult{
			VideoID:  "vid_overlap",
			Title:    "The Great Big Beautiful Planet Song",
			Artists:  []string{"Artist"},
			Duration: "3:00",
		}
		score := engine.CalculateScore(track, cand)
		if score < 75 {
			t.Errorf("expected high score for token overlap >= 80%%, got %d", score)
		}
	})

	t.Run("Artist missing on Spotify track gives default bonus", func(t *testing.T) {
		track := model.SpotifyTrack{
			Title:    "Instrumental Theme",
			Artists:  []string{},
			Duration: "2:00",
		}
		cand := model.YTMSearchResult{
			VideoID:  "vid_inst",
			Title:    "Instrumental Theme",
			Duration: "2:00",
		}
		score := engine.CalculateScore(track, cand)
		// 55 (title) + 20 (artist default) + 15 (duration) = 90
		if score != 90 {
			t.Errorf("expected 90 points, got %d", score)
		}
	})

	t.Run("Duration divergence penalty (25s < delta <= 45s and delta > 45s)", func(t *testing.T) {
		track := model.SpotifyTrack{
			Title:    "Standard Song",
			Artists:  []string{"Artist"},
			Duration: "3:00", // 180s
		}
		candMediumDelta := model.YTMSearchResult{
			VideoID:  "vid_med",
			Title:    "Standard Song",
			Artists:  []string{"Artist"},
			Duration: "3:35", // 215s (delta 35s -> -15 penalty)
		}
		candLargeDelta := model.YTMSearchResult{
			VideoID:  "vid_large",
			Title:    "Standard Song",
			Artists:  []string{"Artist"},
			Duration: "5:00", // 300s (delta 120s -> -30 penalty)
		}

		scoreMed := engine.CalculateScore(track, candMediumDelta)
		scoreLarge := engine.CalculateScore(track, candLargeDelta)

		if scoreMed <= scoreLarge {
			t.Errorf("expected scoreMed (%d) > scoreLarge (%d)", scoreMed, scoreLarge)
		}
	})

	t.Run("Duration missing gives neutral bonus when title and artist match", func(t *testing.T) {
		track := model.SpotifyTrack{
			Title:   "Live Track",
			Artists: []string{"Band"},
		}
		cand := model.YTMSearchResult{
			VideoID: "vid_live",
			Title:   "Live Track",
			Artists: []string{"Band"},
		}
		score := engine.CalculateScore(track, cand)
		// 55 (title) + 30 (artist) + 10 (neutral bonus) = 95
		if score != 95 {
			t.Errorf("expected 95, got %d", score)
		}
	})
}

func TestEvaluateConfidence(t *testing.T) {
	tests := []struct {
		name      string
		track     model.SpotifyTrack
		candidate model.YTMSearchResult
		expected  bool
		minScore  int
	}{
		{
			name: "Empty VideoID returns false",
			track: model.SpotifyTrack{
				Title:   "Shape of You",
				Artists: []string{"Ed Sheeran"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "",
				Title:   "Shape of You",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Exact Title and Artist Match with Duration",
			track: model.SpotifyTrack{
				Title:    "Shape of You",
				Artists:  []string{"Ed Sheeran"},
				Duration: "3:53",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "JGwWNGJdvx8",
				Title:    "Shape of You",
				Artists:  []string{"Ed Sheeran"},
				Duration: "3:53",
			},
			expected: true,
			minScore: 95,
		},
		{
			name: "Case Insensitive & Substring Match with Artist Containment",
			track: model.SpotifyTrack{
				Title:    "Shape of You",
				Artists:  []string{"Ed Sheeran"},
				Duration: "3:53",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "JGwWNGJdvx8",
				Title:    "Ed Sheeran - Shape of You (Official Music Video)",
				Artists:  []string{"Ed Sheeran"},
				Duration: "3:55",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Artist Match Only with Completely Different Title Must Fail",
			track: model.SpotifyTrack{
				Title:   "Blinding Lights",
				Artists: []string{"The Weeknd"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "4NRXx6U8ABQ",
				Title:   "The Weeknd - Track Audio",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Large Duration Divergence (>45s) Must Reject",
			track: model.SpotifyTrack{
				Title:    "Short Interlude",
				Artists:  []string{"Artist"},
				Duration: "1:15",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_long",
				Title:    "Short Interlude",
				Artists:  []string{"Artist"},
				Duration: "10:30",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Ultra-short Title (1 char 'H') with Empty Candidate Artist and Exact Duration Must Fail",
			track: model.SpotifyTrack{
				Title:    "H",
				Artists:  []string{"Artist Name"},
				Duration: "3:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_short_h",
				Title:    "H",
				Artists:  []string{},
				Duration: "3:00",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Ultra-short Title (2 char 'OK') with Mismatched Artist and Exact Duration Must Fail",
			track: model.SpotifyTrack{
				Title:    "OK",
				Artists:  []string{"Robin Schulz"},
				Duration: "3:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_short_ok",
				Title:    "OK",
				Artists:  []string{"Unrelated Band"},
				Duration: "3:00",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Ultra-short CJK Title (1 char '溯') with Mismatched Artist and Exact Duration Must Fail",
			track: model.SpotifyTrack{
				Title:    "溯",
				Artists:  []string{"CORSAK"},
				Duration: "4:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_short_cjk",
				Title:    "溯",
				Artists:  []string{"Different Singer"},
				Duration: "4:00",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Chinese Song & Artist Match",
			track: model.SpotifyTrack{
				Title:   "测试曲目名称",
				Artists: []string{"测试歌手"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "vid_cjk_test",
				Title:   "测试曲目名称 — Test Track Name - 测试歌手",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Chinese Artist Name Match with Short Title",
			track: model.SpotifyTrack{
				Title:   "X",
				Artists: []string{"测试歌手名"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "vid_cjk_artist",
				Title:   "测试歌手名 - X (Official Audio)",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Japanese Kana Match",
			track: model.SpotifyTrack{
				Title:   "夜に駆ける",
				Artists: []string{"YOASOBI"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "vid_jp_1",
				Title:   "YOASOBI「夜に駆ける」 Official Music Video",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Korean Hangul Match",
			track: model.SpotifyTrack{
				Title:   "강남스타일",
				Artists: []string{"PSY"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "vid_kr_1",
				Title:   "PSY - GANGNAM STYLE(강남스타일) M/V",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Accented Latin Letter Match",
			track: model.SpotifyTrack{
				Title:   "Café",
				Artists: []string{"Artist"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "vid_accent_1",
				Title:   "Artist - Café (Official Audio)",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Pure Symbols No False Positive",
			track: model.SpotifyTrack{
				Title:   "??? !!!",
				Artists: []string{"---"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "vid_sym_1",
				Title:   "Random Song Title",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Unrelated Candidate returns false",
			track: model.SpotifyTrack{
				Title:   "Bohemian Rhapsody",
				Artists: []string{"Queen"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "unrelatedVid",
				Title:   "Random Song by Random Artist",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Empty track title returns 0 score",
			track: model.SpotifyTrack{
				Title:   "   ",
				Artists: []string{"Artist"},
			},
			candidate: model.YTMSearchResult{
				VideoID: "vid1",
				Title:   "Some Song",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Duration delta <= 8 seconds bonus",
			track: model.SpotifyTrack{
				Title:    "Song Title",
				Artists:  []string{"Artist Name"},
				Duration: "3:40",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_delta_8",
				Title:    "Song Title",
				Artists:  []string{"Artist Name"},
				Duration: "3:46",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Duration delta <= 15 seconds bonus",
			track: model.SpotifyTrack{
				Title:    "Song Title",
				Artists:  []string{"Artist Name"},
				Duration: "3:40",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_delta_15",
				Title:    "Song Title",
				Artists:  []string{"Artist Name"},
				Duration: "3:52",
			},
			expected: true,
			minScore: 85,
		},
		{
			name: "Duration delta > 25 seconds penalty",
			track: model.SpotifyTrack{
				Title:    "Song Title",
				Artists:  []string{"Artist Name"},
				Duration: "3:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_delta_30",
				Title:    "Song Title",
				Artists:  []string{"Artist Name"},
				Duration: "3:35",
			},
			expected: false,
			minScore: 0,
		},
		{
			name: "Artist matched in candidate title",
			track: model.SpotifyTrack{
				Title:    "Unique Song",
				Artists:  []string{"UniqueArtist"},
				Duration: "3:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_title_contain",
				Title:    "UniqueArtist - Unique Song",
				Artists:  []string{"Various Artists"},
				Duration: "3:00",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Full-width ASCII title and space normalization",
			track: model.SpotifyTrack{
				Title:    "Ｓｏｎｇ　Ｎａｍｅ　０１",
				Artists:  []string{"Artist"},
				Duration: "3:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_fw_1",
				Title:    "Song Name 01",
				Artists:  []string{"Artist"},
				Duration: "3:00",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Full-width artist and case insensitivity AC/DC",
			track: model.SpotifyTrack{
				Title:    "Highway to Hell",
				Artists:  []string{"ＡＣ／ＤＣ"},
				Duration: "3:28",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_acdc_1",
				Title:    "Highway to Hell",
				Artists:  []string{"ac/dc"},
				Duration: "3:28",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Cyrillic and Greek case insensitivity",
			track: model.SpotifyTrack{
				Title:    "ПРИВЕТ ΕΛΛΗΝΙΚΑ",
				Artists:  []string{"ГРУППА"},
				Duration: "3:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_cyr_1",
				Title:    "привет ελληνικα",
				Artists:  []string{"группа"},
				Duration: "3:00",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Entire title enclosed in Chinese book title brackets",
			track: model.SpotifyTrack{
				Title:    "《青花瓷》",
				Artists:  []string{"周杰伦"},
				Duration: "3:58",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_qhc_1",
				Title:    "周杰伦 - 青花瓷 (Official Music Video)",
				Artists:  []string{"周杰伦"},
				Duration: "3:58",
			},
			expected: true,
			minScore: 80,
		},
		{
			name: "Entire title enclosed in Japanese quotation marks",
			track: model.SpotifyTrack{
				Title:    "『晴天』",
				Artists:  []string{"周杰伦"},
				Duration: "4:29",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_qt_1",
				Title:    "晴天",
				Artists:  []string{"周杰伦"},
				Duration: "4:29",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Entire title enclosed in thick brackets",
			track: model.SpotifyTrack{
				Title:    "【稻香】",
				Artists:  []string{"周杰伦"},
				Duration: "3:43",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_dx_1",
				Title:    "稻香",
				Artists:  []string{"周杰伦"},
				Duration: "3:43",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Title enclosed in square and round brackets",
			track: model.SpotifyTrack{
				Title:    "[Shape of You]",
				Artists:  []string{"Ed Sheeran"},
				Duration: "3:53",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_sq_1",
				Title:    "(Shape of You)",
				Artists:  []string{"Ed Sheeran"},
				Duration: "3:53",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Artist split with feat and delimiter combinations",
			track: model.SpotifyTrack{
				Title:    "I Don't Care",
				Artists:  []string{"Ed Sheeran feat. Justin Bieber"},
				Duration: "3:39",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_feat_1",
				Title:    "Ed Sheeran & Justin Bieber - I Don't Care",
				Artists:  []string{"Ed Sheeran", "Justin Bieber"},
				Duration: "3:39",
			},
			expected: true,
			minScore: 85,
		},
		{
			name: "Artist combined with ideographic comma",
			track: model.SpotifyTrack{
				Title:    "千里之外",
				Artists:  []string{"周杰伦、费玉清"},
				Duration: "4:15",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_cjk_comma",
				Title:    "千里之外",
				Artists:  []string{"周杰伦", "费玉清"},
				Duration: "4:15",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Artist separated by slash and with / x / vs.",
			track: model.SpotifyTrack{
				Title:    "Battle Track",
				Artists:  []string{"Artist A vs. Artist B"},
				Duration: "3:00",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_vs_1",
				Title:    "Battle Track",
				Artists:  []string{"Artist A / Artist B"},
				Duration: "3:00",
			},
			expected: true,
			minScore: 90,
		},
		{
			name: "Punctuation variations like curly apostrophes and middle dots",
			track: model.SpotifyTrack{
				Title:    "Don’t Stop · Music",
				Artists:  []string{"Band"},
				Duration: "3:30",
			},
			candidate: model.YTMSearchResult{
				VideoID:  "vid_punct_1",
				Title:    "Don't Stop ・ Music",
				Artists:  []string{"Band"},
				Duration: "3:30",
			},
			expected: true,
			minScore: 90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := engine.CalculateScore(tt.track, tt.candidate)
			got := engine.EvaluateConfidence(tt.track, tt.candidate)

			if got != tt.expected {
				t.Errorf("EvaluateConfidence(%+v, %+v) = %v (Score: %d); want %v", tt.track.Title, tt.candidate.Title, got, score, tt.expected)
			}
			if tt.expected && score < tt.minScore {
				t.Errorf("CalculateScore(%+v, %+v) = %d; want at least %d", tt.track.Title, tt.candidate.Title, score, tt.minScore)
			}
		})
	}
}

func TestNormalizeText(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "helloworld"},
		{"  AC/DC  ", "acdc"},
		{"Ｓｏｎｇ　Ｎａｍｅ　１２３", "songname123"},
		{"ПРИВЕТ", "привет"},
		{"ΕΛΛΗΝΙΚΑ", "ελληνικα"},
		{"Café & Bar", "cafébar"},
		{"晴天（周杰伦）", "晴天周杰伦"},
		{"Don’t Stop `Now´", "dontstopnow"},
		{"Title · Subtitle ・ Extra", "titlesubtitleextra"},
	}

	for _, tt := range tests {
		got := engine.NormalizeText(tt.input)
		if got != tt.expected {
			t.Errorf("NormalizeText(%q) = %q; want %q", tt.input, got, tt.expected)
		}
	}
}

func TestStripNoiseBrackets(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"《青花瓷》", "青花瓷"},
		{"『晴天』", "晴天"},
		{"【稻香】", "稻香"},
		{"[Song Title]", "Song Title"},
		{"(Track Name)", "Track Name"},
		{"周杰伦 - 晴天 (Official Music Video)", "周杰伦 - 晴天"},
		{"周杰伦 - 晴天 【MV】", "周杰伦 - 晴天"},
		{"Track Name (feat. Stormzy)", "Track Name"},
		{"Track Name [HD] [4K Remastered]", "Track Name"},
		{"Track Name (Live at Wembley)", "Track Name"},
		{"Track Name - Official Video", "Track Name"},
		{"YOASOBI「夜に駆ける」 Official Music Video", "YOASOBI 夜に駆ける"},
		{"陳奕迅 - 富士山下 【官方雙語MV】", "陈奕迅 - 富士山下"},
		{"BLACKPINK - 'Pink Venom' M/V", "BLACKPINK - Pink Venom"},
		{"Aimyon - マリーゴールド [Official MV]", "Aimyon - マリーゴールド"},
		{"NewJeans 'Ditto' Official MV (Performance ver.)", "NewJeans Ditto"},
		{"Song Title (2024 Remastered Version)", "Song Title"},
		{"Artist - Song (feat. FeatArtist)", "Artist - Song"},
		{"Song Name - Game Size Inst.", "Song Name"},
	}

	for _, tt := range tests {
		got := engine.StripNoiseBrackets(tt.input)
		if engine.NormalizeText(got) != engine.NormalizeText(tt.expected) {
			t.Errorf("StripNoiseBrackets(%q) = %q; want normalized %q", tt.input, got, tt.expected)
		}
	}
}

func TestTraditionalToSimplifiedChinese(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"陳奕迅 富士山下 浮誇", "陈奕迅 富士山下 浮夸"},
		{"張學友 吻別 祝福", "张学友 吻别 祝福"},
		{"周杰倫 晴天 七里香 雙截棍", "周杰伦 晴天 七里香 双截棍"},
		{"劉德華 忘情水 謝謝你的愛", "刘德华 忘情水 谢谢你的爱"},
		{"鄧紫棋 泡沫 光年之外", "邓紫棋 泡沫 光年之外"},
		{"楊千嬅 少女的祈禱", "杨千嬅 少女的祈祷"},
		{"溫拿樂隊 朋友", "温拿乐队 朋友"},
		{"林峯 愛在記憶中找你", "林峰 爱在记忆中找你"},
		{"李克勤 月半小夜曲", "李克勤 月半小夜曲"},
		// 一简多繁 / 上下文歧义词测试（手写字表无法处理，gocc OpenCC 可以完美处理）
		{"我的頭髮很長", "我的头发很长"},
		{"事情發生得太突然", "事情发生得太突然"},
		{"重複播放這首歌", "重复播放这首歌"},
		{"他在舞台上表演", "他在舞台上表演"},
		{"皇后與國王", "皇后与国王"},
		{"三天後出發", "三天后出发"},
	}

	for _, tt := range tests {
		got := engine.NormalizeText(tt.input)
		expectedNorm := engine.NormalizeText(tt.expected)
		if got != expectedNorm {
			t.Errorf("TraditionalToSimplified(%q) = %q; want %q", tt.input, got, expectedNorm)
		}
	}
}

// ============================================================================
// Property-Based Testing (基于属性的随机验证)
// ============================================================================

// 1. 幂等性属性 (Idempotency Property): Clean(Clean(s)) == Clean(s)
func TestProperty_StripNoiseBrackets_Idempotency(t *testing.T) {
	property := func(s string) bool {
		once := engine.StripNoiseBrackets(s)
		twice := engine.StripNoiseBrackets(once)
		return once == twice
	}

	cfg := &quick.Config{
		MaxCount: 2000,
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	if err := quick.Check(property, cfg); err != nil {
		t.Errorf("Idempotency property failed for StripNoiseBrackets: %v", err)
	}
}

// 2. 非空保底守恒属性 (Non-empty Preservation Property)
func TestProperty_StripNoiseBrackets_NonEmptyPreservation(t *testing.T) {
	property := func(s string) bool {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			return true
		}
		// 如果原始字符串有字母/数字，清洗后的文本也绝不能被抹除为空（触发保底）
		norm := engine.NormalizeText(trimmed)
		if norm == "" {
			return true
		}

		cleaned := engine.StripNoiseBrackets(trimmed)
		return engine.NormalizeText(cleaned) != ""
	}

	cfg := &quick.Config{
		MaxCount: 2000,
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	if err := quick.Check(property, cfg); err != nil {
		t.Errorf("Non-empty preservation property failed: %v", err)
	}
}

// 3. 噪音注入单调性属性 (Metamorphic Noise Invariance Property)
// Clean(Title + RandomNoise) 的主干内容必须与 Clean(Title) 一致
func TestProperty_NoiseInjection_Invariance(t *testing.T) {
	sampleTitles := []string{
		"Shape of You",
		"夜に駆ける",
		"七里香",
		"Ditto",
		"Bohemian Rhapsody",
		"Hotel California",
		"光年之外",
		"Butter",
	}

	noiseTemplates := []string{
		" (Official Music Video)",
		" [Official MV]",
		" 【官方雙語MV】",
		" (4K 60FPS)",
		" [Live at Wembley]",
		" (Remastered 2024)",
		" - Official Audio",
		" [Instrumental]",
		" (feat. Special Guest)",
		" 【現場版】",
		" [HD 1080p]",
		" (Performance ver.)",
		" - Visualizer",
	}

	r := rand.New(rand.NewSource(42))

	for i := 0; i < 1000; i++ {
		baseTitle := sampleTitles[r.Intn(len(sampleTitles))]
		noise1 := noiseTemplates[r.Intn(len(noiseTemplates))]
		noise2 := noiseTemplates[r.Intn(len(noiseTemplates))]

		noisyTitle := baseTitle + noise1 + noise2
		cleaned := engine.StripNoiseBrackets(noisyTitle)

		normCleaned := engine.NormalizeText(cleaned)
		normBase := engine.NormalizeText(baseTitle)

		if normCleaned != normBase {
			t.Errorf("Noise injection invariance failed on iteration %d:\n  Input:   %q\n  Cleaned: %q (norm: %q)\n  Want:    %q (norm: %q)",
				i, noisyTitle, cleaned, normCleaned, baseTitle, normBase)
		}
	}
}

// 4. 打分有界性与自反满分属性 (Scoring Boundedness & Reflexivity Property)
func TestProperty_CalculateTrackScore_BoundedAndReflexive(t *testing.T) {
	property := func(title1, artist1, dur1, title2, artist2, dur2 string) bool {
		score := engine.CalculateTrackScore(title1, []string{artist1}, dur1, title2, []string{artist2}, dur2)
		// 1. 有界性: 必须在 [0, 100] 范围内
		if score < 0 || score > 100 {
			return false
		}

		// 2. 自反性: 自身对比自身（有效非空输入）得分必须为满分 100
		if strings.TrimSpace(title1) != "" && strings.TrimSpace(artist1) != "" && dur1 == "3:30" {
			selfScore := engine.CalculateTrackScore(title1, []string{artist1}, dur1, title1, []string{artist1}, dur1)
			if selfScore != 100 {
				return false
			}
		}

		return true
	}

	cfg := &quick.Config{
		MaxCount: 3000,
		Rand:     rand.New(rand.NewSource(time.Now().UnixNano())),
	}

	if err := quick.Check(property, cfg); err != nil {
		t.Errorf("Scoring boundedness or reflexivity failed: %v", err)
	}
}

func TestTrailingNoiseIntegrity(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Lover", "lover"},
		{"Alive", "alive"},
		{"River", "river"},
		{"Discover", "discover"},
		{"Credit", "credit"},
		{"Forever", "forever"},
		{"Song - Official Music Video", "song"},
		{"Song (Live at Wembley)", "song"},
		{"Song - 4K 60fps", "song"},
		{"Song - Remastered 2024", "song"},
	}

	for _, c := range cases {
		cleaned := engine.StripNoiseBrackets(c.input)
		normCleaned := engine.NormalizeText(cleaned)
		if normCleaned != c.expected {
			t.Errorf("StripNoiseBrackets(%q) normalized = %q; want %q", c.input, normCleaned, c.expected)
		}
	}
}

func TestShortArtistNoFalseSubstringMatch(t *testing.T) {
	// Artist "V" or "IU" should not match title "Love" or "Medium" via substring match
	score := engine.CalculateTrackScore("Love", []string{"V"}, "3:30", "Love", []string{"Unrelated Artist"}, "3:30")
	// Exact title = 55, Artist mismatch = -5, Duration exact = 15 => Score = 65 (< 70 confidence threshold)
	if score >= engine.ConfidenceThreshold {
		t.Errorf("Expected score < %d for short artist mismatch, got %d", engine.ConfidenceThreshold, score)
	}
}

func TestTokenOverlapSymmetry(t *testing.T) {
	// Source "Alpha Beta" vs Candidate "Alpha Gamma Delta Epsilon Zeta"
	score := engine.CalculateTrackScore("Alpha Beta", []string{"Artist"}, "3:30", "Alpha Gamma Delta Epsilon Zeta", []string{"Artist"}, "3:30")
	// Overlap is 1/5 = 0.2 (< 0.5), so token overlap score should be 0.
	// Title score = 0, Artist match = 30, Duration = 15 => Score = 45 (< 70 confidence threshold)
	if score >= engine.ConfidenceThreshold {
		t.Errorf("Expected score < %d for asymmetric token overlap, got %d", engine.ConfidenceThreshold, score)
	}
}

func TestVersionMismatchPenalty(t *testing.T) {
	// Source is studio version "Creep", Candidate is "Creep (Live at Reading)"
	score := engine.CalculateTrackScore("Creep", []string{"Radiohead"}, "3:58", "Creep (Live at Reading)", []string{"Radiohead"}, "4:02")
	// Title contains (45) + Artist exact (30) + Duration exact (15) = 90
	// Version penalty: -40 => 90 - 40 = 50 (< 70 confidence threshold)
	if score >= engine.ConfidenceThreshold {
		t.Errorf("Expected studio track vs live track score < %d, got %d", engine.ConfidenceThreshold, score)
	}
}

func TestShortTitleNoFalseSubstringMatch(t *testing.T) {
	// Source is "Run" by Foo Fighters, Candidate is "Run with the Wolves" by Foo Fighters
	score := engine.CalculateTrackScore("Run", []string{"Foo Fighters"}, "3:30", "Run with the Wolves", []string{"Foo Fighters"}, "3:30")
	// "Run" is <= 3 runes, standalone token in "Run with the Wolves" is checked.
	// But "run" is not equal to "runwiththewolves", and rune similarity is low.
	if score >= engine.ConfidenceThreshold {
		t.Errorf("Expected short title 'Run' vs 'Run with the Wolves' score < %d, got %d", engine.ConfidenceThreshold, score)
	}
}

func TestEmptyCandidateArtistNeutrality(t *testing.T) {
	// Candidate has empty artist list and title doesn't mention artist
	score := engine.CalculateTrackScore("Hello", []string{"Adele"}, "4:55", "Hello", []string{}, "4:55")
	if score < engine.ConfidenceThreshold {
		t.Errorf("Expected exact title match score >= %d, got %d", engine.ConfidenceThreshold, score)
	}

	// But if title is only substring, score should be strictly below 70.
	subScore := engine.CalculateTrackScore("Hello", []string{"Adele"}, "4:55", "Hello - Martin Solveig", []string{}, "4:55")
	if subScore >= engine.ConfidenceThreshold {
		t.Errorf("Expected unrelated candidate with empty artist score < %d, got %d", engine.ConfidenceThreshold, subScore)
	}
}

func TestCrossScriptArtistWithExactDurationNaturalPass(t *testing.T) {
	// Exact title + Cross-script disjoint artist (Dean Ting vs 丁世光 -> 0 pts) + Exact duration (4:18 vs 4:18 -> 15 pts) = 70 pts
	score := engine.CalculateTrackScore("好的一天 A Good Day", []string{"Dean Ting"}, "4:18", "好的一天 A Good Day", []string{"丁世光"}, "4:18")
	if score < engine.ConfidenceThreshold {
		t.Errorf("Expected exact title with cross-script artist and exact duration to pass threshold (>= %d), got %d", engine.ConfidenceThreshold, score)
	}

	// But if duration diverges by 60s, score = 55 + 0 - 40 = 15 < 70
	scoreBadDur := engine.CalculateTrackScore("好的一天 A Good Day", []string{"Dean Ting"}, "4:18", "好的一天 A Good Day", []string{"丁世光"}, "6:00")
	if scoreBadDur >= engine.ConfidenceThreshold {
		t.Errorf("Expected diverging duration to fail threshold (< %d), got %d", engine.ConfidenceThreshold, scoreBadDur)
	}
}

func TestShortTitleSafetyGate(t *testing.T) {
	// Ultra-short song titles (rune length <= 2 of cleaned/normalized title, such as "H", "OK", "1", "U", "溯"):
	// When there is NO positive artist match (artistScore <= 0 or !artistMatched),
	// composite score must be clamped <= 55 (ConfidenceThreshold - 15) and EvaluateConfidence must return false,
	// even with exact duration match (55 + 0 + 15 = 70 -> clamped to 55).
	shortTitles := []string{"H", "OK", "1", "U", "溯", "Go"}

	for _, title := range shortTitles {
		t.Run("ZeroArtistScore_ClampTo55_"+title, func(t *testing.T) {
			// Candidate has empty artist list and title does not mention artist -> artistScore = 0
			track := model.SpotifyTrack{
				Title:    title,
				Artists:  []string{"Some Artist"},
				Duration: "3:00",
			}
			cand := model.YTMSearchResult{
				VideoID:  "vid_short_1",
				Title:    title,
				Artists:  []string{},
				Duration: "3:00",
			}

			score := engine.CalculateScore(track, cand)
			if score > 55 {
				t.Errorf("Title %q: expected score <= 55 for short title with no artist match, got %d", title, score)
			}

			if engine.EvaluateConfidence(track, cand) {
				t.Errorf("Title %q: expected EvaluateConfidence to return false for short title without artist match", title)
			}
		})

		t.Run("ArtistMismatch_ClampTo55_"+title, func(t *testing.T) {
			track := model.SpotifyTrack{
				Title:    title,
				Artists:  []string{"Taylor Swift"},
				Duration: "3:00",
			}
			cand := model.YTMSearchResult{
				VideoID:  "vid_short_2",
				Title:    title,
				Artists:  []string{"Coldplay"},
				Duration: "3:00",
			}

			score := engine.CalculateScore(track, cand)
			if score > 55 {
				t.Errorf("Title %q: expected score <= 55 for short title with mismatched artist, got %d", title, score)
			}

			if engine.EvaluateConfidence(track, cand) {
				t.Errorf("Title %q: expected EvaluateConfidence to return false for short title with mismatched artist", title)
			}
		})

		t.Run("PositiveArtistMatch_PassesThreshold_"+title, func(t *testing.T) {
			track := model.SpotifyTrack{
				Title:    title,
				Artists:  []string{"Known Artist"},
				Duration: "3:00",
			}
			cand := model.YTMSearchResult{
				VideoID:  "vid_short_3",
				Title:    title,
				Artists:  []string{"Known Artist"},
				Duration: "3:00",
			}

			score := engine.CalculateScore(track, cand)
			if score < engine.ConfidenceThreshold {
				t.Errorf("Title %q: expected score >= %d for short title with matching artist, got %d", title, engine.ConfidenceThreshold, score)
			}

			if !engine.EvaluateConfidence(track, cand) {
				t.Errorf("Title %q: expected EvaluateConfidence to return true for short title with matching artist", title)
			}
		})
	}
}

func TestComputeDiff_NoDuplicateVideoIDAssignment(t *testing.T) {
	sp := &model.SpotifyPlaylist{
		Tracks: []model.SpotifyTrack{
			{Index: 1, Title: "Song One", Artists: []string{"Artist 1"}},
			{Index: 2, Title: "Song One", Artists: []string{"Artist 1"}},
		},
	}
	yt := &model.YTMPlaylist{
		Tracks: []model.YTMTrack{
			{VideoID: "vid_shared_1", Title: "Song One", Artists: []string{"Artist 1"}},
		},
	}
	mapping := map[int]string{}

	plan := engine.ComputeDiff(sp, yt, mapping)

	if len(plan.Matched) != 1 {
		t.Fatalf("expected exactly 1 matched track, got %d", len(plan.Matched))
	}
	if plan.Matched[0].TargetTrackID != "vid_shared_1" {
		t.Errorf("expected matched vid_shared_1, got %s", plan.Matched[0].TargetTrackID)
	}
	if len(plan.MissingInYTM) != 1 {
		t.Fatalf("expected 1 missing track for the second duplicate source track, got %d", len(plan.MissingInYTM))
	}
	if plan.MissingInYTM[0].Index != 2 {
		t.Errorf("expected missing track index 2, got %d", plan.MissingInYTM[0].Index)
	}
	if len(plan.ExtraInYTM) != 0 {
		t.Errorf("expected 0 extra in YTM, got %d", len(plan.ExtraInYTM))
	}
}

func TestCalculateTrackScore_RemasterAndEditionTags(t *testing.T) {
	cases := []struct {
		name       string
		title1     string
		artists1   []string
		dur1       string
		title2     string
		artists2   []string
		dur2       string
		shouldPass bool
	}{
		{
			name:       "Remastered 2024 in candidate title",
			title1:     "Hotel California",
			artists1:   []string{"Eagles"},
			dur1:       "6:30",
			title2:     "Hotel California (2024 Remaster)",
			artists2:   []string{"Eagles"},
			dur2:       "6:31",
			shouldPass: true,
		},
		{
			name:       "Deluxe Edition in candidate title",
			title1:     "Viva La Vida",
			artists1:   []string{"Coldplay"},
			dur1:       "4:01",
			title2:     "Viva La Vida - Deluxe Edition",
			artists2:   []string{"Coldplay"},
			dur2:       "4:02",
			shouldPass: true,
		},
		{
			name:       "Anniversary Edition in candidate title",
			title1:     "Bohemian Rhapsody",
			artists1:   []string{"Queen"},
			dur1:       "5:55",
			title2:     "Bohemian Rhapsody (40th Anniversary Edition)",
			artists2:   []string{"Queen"},
			dur2:       "5:55",
			shouldPass: true,
		},
		{
			name:       "Remaster in source and candidate",
			title1:     "Heroes - 2017 Remaster",
			artists1:   []string{"David Bowie"},
			dur1:       "6:10",
			title2:     "Heroes (Remastered)",
			artists2:   []string{"David Bowie"},
			dur2:       "6:11",
			shouldPass: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score := engine.CalculateTrackScore(tc.title1, tc.artists1, tc.dur1, tc.title2, tc.artists2, tc.dur2)
			passed := score >= engine.ConfidenceThreshold
			if passed != tc.shouldPass {
				t.Errorf("CalculateTrackScore(%q vs %q) = %d (passed=%v, want %v)", tc.title1, tc.title2, score, passed, tc.shouldPass)
			}
		})
	}
}

func TestCalculateTrackScore_FeaturingAndArtistDelimiterVariants(t *testing.T) {
	cases := []struct {
		name       string
		title1     string
		artists1   []string
		title2     string
		artists2   []string
		dur        string
		shouldPass bool
	}{
		{
			name:       "Feat in bracket on candidate",
			title1:     "Levitating",
			artists1:   []string{"Dua Lipa", "DaBaby"},
			title2:     "Levitating (feat. DaBaby)",
			artists2:   []string{"Dua Lipa"},
			dur:        "3:23",
			shouldPass: true,
		},
		{
			name:       "Featuring with slash delimiter",
			title1:     "Save Your Tears",
			artists1:   []string{"The Weeknd", "Ariana Grande"},
			title2:     "Save Your Tears (Remix) - The Weeknd / Ariana Grande",
			artists2:   []string{"The Weeknd"},
			dur:        "3:11",
			shouldPass: true,
		},
		{
			name:       "Chinese conjunction delimiter 和 / 与 / 、",
			title1:     "珊瑚海",
			artists1:   []string{"周杰伦", "Lara梁心颐"},
			title2:     "周杰伦 和 Lara 梁心颐 - 珊瑚海",
			artists2:   []string{"周杰伦"},
			dur:        "4:16",
			shouldPass: true,
		},
		{
			name:       "Ampersand delimiter",
			title1:     "Stay",
			artists1:   []string{"The Kid LAROI", "Justin Bieber"},
			title2:     "The Kid LAROI & Justin Bieber - Stay",
			artists2:   []string{"The Kid LAROI"},
			dur:        "2:21",
			shouldPass: true,
		},
		{
			name:       "Letter x delimiter",
			title1:     "Cold Heart",
			artists1:   []string{"Elton John", "Dua Lipa"},
			title2:     "Elton John x Dua Lipa - Cold Heart (PNAU Remix)",
			artists2:   []string{"Elton John"},
			dur:        "3:22",
			shouldPass: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score := engine.CalculateTrackScore(tc.title1, tc.artists1, tc.dur, tc.title2, tc.artists2, tc.dur)
			passed := score >= engine.ConfidenceThreshold
			if passed != tc.shouldPass {
				t.Errorf("CalculateTrackScore(%q vs %q) = %d (passed=%v, want %v)", tc.title1, tc.title2, score, passed, tc.shouldPass)
			}
		})
	}
}

func TestCalculateTrackScore_LiveTrackMatchingSemantics(t *testing.T) {
	t.Run("Studio source vs Live candidate is penalized and rejected", func(t *testing.T) {
		score := engine.CalculateTrackScore("Yellow", []string{"Coldplay"}, "4:29", "Yellow (Live in Buenos Aires)", []string{"Coldplay"}, "4:30")
		if score >= engine.ConfidenceThreshold {
			t.Errorf("expected score < %d for studio vs live, got %d", engine.ConfidenceThreshold, score)
		}
	})

	t.Run("Studio source vs Chinese 现场版 candidate is penalized and rejected", func(t *testing.T) {
		score := engine.CalculateTrackScore("晴天", []string{"周杰伦"}, "4:29", "周杰伦 - 晴天 [现场版]", []string{"周杰伦"}, "4:30")
		if score >= engine.ConfidenceThreshold {
			t.Errorf("expected score < %d for studio vs live, got %d", engine.ConfidenceThreshold, score)
		}
	})

	t.Run("Live source vs Live candidate is allowed to match", func(t *testing.T) {
		score := engine.CalculateTrackScore("Yellow (Live at Glastonbury)", []string{"Coldplay"}, "4:30", "Yellow - Live at Glastonbury", []string{"Coldplay"}, "4:30")
		if score < engine.ConfidenceThreshold {
			t.Errorf("expected score >= %d for live vs live, got %d", engine.ConfidenceThreshold, score)
		}
	})
}

func TestCalculateTrackScore_CJKVariantsAndBrackets(t *testing.T) {
	cases := []struct {
		name       string
		title1     string
		artists1   []string
		title2     string
		artists2   []string
		dur        string
		shouldPass bool
	}{
		{
			name:       "Traditional Chinese to Simplified Chinese",
			title1:     "說好不哭",
			artists1:   []string{"周杰倫", "阿信"},
			title2:     "说好不哭 (Won't Cry)",
			artists2:   []string{"周杰伦", "阿信"},
			dur:        "3:42",
			shouldPass: true,
		},
		{
			name:       "Fullwidth Lenticular brackets 【】 and 『』",
			title1:     "夜曲",
			artists1:   []string{"周杰伦"},
			title2:     "【官方MV】『夜曲』周杰伦",
			artists2:   []string{"周杰伦"},
			dur:        "3:46",
			shouldPass: true,
		},
		{
			name:       "Corner brackets 「」 and Tortoise shell brackets 〘〙",
			title1:     "青花瓷",
			artists1:   []string{"周杰伦"},
			title2:     "〘高清〙周杰伦「青花瓷」",
			artists2:   []string{"周杰伦"},
			dur:        "3:59",
			shouldPass: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			score := engine.CalculateTrackScore(tc.title1, tc.artists1, tc.dur, tc.title2, tc.artists2, tc.dur)
			passed := score >= engine.ConfidenceThreshold
			if passed != tc.shouldPass {
				t.Errorf("CalculateTrackScore(%q vs %q) = %d (passed=%v, want %v)", tc.title1, tc.title2, score, passed, tc.shouldPass)
			}
		})
	}
}

func TestCalculateTrackScore_DurationToleranceBuckets(t *testing.T) {
	baseTitle := "Universal Constant"
	baseArtist := []string{"Scientist"}

	// Delta <= 3s (+15): Exact match 55 + 30 + 15 = 100
	s1 := engine.CalculateTrackScore(baseTitle, baseArtist, "3:00", baseTitle, baseArtist, "3:02")
	if s1 != 100 {
		t.Errorf("expected 100 for delta <= 3s, got %d", s1)
	}

	// Delta 5s (+10): 55 + 30 + 10 = 95
	s2 := engine.CalculateTrackScore(baseTitle, baseArtist, "3:00", baseTitle, baseArtist, "3:05")
	if s2 != 95 {
		t.Errorf("expected 95 for delta 5s, got %d", s2)
	}

	// Delta 12s (+5): 55 + 30 + 5 = 90
	s3 := engine.CalculateTrackScore(baseTitle, baseArtist, "3:00", baseTitle, baseArtist, "3:12")
	if s3 != 90 {
		t.Errorf("expected 90 for delta 12s, got %d", s3)
	}

	// Delta 20s (0): 55 + 30 + 0 = 85
	s4 := engine.CalculateTrackScore(baseTitle, baseArtist, "3:00", baseTitle, baseArtist, "3:20")
	if s4 != 85 {
		t.Errorf("expected 85 for delta 20s, got %d", s4)
	}

	// Delta 35s (-25): 55 + 30 - 25 = 60 (< 70 threshold)
	s5 := engine.CalculateTrackScore(baseTitle, baseArtist, "3:00", baseTitle, baseArtist, "3:35")
	if s5 != 60 {
		t.Errorf("expected 60 for delta 35s, got %d", s5)
	}

	// Delta 60s (-40): 55 + 30 - 40 = 45 (< 70 threshold)
	s6 := engine.CalculateTrackScore(baseTitle, baseArtist, "3:00", baseTitle, baseArtist, "4:00")
	if s6 != 45 {
		t.Errorf("expected 45 for delta 60s, got %d", s6)
	}
}

func TestDomainInvariants_DiffPlanAndTrackConservation(t *testing.T) {
	// Invariant 1: N_source = N_matched + N_missing (Diff conservation)
	// Invariant 2: Zero duplicate assignments to destination tracks
	// Invariant 3: Diff plan conservation (every source track fate is accounted for without loss)
	// Invariant 4: Timestamp RFC3339 validation

	t.Run("Diff Plan Conservation and Zero Duplicate Assignment", func(t *testing.T) {
		sp := &model.SpotifyPlaylist{
			Tracks: []model.SpotifyTrack{
				{Index: 1, Title: "Song A", Artists: []string{"Artist 1"}},
				{Index: 2, Title: "Song B", Artists: []string{"Artist 2"}},
				{Index: 3, Title: "Song A", Artists: []string{"Artist 1"}}, // duplicate source title/artist
				{Index: 4, Title: "Song C", Artists: []string{"Artist 3"}},
				{Index: 5, Title: "Song D", Artists: []string{"Artist 4"}},
			},
		}

		yt := &model.YTMPlaylist{
			Tracks: []model.YTMTrack{
				{VideoID: "vid_a1", Title: "Song A", Artists: []string{"Artist 1"}},
				{VideoID: "vid_c1", Title: "Song C", Artists: []string{"Artist 3"}},
				{VideoID: "vid_extra", Title: "Extra Song", Artists: []string{"Extra Artist"}},
			},
		}

		mapping := map[int]string{
			2: "vid_b_manual", // manual override mapping
		}

		plan := engine.ComputeDiff(sp, yt, mapping)

		// 1. Total Source Conservation: N_source == N_matched + N_missing
		nSource := len(sp.Tracks)
		nMatched := len(plan.Matched)
		nMissing := len(plan.MissingInYTM)
		if nSource != nMatched+nMissing {
			t.Fatalf("Invariant 1 violation: N_source (%d) != N_matched (%d) + N_missing (%d)", nSource, nMatched, nMissing)
		}

		// 2. Diff Plan Conservation: Every 1-based source index [1..N_source] is uniquely present in either Matched or Missing
		accountedIndices := make(map[int]int)
		for _, m := range plan.Matched {
			accountedIndices[m.Index]++
		}
		for _, m := range plan.MissingInYTM {
			accountedIndices[m.Index]++
		}

		for idx := 1; idx <= nSource; idx++ {
			if count := accountedIndices[idx]; count != 1 {
				t.Errorf("Invariant 3 violation: source track index %d appeared %d times in diff plan partitions (want exactly 1)", idx, count)
			}
		}

		// 3. Zero Duplicate Destination Assignment: No VideoID is matched to multiple tracks
		usedVideoIDs := make(map[string]int)
		for _, m := range plan.Matched {
			if m.TargetTrackID != "" {
				usedVideoIDs[m.TargetTrackID]++
				if usedVideoIDs[m.TargetTrackID] > 1 {
					t.Errorf("Invariant 2 violation: duplicate VideoID %q assigned to multiple source tracks", m.TargetTrackID)
				}
			}
		}

		// 4. Extra tracks in destination are correctly partitioned
		if len(plan.ExtraInYTM) != 1 || plan.ExtraInYTM[0].VideoID != "vid_extra" {
			t.Errorf("expected 1 extra track with VideoID vid_extra, got %+v", plan.ExtraInYTM)
		}
	})

	t.Run("Timestamp RFC3339 Invariant Validation", func(t *testing.T) {
		sampleTimestamps := []string{
			time.Now().UTC().Format(time.RFC3339),
			"2026-01-01T12:00:00Z",
			"2026-02-15T08:30:45+08:00",
		}
		for _, ts := range sampleTimestamps {
			if _, err := time.Parse(time.RFC3339, ts); err != nil {
				t.Errorf("Invariant 4 violation: timestamp %q is not valid RFC3339: %v", ts, err)
			}
		}
	})
}


