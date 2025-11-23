package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
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

// RemovePoll removes a poll from active state
func (ps *PollState) RemovePoll(pollID string) {
	ps.mut.Lock()
	defer ps.mut.Unlock()
	if _, ok := ps.polls[pollID]; ok {
		delete(ps.polls, pollID)
	}
}

// GetAllPolls returns a copy of all polls
func (ps *PollState) GetAllPolls() []*Poll {
	ps.mut.RLock()
	defer ps.mut.RUnlock()
	polls := make([]*Poll, 0, len(ps.polls))
	for _, poll := range ps.polls {
		polls = append(polls, poll)
	}
	return polls
}

// SaveToFile saves the poll state to a JSON file
func (ps *PollState) SaveToFile(filename string) error {
	ps.mut.RLock()
	defer ps.mut.RUnlock()

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

	// Keep running IRV until we have a full ranking
	for len(results) < len(p.Submissions) {
		// Count first-choice votes for non-eliminated candidates
		counts := make(map[int]int)
		for _, vote := range p.Votes {
			// Find the highest-ranked non-eliminated candidate
			for _, candidateIdx := range vote.Rankings {
				if !eliminated[candidateIdx] {
					counts[candidateIdx]++
					break
				}
			}
		}

		// If no votes or only one candidate left, add remaining in arbitrary order
		if len(counts) == 0 {
			for i := range p.Submissions {
				if !eliminated[i] {
					results = append(results, i)
				}
			}
			break
		}

		// Find candidate(s) with most votes
		maxVotes := 0
		for _, count := range counts {
			if count > maxVotes {
				maxVotes = count
			}
		}

		// Find all candidates with max votes (for tie handling)
		var winners []int
		for idx, count := range counts {
			if count == maxVotes {
				winners = append(winners, idx)
			}
		}

		// Sort winners by submission order for consistent tie-breaking
		sort.Ints(winners)

		// Add the winner(s) to results and eliminate them
		for _, winner := range winners {
			results = append(results, winner)
			eliminated[winner] = true
		}

		// If all candidates are now placed, we're done
		if len(results) >= len(p.Submissions) {
			break
		}

		// If only one candidate remains, they're last
		var remaining []int
		for i := range p.Submissions {
			if !eliminated[i] {
				remaining = append(remaining, i)
			}
		}

		if len(remaining) == 1 {
			results = append(results, remaining[0])
			break
		}

		// Find candidate with fewest votes to eliminate
		if len(remaining) > 0 {
			minVotes := len(p.Votes) + 1
			var toEliminate int

			for idx, count := range counts {
				if count < minVotes && !eliminated[idx] {
					minVotes = count
					toEliminate = idx
				}
			}

			// If we found someone to eliminate but they're not already a winner
			if minVotes <= len(p.Votes) {
				eliminated[toEliminate] = true
			}
		}
	}

	return results
}

