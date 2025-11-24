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

// CalculateResults uses Instant Runoff Voting to determine the ranked results.
// Returns a slice of candidate indices ordered from winner (first) to loser (last).
func (p *Poll) CalculateResults() []int {
	numCandidates := len(p.Submissions)
	if numCandidates == 0 {
		return []int{}
	}

	// If no votes, return candidates in natural order
	if len(p.Votes) == 0 {
		results := make([]int, numCandidates)
		for i := range results {
			results[i] = i
		}
		return results
	}

	eliminated := make(map[int]bool)
	var eliminationOrder []int

	// Eliminate candidates one by one using IRV
	// Each round, eliminate the candidate with the fewest first-choice votes
	for len(eliminated) < numCandidates-1 {
		// Count first-choice votes among remaining candidates
		counts := make(map[int]int)
		for _, vote := range p.Votes {
			// Find this voter's highest-ranked non-eliminated candidate
			for _, candidateIdx := range vote.Rankings {
				if candidateIdx >= 0 && candidateIdx < numCandidates && !eliminated[candidateIdx] {
					counts[candidateIdx]++
					break
				}
			}
		}

		// Find minimum vote count among remaining candidates
		minVotes := len(p.Votes) + 1
		for candidateIdx := 0; candidateIdx < numCandidates; candidateIdx++ {
			if !eliminated[candidateIdx] {
				if counts[candidateIdx] < minVotes {
					minVotes = counts[candidateIdx]
				}
			}
		}

		// Collect all candidates tied for minimum votes
		var tiedCandidates []int
		for candidateIdx := 0; candidateIdx < numCandidates; candidateIdx++ {
			if !eliminated[candidateIdx] && counts[candidateIdx] == minVotes {
				tiedCandidates = append(tiedCandidates, candidateIdx)
			}
		}
		sort.Ints(tiedCandidates)

		// Eliminate first candidate (deterministic tie-breaking by index)
		toEliminate := tiedCandidates[0]
		eliminated[toEliminate] = true
		eliminationOrder = append(eliminationOrder, toEliminate)
	}

	// Add the winner (last remaining candidate)
	for i := 0; i < numCandidates; i++ {
		if !eliminated[i] {
			eliminationOrder = append(eliminationOrder, i)
			break
		}
	}

	// Reverse elimination order to get ranking (winner first, last eliminated last)
	results := make([]int, len(eliminationOrder))
	for i := range results {
		results[i] = eliminationOrder[len(eliminationOrder)-1-i]
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
