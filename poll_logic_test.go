package main

import (
	"reflect"
	"testing"
	"time"
)

// Helper function to create a simple submission
func makeSubmission(userID, gameName string) Submission {
	return Submission{
		UserID:      userID,
		Username:    userID,
		GameName:    gameName,
		Description: "",
		SubmittedAt: time.Now(),
	}
}

// Helper function to create a vote with rankings
func makeVote(userID string, rankings []int) Vote {
	return Vote{
		UserID:   userID,
		Rankings: rankings,
		VotedAt:  time.Now(),
	}
}

func TestCalculateResults(t *testing.T) {
	tests := []struct {
		name        string
		submissions []Submission
		votes       []Vote
		expected    []int
		description string
	}{
		// Empty and Single Cases
		{
			name:        "Empty poll (no submissions)",
			submissions: []Submission{},
			votes:       []Vote{},
			expected:    []int{},
			description: "Poll with no submissions should return empty slice",
		},
		{
			name:        "Single submission, no votes",
			submissions: []Submission{makeSubmission("user1", "GameA")},
			votes:       []Vote{},
			expected:    []int{0},
			description: "Single submission with no votes returns that submission",
		},
		{
			name:        "Single submission, one vote",
			submissions: []Submission{makeSubmission("user1", "GameA")},
			votes:       []Vote{makeVote("voter1", []int{0})},
			expected:    []int{0},
			description: "Single submission with vote returns that submission",
		},
		{
			name: "Two submissions, no votes",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
			},
			votes:       []Vote{},
			expected:    []int{0, 1},
			description: "Multiple submissions with no votes returns natural order",
		},
		{
			name: "Three submissions, no votes",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes:       []Vote{},
			expected:    []int{0, 1, 2},
			description: "Multiple submissions with no votes returns natural order",
		},

		// Clear Winners (First Round)
		{
			name: "Unanimous winner (2 candidates)",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1}),
				makeVote("voter2", []int{0, 1}),
				makeVote("voter3", []int{0, 1}),
			},
			expected:    []int{0, 1},
			description: "A gets 3 votes, B gets 0. B eliminated first, A wins. Result: [A, B]",
		},
		{
			name: "Clear majority (3 candidates)",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2}),
				makeVote("voter2", []int{0, 1, 2}),
				makeVote("voter3", []int{0, 2, 1}),
				makeVote("voter4", []int{1, 2, 0}),
			},
			expected:    []int{0, 1, 2},
			description: "Round 1: A=3, B=1, C=0. C eliminated. Round 2: A=3, B=1. B eliminated. A wins. Result: [A, B, C]",
		},

		// IRV with Elimination Rounds
		{
			name: "Basic IRV elimination",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2}), // A > B > C
				makeVote("voter2", []int{0, 1, 2}), // A > B > C
				makeVote("voter3", []int{1, 0, 2}), // B > A > C
				makeVote("voter4", []int{1, 0, 2}), // B > A > C
				makeVote("voter5", []int{2, 0, 1}), // C > A > B
			},
			expected:    []int{0, 1, 2},
			description: "Round 1: A=2, B=2, C=1. C eliminated. Round 2: A=3 (gets C's vote), B=2. B eliminated. A wins. Result: [A, B, C]",
		},
		{
			name: "IRV with vote transfer changing outcome",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 2, 1}), // A > C > B
				makeVote("voter2", []int{0, 2, 1}), // A > C > B
				makeVote("voter3", []int{1, 2, 0}), // B > C > A
				makeVote("voter4", []int{1, 2, 0}), // B > C > A
				makeVote("voter5", []int{2, 0, 1}), // C > A > B
				makeVote("voter6", []int{2, 0, 1}), // C > A > B
				makeVote("voter7", []int{2, 0, 1}), // C > A > B
			},
			expected:    []int{2, 1, 0},
			description: "Round 1: A=2, B=2, C=3. A eliminated (index 0 < 1). Round 2: All votes go to C, B eliminated. C wins. Result: [C, B, A]",
		},
		{
			name: "Four candidates with multiple rounds",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
				makeSubmission("user4", "GameD"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2, 3}),
				makeVote("voter2", []int{0, 1, 2, 3}),
				makeVote("voter3", []int{1, 0, 2, 3}),
				makeVote("voter4", []int{2, 0, 1, 3}),
				makeVote("voter5", []int{3, 2, 1, 0}),
			},
			expected:    []int{0, 3, 2, 1},
			description: "Round 1: A=2, B=1, C=1, D=1. B eliminated. Round 2: A=3, C=1, D=1. C eliminated. Round 3: A=3, D=2. D eliminated. A wins.",
		},

		// Perfect Ties (Deterministic Tie-Breaking)
		{
			name: "Perfect two-way tie",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1}),
				makeVote("voter2", []int{1, 0}),
			},
			expected:    []int{1, 0},
			description: "A=1, B=1 tied. A eliminated (index 0 < 1). B wins. Result: [B, A]",
		},
		{
			name: "Perfect three-way tie",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2}),
				makeVote("voter2", []int{1, 2, 0}),
				makeVote("voter3", []int{2, 0, 1}),
			},
			expected:    []int{1, 2, 0},
			description: "Round 1: A=1, B=1, C=1 all tied. A eliminated (lowest index). Round 2: B=2, C=1. C eliminated. B wins. Result: [B, C, A]",
		},
		{
			name: "Four-way tie",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
				makeSubmission("user4", "GameD"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2, 3}),
				makeVote("voter2", []int{1, 2, 3, 0}),
				makeVote("voter3", []int{2, 3, 0, 1}),
				makeVote("voter4", []int{3, 0, 1, 2}),
			},
			expected:    []int{3, 1, 2, 0},
			description: "All tied at 1. Eliminate A, then C, then B. D wins. Result: [D, B, C, A]",
		},

		// Vote Redistribution
		{
			name: "Vote transfers favor different candidate",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2}),
				makeVote("voter2", []int{1, 0, 2}),
				makeVote("voter3", []int{2, 1, 0}),
				makeVote("voter4", []int{2, 1, 0}),
			},
			expected:    []int{2, 1, 0},
			description: "Round 1: A=1, B=1, C=2. A eliminated. Round 2: B=2, C=2. B eliminated (index). C wins. Result: [C, B, A]",
		},
		{
			name: "Cascade of eliminations",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
				makeSubmission("user4", "GameD"),
				makeSubmission("user5", "GameE"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2, 3, 4}),
				makeVote("voter2", []int{1, 2, 3, 4, 0}),
				makeVote("voter3", []int{2, 3, 4, 0, 1}),
				makeVote("voter4", []int{3, 4, 0, 1, 2}),
				makeVote("voter5", []int{4, 0, 1, 2, 3}),
			},
			expected:    []int{1, 3, 4, 2, 0},
			description: "All tied at 1. A eliminated, then C, then E, then D. B wins. Result: [B, D, E, C, A]",
		},

		// Realistic Scenarios
		{
			name: "Condorcet winner exists and wins IRV",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1, 2}),
				makeVote("voter2", []int{0, 1, 2}),
				makeVote("voter3", []int{1, 0, 2}),
				makeVote("voter4", []int{2, 0, 1}),
			},
			expected:    []int{0, 2, 1},
			description: "A would beat both B and C in head-to-head. Round 1: A=2, B=1, C=1. B eliminated. Round 2: A=3, C=1. C eliminated. A wins.",
		},
		{
			name: "Polarized election",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1}),
				makeVote("voter2", []int{0, 1}),
				makeVote("voter3", []int{0, 1}),
				makeVote("voter4", []int{1, 0}),
				makeVote("voter5", []int{1, 0}),
				makeVote("voter6", []int{1, 0}),
				makeVote("voter7", []int{1, 0}),
			},
			expected:    []int{1, 0},
			description: "B wins 4-3. A eliminated. Result: [B, A]",
		},
		{
			name: "Strong second-choice consensus",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
				makeSubmission("user3", "GameC"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 2, 1}),
				makeVote("voter2", []int{0, 2, 1}),
				makeVote("voter3", []int{1, 2, 0}),
				makeVote("voter4", []int{1, 2, 0}),
				makeVote("voter5", []int{2, 0, 1}),
			},
			expected:    []int{0, 1, 2},
			description: "Round 1: A=2, B=2, C=1. C eliminated. Round 2: A=3 (gets C), B=2. B eliminated. A wins. Result: [A, B, C]",
		},

		// Edge Cases
		{
			name: "Vote with invalid index (-1)",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
			},
			votes: []Vote{
				makeVote("voter1", []int{0, 1}),
				makeVote("voter2", []int{-1, 0}), // Invalid first choice
			},
			expected:    []int{0, 1},
			description: "Voter2's first choice is invalid, so their vote goes to next valid (0). A=2, B=0. B eliminated, A wins.",
		},
		{
			name: "All votes have only invalid indices",
			submissions: []Submission{
				makeSubmission("user1", "GameA"),
				makeSubmission("user2", "GameB"),
			},
			votes: []Vote{
				makeVote("voter1", []int{-1, -1}),
				makeVote("voter2", []int{-1, -1}),
			},
			expected:    []int{1, 0},
			description: "No valid votes count. A=0, B=0. A eliminated (index 0 < 1), then B wins.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			poll := &Poll{
				Submissions: tt.submissions,
				Votes:       tt.votes,
			}

			result := poll.CalculateResults()

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("\nCalculateResults() = %v\nwant              = %v\nDescription: %s",
					result, tt.expected, tt.description)
			}
		})
	}
}