// RenderPollContent creates the Discord message content using ComponentsV2
func (p *Poll) RenderPollContent() []discordgo.MessageComponent {
	var contentParts []string

	// Build the content based on phase
	switch p.Phase {
	case PhaseSubmission:
		contentParts = append(contentParts, "# Video Game Club Poll\n")
		contentParts = append(contentParts, "Submit your game suggestions! Click the button below to add a game.\n\n")

		// Submissions section
		contentParts = append(contentParts, fmt.Sprintf("**Submissions (%d/%d)**\n", len(p.Submissions), MaxSubmissions))
		if len(p.Submissions) > 0 {
			for i, sub := range p.Submissions {
				contentParts = append(contentParts, fmt.Sprintf("**%d.** %s\n", i+1, sub.GameName))
				if sub.Description != "" {
					contentParts = append(contentParts, fmt.Sprintf("   %s\n", sub.Description))
				}
				if sub.Link != "" {
					contentParts = append(contentParts, fmt.Sprintf("   %s\n", sub.Link))
				}
				contentParts = append(contentParts, fmt.Sprintf("   *Submitted by %s*\n\n", sub.Username))
			}
		} else {
			contentParts = append(contentParts, "*No submissions yet*\n\n")
		}

		// Time remaining
		timeLeft := time.Until(p.EndTime)
		contentParts = append(contentParts, fmt.Sprintf("*Submission phase ends in %s*", formatDuration(timeLeft)))

	case PhaseVoting:
		contentParts = append(contentParts, "# Video Game Club Poll\n")
		contentParts = append(contentParts, "Vote for your preferred games! Rank all candidates from most to least preferred.\n\n")

		// Candidates section
		if len(p.Submissions) > 0 {
			contentParts = append(contentParts, "**Candidates**\n")
			for i, sub := range p.Submissions {
				contentParts = append(contentParts, fmt.Sprintf("**%d.** %s\n", i+1, sub.GameName))
				if sub.Description != "" {
					contentParts = append(contentParts, fmt.Sprintf("   %s\n", sub.Description))
				}
				if sub.Link != "" {
					contentParts = append(contentParts, fmt.Sprintf("   %s\n", sub.Link))
				}
				contentParts = append(contentParts, "\n")
			}
		}

		// Vote count
		contentParts = append(contentParts, fmt.Sprintf("**Votes**\n%d vote(s) cast\n\n", len(p.Votes)))

		// Time remaining
		timeLeft := time.Until(p.EndTime)
		contentParts = append(contentParts, fmt.Sprintf("*Voting ends in %s*", formatDuration(timeLeft)))

	case PhaseCompleted:
		contentParts = append(contentParts, "# Video Game Club Poll\n")
		contentParts = append(contentParts, "Voting has concluded! Here are the results:\n\n")

		// Results section
		results := p.CalculateResults()
		if len(results) > 0 {
			contentParts = append(contentParts, "**Final Rankings**\n")
			medals := []string{"🥇", "🥈", "🥉"}
			for i, idx := range results {
				sub := p.Submissions[idx]
				var medal string
				if i < len(medals) {
					medal = medals[i]
				} else {
					medal = fmt.Sprintf("%d.", i+1)
				}
				contentParts = append(contentParts, fmt.Sprintf("%s **%s**\n", medal, sub.GameName))
				if sub.Description != "" {
					contentParts = append(contentParts, fmt.Sprintf("   %s\n", sub.Description))
				}
				contentParts = append(contentParts, "\n")
			}
		}

		contentParts = append(contentParts, fmt.Sprintf("*Poll completed • %d vote(s) cast*", len(p.Votes)))
	}

	// Combine all content parts into a single string
	content := strings.Join(contentParts, "")

	// Create Container with TextDisplay
	container := discordgo.Container{
		Components: []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: content},
		},
	}

	return []discordgo.MessageComponent{container}
}

// RenderPollComponents creates the Discord message components for the poll
// This includes both content (Container + TextDisplay) and interactive components (buttons)
func (p *Poll) RenderPollComponents() []discordgo.MessageComponent {
	components := p.RenderPollContent()

	switch p.Phase {
	case PhaseSubmission:
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Submit Game",
				Style:    discordgo.PrimaryButton,
				CustomID: formID{PollID: p.ID, Kind: SubmitButton}.String(),
				Disabled: len(p.Submissions) >= MaxSubmissions,
			}, discordgo.Button{
				Label:    "Lock submissions",
				Style:    discordgo.DangerButton,
				CustomID: formID{PollID: p.ID, Kind: LockButton}.String(),
			},
		}})

	case PhaseVoting:
		components = append(components, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Cast Vote",
				Style:    discordgo.PrimaryButton,
				CustomID: formID{PollID: p.ID, Kind: VoteKind}.String(),
			}, discordgo.Button{
				Label:    "End Voting",
				Style:    discordgo.DangerButton,
				CustomID: formID{PollID: p.ID, Kind: EndButton}.String(),
			},
		}})

	case PhaseCompleted:
		// No buttons for completed polls, just content
	}

	return components
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

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "expired"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		if minutes > 0 {
			return fmt.Sprintf("%dh %dm", hours, minutes)
		}
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dm", minutes)
}

