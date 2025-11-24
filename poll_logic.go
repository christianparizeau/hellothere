package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/bwmarrin/discordgo"
)

// CreatePoll creates a new poll and returns it
func CreatePoll(guildID, channelID, creatorID string, i *discordgo.Interaction, hours int) *Poll {
	now := time.Now()
	pollID := fmt.Sprintf("%s-%d", guildID, now.Unix())

	return &Poll{
		ID:          pollID,
		GuildID:     guildID,
		ChannelID:   channelID,
		CreatorID:   creatorID,
		Phase:       PhaseSubmission,
		Submissions: []Submission{},
		Votes:       []Vote{},
		EndTime:     now.Add(time.Duration(hours) * time.Hour),
		CreatedAt:   now,
		Interaction: i,
	}
}

// FinalizeVote adds or updates a user's vote
func (p *Poll) FinalizeVote(userID string) error {
	if p.Phase != PhaseVoting {
		return fmt.Errorf("poll is not in voting phase")
	}
	for i, v := range p.Votes {
		if v.UserID == userID {
			// Validate rankings
			if len(v.Rankings) != len(p.Submissions) {
				return fmt.Errorf("must rank all %d submissions", len(p.Submissions))
			}

			// Check for valid indices and no duplicates
			seen := make(map[int]bool)
			for _, rank := range v.Rankings {
				if rank < 0 || rank >= len(p.Submissions) {
					return fmt.Errorf("invalid ranking index: %d", rank)
				}
				if seen[rank] {
					return fmt.Errorf("duplicate ranking detected")
				}
				seen[rank] = true
			}
			v.VotedAt = time.Now()
			p.Votes[i] = v
		}
	}
	return nil
}

// CalculateResults uses Instant Runoff Voting to determine the ranked results
func (p *Poll) CalculateResults() []int {
	if len(p.Submissions) == 0 {
		return []int{}
	}

	var results []int
	eliminated := make(map[int]bool)

	for len(results) < len(p.Submissions) {
		// Count first-choice votes for non-eliminated candidates
		counts := make(map[int]int)
		for _, vote := range p.Votes {
			for _, candidateIdx := range vote.Rankings {
				if !eliminated[candidateIdx] {
					counts[candidateIdx]++
					break
				}
			}
		}

		// If no votes, add all remaining candidates in order
		if len(counts) == 0 {
			for i := range p.Submissions {
				if !eliminated[i] {
					results = append(results, i)
				}
			}
			break
		}

		// Find max and min votes
		maxVotes, minVotes := 0, len(p.Votes)+1
		for _, count := range counts {
			if count > maxVotes {
				maxVotes = count
			}
			if count < minVotes {
				minVotes = count
			}
		}

		// Collect winners (candidates with max votes)
		var winners []int
		for idx, count := range counts {
			if count == maxVotes {
				winners = append(winners, idx)
			}
		}
		sort.Ints(winners) // Consistent tie-breaking

		// Add winners to results and mark as eliminated
		for _, winner := range winners {
			results = append(results, winner)
			eliminated[winner] = true
		}

		// Eliminate candidate with fewest votes (if not all are winners)
		if len(results) < len(p.Submissions) {
			for idx, count := range counts {
				if count == minVotes && !eliminated[idx] {
					eliminated[idx] = true
					break
				}
			}
		}
	}

	return results
}

func (p *Poll) UpsertVote(userID string, rank int, selection int) {
	for i, vote := range p.Votes {
		if vote.UserID == userID {
			vote.Rankings[rank] = selection
			p.Votes[i] = vote
			return
		}
	}
	vote := Vote{
		UserID:   userID,
		Rankings: make([]int, len(p.Submissions)),
	}
	for i := range vote.Rankings {
		vote.Rankings[i] = -1
	}
	vote.Rankings[rank] = selection
	p.Votes = append(p.Votes, vote)
}
