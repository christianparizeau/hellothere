package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// PollPhase represents the current phase of a poll
type PollPhase int

const (
	PhaseSubmission PollPhase = iota
	PhaseVoting
	PhaseCompleted
)

const (
	MaxSubmissions = 20
)

func (p PollPhase) String() string {
	switch p {
	case PhaseSubmission:
		return "Submission"
	case PhaseVoting:
		return "Voting"
	case PhaseCompleted:
		return "Completed"
	default:
		return "Unknown"
	}
}

// Submission represents a game candidate submitted by a user
type Submission struct {
	UserID      string    `json:"user_id"`
	Username    string    `json:"username"`
	GameName    string    `json:"game_name"`
	Description string    `json:"description"`
	Link        string    `json:"link,omitempty"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// Vote represents a user's ranked choices
type Vote struct {
	UserID   string    `json:"user_id"`
	Rankings []int     `json:"rankings"` // indices into Poll.Submissions, ordered by preference
	VotedAt  time.Time `json:"voted_at"`
}

// Poll represents a single VGC poll
type Poll struct {
	mut         sync.Mutex
	ID          string       `json:"id"`
	GuildID     string       `json:"guild_id"`
	ChannelID   string       `json:"channel_id"`
	CreatorID   string       `json:"creator_id"`
	Phase       PollPhase    `json:"phase"`
	Submissions []Submission `json:"submissions"`
	Votes       []Vote       `json:"votes"`
	EndTime     time.Time    `json:"submission_end_time"`
	CreatedAt   time.Time    `json:"created_at"`
	Interaction *discordgo.Interaction
	MessageID   string
}

// PollState manages all active polls
type PollState struct {
	polls map[string]*Poll // pollID -> Poll
	mut   sync.RWMutex
}

// NewPollState creates a new poll state manager
func NewPollState() *PollState {
	return &PollState{
		polls: make(map[string]*Poll),
	}
}

// AddPoll adds a new poll to the state
func (ps *PollState) AddPoll(poll *Poll) {
	ps.mut.Lock()
	defer ps.mut.Unlock()
	ps.polls[poll.ID] = poll
}

// GetPoll retrieves a poll by ID
func (ps *PollState) GetPoll(pollID string) (*Poll, bool) {
	ps.mut.RLock()
	defer ps.mut.RUnlock()
	poll, ok := ps.polls[pollID]
	return poll, ok
}

// SaveToFile saves the poll state to a JSON file
func (ps *PollState) SaveToFile(filename string) error {
	ps.mut.RLock()
	defer ps.mut.RUnlock()

	for k, poll := range ps.polls {
		//purge old polls on save
		if poll.EndTime.Before(time.Now().Add(time.Hour * 24 * -7)) {
			delete(ps.polls, k)
		}
	}

	data, err := json.MarshalIndent(ps.polls, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal polls: %w", err)
	}

	err = os.WriteFile(filename, data, 0644)
	if err != nil {
		return fmt.Errorf("failed to write polls file: %w", err)
	}

	slog.Info("saved poll state", "filename", filename, "poll_count", len(ps.polls))
	return nil
}

// LoadFromFile loads poll state from a JSON file
func (ps *PollState) LoadFromFile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("no existing polls file found", "filename", filename)
			return nil
		}
		return fmt.Errorf("failed to read polls file: %w", err)
	}

	var polls map[string]*Poll
	err = json.Unmarshal(data, &polls)
	if err != nil {
		return fmt.Errorf("failed to unmarshal polls: %w", err)
	}

	ps.mut.Lock()
	defer ps.mut.Unlock()
	ps.polls = polls

	slog.Info("loaded poll state", "filename", filename, "poll_count", len(ps.polls))
	return nil
}

type kind string

var (
	SubmitModal  = kind("submit-modal")
	VoteSelect   = kind("vote-select")
	VoteSubmit   = kind("vote-submit")
	VoteReset    = kind("vote-reset")
	LockButton   = kind("lock")
	EndButton    = kind("end")
	VoteButton   = kind("vote")
	SubmitButton = kind("submit")
)

type formID struct {
	Kind   kind
	PollID string
	Rank   int
}

func (f formID) String() string {
	return fmt.Sprintf("%s_%s_%d", f.Kind, f.PollID, f.Rank)
}

func parseForm(s string) (f formID) {
	split := strings.Split(s, "_")
	f.Kind = kind(split[0])
	f.PollID = split[1]
	if len(split) == 3 {
		f.Rank, _ = strconv.Atoi(split[2])
	}
	return f
}