// RegisterPollHandlers registers all poll-related interaction handlers
func RegisterPollHandlers(s *discordgo.Session, pollState *PollState) {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		customID := ""
		// Handle button interactions
		if i.Type == discordgo.InteractionMessageComponent {
			customID = i.MessageComponentData().CustomID
		} else if i.Type == discordgo.InteractionModalSubmit {
			customID = i.ModalSubmitData().CustomID
		} else {
			return
		}

		f := parseForm(customID)
		slog.With("customID", customID).Info("Processing poll")
		handleFormEvent(s, i, pollState, f)

		if err := pollState.SaveToFile("polls.json"); err != nil {
			slog.Error("failed to save polls.json", "error", err, "id", customID)
		}
	})
}

func handleFormEvent(s *discordgo.Session, i *discordgo.InteractionCreate, pollState *PollState, f formID) {
	poll, ok := pollState.GetPoll(f.PollID)
	if !ok {
		slog.Warn("failed to find poll", "pollID", f.PollID)
		ephemeralNotice("Poll not found or has expired.", s, i)
		return
	}
	poll.mut.Lock()
	defer poll.mut.Unlock()

	switch f.Kind {
	case SubmitModal:
		HandleSubmitModal(s, i, poll)
	case VoteKind:
		HandleVoteButton(s, i, poll)
	case SubmitButton:
		HandleSubmitButton(s, i, poll)
	case VoteSelect:
		HandleVoteSelectMenu(s, i, poll, f.Rank)
	case LockButton:
		HandleLockButton(s, i, poll)
	case EndButton:
		HandleEndButton(s, i, poll)
	case VoteSubmit:
		HandleVoteSubmitButton(s, i, poll)
	}

	components := poll.RenderPollComponents()

	//Respond to the current interaction (because discord gets mad if you dont)
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Components: components,
			Flags:      discordgo.MessageFlagsIsComponentsV2,
		},
	})

	//Update the old interaction so that the original message is guaranteed to be updates
	_, err := s.InteractionResponseEdit(poll.Interaction, &discordgo.WebhookEdit{
		Components: &components,
	})
	if err != nil {
		slog.Error("failed to update poll message", "error", err, "poll_id", poll.ID)
	}
}

type kind string

var (
	SubmitModal  = kind("submit-modal")
	VoteSelect   = kind("vote-select")
	VoteSubmit   = kind("vote-submit")
	LockButton   = kind("lock")
	EndButton    = kind("end")
	VoteKind     = kind("vote")
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

// HandleSubmitButton opens the game submission modal
func HandleSubmitButton(s *discordgo.Session, i *discordgo.InteractionCreate, poll *Poll) {
	if poll.Phase != PhaseSubmission {
		ephemeralNotice("This poll is no longer accepting submissions.", s, i)
		return
	}

	if len(poll.Submissions) >= MaxSubmissions {
		ephemeralNotice(fmt.Sprintf("Maximum number of submissions (%d) has been reached.", MaxSubmissions), s, i)
		return
	}

	// Show modal
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: formID{PollID: poll.ID, Kind: SubmitModal}.String(),
			Title:    "Submit a Game",
			Components: []discordgo.MessageComponent{
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "game_name",
							Label:       "Game Name",
							Style:       discordgo.TextInputShort,
							Placeholder: "Enter the game name",
							Required:    true,
							MaxLength:   100,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "game_description",
							Label:       "Description",
							Style:       discordgo.TextInputParagraph,
							Placeholder: "Brief description or pitch for the game",
							Required:    true,
							MaxLength:   500,
						},
					},
				},
				discordgo.ActionsRow{
					Components: []discordgo.MessageComponent{
						discordgo.TextInput{
							CustomID:    "game_link",
							Label:       "Link (optional)",
							Style:       discordgo.TextInputShort,
							Placeholder: "Steam, website, or other link",
							Required:    false,
							MaxLength:   200,
						},
					},
				},
			},
		},
	})

	if err != nil {
		slog.Error("failed to show submission modal", "error", err)
	}
}