// TestCalculateResultsDeterminism ensures the same input always produces the same output
func TestCalculateResultsDeterminism(t *testing.T) {
	poll := &Poll{
		Submissions: []Submission{
			makeSubmission("user1", "GameA"),
			makeSubmission("user2", "GameB"),
			makeSubmission("user3", "GameC"),
		},
		Votes: []Vote{
			makeVote("voter1", []int{0, 1, 2}),
			makeVote("voter2", []int{1, 2, 0}),
			makeVote("voter3", []int{2, 0, 1}),
		},
	}

	// Run the calculation multiple times
	results := make([][]int, 100)
	for i := 0; i < 100; i++ {
		results[i] = poll.CalculateResults()
	}

	// All results should be identical
	expected := results[0]
	for i := 1; i < 100; i++ {
		if !reflect.DeepEqual(results[i], expected) {
			t.Errorf("Run %d produced different result: %v, expected %v", i, results[i], expected)
		}
	}

	t.Logf("Verified deterministic result over 100 runs: %v", expected)
}

// TestCalculateResultsComplexScenario tests a larger, more realistic poll
func TestCalculateResultsComplexScenario(t *testing.T) {
	poll := &Poll{
		Submissions: []Submission{
			makeSubmission("user1", "Elden Ring"),
			makeSubmission("user2", "Baldur's Gate 3"),
			makeSubmission("user3", "Zelda: TOTK"),
			makeSubmission("user4", "Hogwarts Legacy"),
			makeSubmission("user5", "Starfield"),
		},
		Votes: []Vote{
			makeVote("voter1", []int{0, 2, 1, 3, 4}),  // Elden Ring
			makeVote("voter2", []int{0, 1, 2, 3, 4}),  // Elden Ring
			makeVote("voter3", []int{1, 2, 0, 3, 4}),  // BG3
			makeVote("voter4", []int{1, 2, 0, 3, 4}),  // BG3
			makeVote("voter5", []int{1, 0, 2, 3, 4}),  // BG3
			makeVote("voter6", []int{2, 1, 0, 3, 4}),  // Zelda
			makeVote("voter7", []int{2, 1, 0, 3, 4}),  // Zelda
			makeVote("voter8", []int{3, 1, 2, 0, 4}),  // Hogwarts
			makeVote("voter9", []int{4, 3, 2, 1, 0}),  // Starfield
		},
	}

	result := poll.CalculateResults()

	// Round 1: ER=2, BG3=3, Zelda=2, Hogwarts=1, Starfield=1
	// Eliminate Hogwarts (index 3 < 4)
	// Round 2: ER=2, BG3=4 (gets Hogwarts), Zelda=2, Starfield=1
	// Eliminate Starfield
	// Round 3: ER=2, BG3=4, Zelda=3 (gets Starfield)
	// Eliminate ER (tied with Zelda, index 0 < 2)
	// Round 4: BG3=6, Zelda=3
	// Eliminate Zelda
	// BG3 wins
	expected := []int{1, 2, 0, 4, 3} // BG3, Zelda, Elden Ring, Starfield, Hogwarts

	if !reflect.DeepEqual(result, expected) {
		t.Errorf("\nComplex scenario failed:\nGot:  %v\nWant: %v", result, expected)

		// Print helpful debugging info
		gameNames := []string{"Elden Ring", "BG3", "Zelda", "Hogwarts", "Starfield"}
		t.Logf("\nResult order:")
		for i, idx := range result {
			t.Logf("  %d. %s (index %d)", i+1, gameNames[idx], idx)
		}
	}
}
