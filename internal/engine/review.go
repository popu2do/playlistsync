package engine

import (
	"fmt"
	"playlistsync/internal/model"
)

// ReviewOption represents a concrete candidate target presented to the user
type ReviewOption struct {
	TargetID  string
	TargetURI string
	Title     string
	Artists   []string
	Duration  string
	Score     int
	TargetURL string
}

// ReviewItem represents an unresolved source track and its evaluated candidate options
type ReviewItem struct {
	SourceIndex    int
	SourceTitle    string
	SourceArtists  []string
	SourceDuration string
	SourceURL      string
	SourcePlatform string
	TargetPlatform string
	Options        []ReviewOption
}

// ReviewPromptFunc defines the pure functional seam for human-in-the-loop review
type ReviewPromptFunc func(item ReviewItem) (selectedID string, confirmed bool, abortRemaining bool)

// ReviewResult aggregates the decisions made across the review process
type ReviewResult struct {
	AcceptedIDs       map[int]string
	ReviewedAdditions []model.AddedTrack
	SkippedTracks     []model.SkippedTrack
}

// ExecuteReview processes ambiguous items through the review prompt sequentially
func ExecuteReview(items []ReviewItem, prompt ReviewPromptFunc, autoYes bool) ReviewResult {
	res := ReviewResult{
		AcceptedIDs: make(map[int]string),
	}

	if len(items) == 0 {
		return res
	}

	if prompt == nil || autoYes {
		for _, item := range items {
			topInfo := ""
			if len(item.Options) > 0 {
				topInfo = fmt.Sprintf(" (Top candidate: %s, Score: %d)", item.Options[0].Title, item.Options[0].Score)
			}
			fmt.Printf(" - Low confidence candidate, skipping: %s%s\n", item.SourceTitle, topInfo)
			res.SkippedTracks = append(res.SkippedTracks, model.SkippedTrack{
				Index:   item.SourceIndex,
				Title:   item.SourceTitle,
				Artists: item.SourceArtists,
				Reason:  "low confidence candidate",
			})
		}
		return res
	}

	fmt.Printf("\nFound %d track(s) needing confirmation. Entering interactive review:\n", len(items))
	abortAll := false

	for _, item := range items {
		if abortAll {
			res.SkippedTracks = append(res.SkippedTracks, model.SkippedTrack{
				Index:   item.SourceIndex,
				Title:   item.SourceTitle,
				Artists: item.SourceArtists,
				Reason:  "skipped by user (review aborted)",
			})
			continue
		}

		chosenID, confirmed, stop := prompt(item)
		if stop {
			abortAll = true
			res.SkippedTracks = append(res.SkippedTracks, model.SkippedTrack{
				Index:   item.SourceIndex,
				Title:   item.SourceTitle,
				Artists: item.SourceArtists,
				Reason:  "skipped by user (review aborted)",
			})
			continue
		}

		if confirmed && chosenID != "" {
			res.AcceptedIDs[item.SourceIndex] = chosenID

			destTitle := ""
			for _, opt := range item.Options {
				if opt.TargetID == chosenID {
					destTitle = opt.Title
					break
				}
			}

			res.ReviewedAdditions = append(res.ReviewedAdditions, model.AddedTrack{
				Index:            item.SourceIndex,
				Title:            item.SourceTitle,
				Artists:          item.SourceArtists,
				TargetTrackID:    chosenID,
				DestinationTitle: destTitle,
			})
			fmt.Printf(" -> Accepted [%s] as match for %s\n", chosenID, item.SourceTitle)
		} else {
			fmt.Printf(" - Skipped by user: %s\n", item.SourceTitle)
			res.SkippedTracks = append(res.SkippedTracks, model.SkippedTrack{
				Index:   item.SourceIndex,
				Title:   item.SourceTitle,
				Artists: item.SourceArtists,
				Reason:  "skipped by user in review",
			})
		}
	}

	return res
}