// HandleLockButton locks the poll early and transitions to voting
func HandleLockButton(s *discordgo.Session, i *discordgo.InteractionCreate, poll *Poll) {
	// Only poll creator can lock early
	if i.Member.User.ID != poll.CreatorID {
		ephemeralNotice("Only the poll creator can lock the poll early.", s, i)
		return
	}

	if poll.Phase != PhaseSubmission {
		ephemeralNotice("This poll is not in the submission phase.", s, i)
		return
	}

	if len(poll.Submissions) == 0 {
		ephemeralNotice("Cannot lock poll with no submissions.", s, i)
		return
	}

	slog.Info("transitioning poll to voting phase", "poll_id", poll.ID)

	poll.Phase = PhaseVoting
}

// HandleVoteButton opens the voting interface with dropdown menus
func HandleVoteButton(s *discordgo.Session, i *discordgo.InteractionCreate, poll *Poll) {
	if poll.Phase != PhaseVoting {
		ephemeralNotice("This poll is not currently accepting votes.", s, i)
		return
	}

	if len(poll.Submissions) == 0 {
		ephemeralNotice("There are no submissions to vote on.", s, i)
		return
	}

	// Show the voting interface
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Components: buildVoteFormComponents(poll, ""),
			Flags:      discordgo.MessageFlagsIsComponentsV2 | discordgo.MessageFlagsEphemeral,
		},
	})

	if err != nil {
		slog.Error("failed to show voting interface", "error", err)
		return
	}
}

// HandleEndButton ends the voting early and shows results
func HandleEndButton(s *discordgo.Session, i *discordgo.InteractionCreate, poll *Poll) {
	// Only poll creator can end early
	if i.Member.User.ID != poll.CreatorID {
		ephemeralNotice("Only the poll creator can end voting early.", s, i)
		return
	}

	if poll.Phase != PhaseVoting {
		ephemeralNotice("This poll is not in the voting phase.", s, i)
		return
	}

	slog.Info("completing poll", "poll_id", poll.ID)

	poll.Phase = PhaseCompleted
}

// buildVoteFormComponents creates the voting form components with optional error message
func buildVoteFormComponents(poll *Poll, errorText string) []discordgo.MessageComponent {
	// Build select menu options from submissions
	options := make([]discordgo.SelectMenuOption, len(poll.Submissions))
	for idx, sub := range poll.Submissions {
		options[idx] = discordgo.SelectMenuOption{
			Label:       fmt.Sprintf("%d. %s", idx+1, sub.GameName),
			Value:       fmt.Sprintf("%d", idx),
			Description: truncateString(sub.Description, 100),
		}
	}

	// Create dropdown menus for each rank position
	var components []discordgo.MessageComponent

	for rank := 0; rank < len(poll.Submissions); rank++ {
		// Generate ordinal label (1st, 2nd, 3rd, etc.)
		var rankLabel string
		if rank == 0 {
			rankLabel = "1st Choice"
		} else {
			suffix := "th"
			num := rank + 1
			if num%10 == 1 && num != 11 {
				suffix = "st"
			} else if num%10 == 2 && num != 12 {
				suffix = "nd"
			} else if num%10 == 3 && num != 13 {
				suffix = "rd"
			}
			rankLabel = fmt.Sprintf("%d%s Choice", num, suffix)
		}

		components = append(components, discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{discordgo.SelectMenu{
				CustomID:    formID{PollID: poll.ID, Kind: VoteSelect, Rank: rank}.String(),
				Placeholder: rankLabel,
				Options:     options,
			}}})
	}

	components = append(components, discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Submit Rankings",
				Style:    discordgo.SuccessButton,
				CustomID: formID{PollID: poll.ID, Kind: VoteSubmit}.String(),
			},
		},
	})

	if errorText != "" {
		components = append(components, discordgo.TextDisplay{Content: fmt.Sprintf("⚠️ **Error:** %s\n\n", errorText)})
	}
	components = append(components, discordgo.TextDisplay{Content: "**Rank the games below then Submit:**"})

	return components
}

func ephemeralNotice(content string, s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Components: []discordgo.MessageComponent{discordgo.Container{Components: []discordgo.MessageComponent{discordgo.TextDisplay{Content: content}}}},
			Flags:      discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsIsComponentsV2,
		},
	})
}
func ephemeralUpdate(components []discordgo.MessageComponent, s *discordgo.Session, i *discordgo.InteractionCreate) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Components: components,
			Flags:      discordgo.MessageFlagsEphemeral | discordgo.MessageFlagsIsComponentsV2,
		},
	})
}

// HandleSubmitModal processes game submission from modal
func HandleSubmitModal(s *discordgo.Session, i *discordgo.InteractionCreate, poll *Poll) {
	if poll.Phase != PhaseSubmission {
		ephemeralNotice("Failed to submit game: poll is not in submission phase", s, i)
		return
	}

	if len(poll.Submissions) >= MaxSubmissions {
		ephemeralNotice("Failed to submit game: too many games are already submitted", s, i)
		return
	}

	var gameName, description, link string
	for _, component := range i.ModalSubmitData().Components {
		if actionRow, ok := component.(*discordgo.ActionsRow); ok {
			for _, comp := range actionRow.Components {
				if textInput, ok := comp.(*discordgo.TextInput); ok {
					switch textInput.CustomID {
					case "game_name":
						gameName = textInput.Value
					case "game_description":
						description = textInput.Value
					case "game_link":
						link = textInput.Value
					}
				}
			}
		}
	}

	poll.Submissions = append(poll.Submissions, Submission{
		UserID:      i.Member.User.ID,
		Username:    i.Member.User.Username,
		GameName:    gameName,
		Description: description,
		Link:        link,
		SubmittedAt: time.Now(),
	})
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Successfully submitted **%s**!", gameName),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// HandleVoteSelectMenu handles dropdown selection for voting
func HandleVoteSelectMenu(s *discordgo.Session, i *discordgo.InteractionCreate, poll *Poll, rankPosition int) {
	defer func() {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredMessageUpdate,
		})
	}()
	slog.Info("parsed vote select menu", "poll_id", poll.ID, "rank_pos", rankPosition, "user_id", i.Member.User.ID)

	// Get selected value
	values := i.MessageComponentData().Values
	if len(values) == 0 {
		slog.Error("no values selected in dropdown", "poll_id", poll.ID, "user_id", i.Member.User.ID)
		return
	}
	selectedIdx, err := strconv.Atoi(values[0])
	if err != nil {
		slog.Error("something went wrong", "poll_id", poll.ID, "user_id", i.Member.User.ID, values[0])
		return
	}
	slog.Info("user selected game", "poll_id", poll.ID, "user_id", i.Member.User.ID, "rank_pos", rankPosition, "game_idx", selectedIdx)

	poll.UpsertVote(i.Member.User.ID, rankPosition, selectedIdx)
}

// HandleVoteSubmitButton processes the final vote submission
func HandleVoteSubmitButton(s *discordgo.Session, i *discordgo.InteractionCreate, poll *Poll) {
	userID := i.Member.User.ID
	logger := slog.With("poll_id", poll.ID, "user_id", userID)
	logger.Info("parsed vote submit button")

	vote := Vote{}
	// Get the stored selections
	for _, v := range poll.Votes {
		if v.UserID == userID {
			vote = v
		}
	}
	if vote.UserID != userID {
		components := buildVoteFormComponents(poll, fmt.Sprintf("Unexpected voter %s", userID))
		ephemeralUpdate(components, s, i)
	}

	// Record the vote
	err := poll.FinalizeVote(userID)
	if err != nil {
		logger.Error("failed to add vote to poll", "error", err)
		components := buildVoteFormComponents(poll, fmt.Sprintf("Failed to record vote: %s", err.Error()))
		ephemeralUpdate(components, s, i)
		return
	}

	// Update the message to show success and remove the form
	logger.Info("responding with success message")
	ephemeralUpdate([]discordgo.MessageComponent{
		discordgo.Container{
			Components: []discordgo.MessageComponent{
				discordgo.TextDisplay{Content: "✅ **Vote recorded successfully!**\n\nThank you for voting. Your rankings have been saved."},
			},
		},
	}, s, i)
}

// Helper functions

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
