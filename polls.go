package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/template"
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

var (
	pollTemplateFuncs = template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"medal": func(i int) string {
			medals := []string{"🥇", "🥈", "🥉"}
			if i < len(medals) {
				return medals[i]
			}
			return fmt.Sprintf("%d.", i+1)
		},
	}

	submissionTemplate = template.Must(template.New("submission").Funcs(pollTemplateFuncs).Parse(`# Video Game Club Poll
Submit your game suggestions! Click the button below to add a game.

**Submissions ({{.SubmissionCount}}/{{.MaxSubmissions}})**
{{- if .Submissions}}
{{range $i, $sub := .Submissions}}
**{{add $i 1}}.** {{$sub.GameName}}
{{- if $sub.Description}}
   {{$sub.Description}}
{{- end}}
{{- if $sub.Link}}
   {{$sub.Link}}
{{- end}}
   *Submitted by {{$sub.Username}}*

{{end}}
{{- else}}
*No submissions yet*

{{end}}
*Submission phase ends in {{.TimeRemaining}}*`))

	votingTemplate = template.Must(template.New("voting").Funcs(pollTemplateFuncs).Parse(`# Video Game Club Poll
Vote for your preferred games! Rank all candidates from most to least preferred.

{{- if .Submissions}}
**Candidates**
{{range $i, $sub := .Submissions}}
**{{add $i 1}}.** {{$sub.GameName}}
{{- if $sub.Description}}
   {{$sub.Description}}
{{- end}}
{{- if $sub.Link}}
   {{$sub.Link}}
{{- end}}

{{end}}
{{- end}}
**Votes**
{{.VoteCount}} vote(s) cast

*Voting ends in {{.TimeRemaining}}*`))

	completedTemplate = template.Must(template.New("completed").Funcs(pollTemplateFuncs).Parse(`# Video Game Club Poll
Voting has concluded! Here are the results:

{{- if .Results}}
**Final Rankings**
{{range $i, $idx := .Results}}
{{medal $i}} **{{(index $.Submissions $idx).GameName}}**
{{- with index $.Submissions $idx}}
{{- if .Description}}
   {{.Description}}
{{- end}}

{{- end}}
{{end}}
{{- end}}
*Poll completed • {{.VoteCount}} vote(s) cast*`))
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

// pollTemplateData holds the data for rendering poll templates
type pollTemplateData struct {
	SubmissionCount int
	MaxSubmissions  int
	Submissions     []Submission
	VoteCount       int
	TimeRemaining   string
	Results         []int
}

// RenderPollContent creates the Discord message content using ComponentsV2
func (p *Poll) RenderPollContent() []discordgo.MessageComponent {
	var buf bytes.Buffer
	var err error

	data := pollTemplateData{
		SubmissionCount: len(p.Submissions),
		MaxSubmissions:  MaxSubmissions,
		Submissions:     p.Submissions,
		VoteCount:       len(p.Votes),
		TimeRemaining:   formatDuration(time.Until(p.EndTime)),
	}

	switch p.Phase {
	case PhaseSubmission:
		err = submissionTemplate.Execute(&buf, data)
	case PhaseVoting:
		err = votingTemplate.Execute(&buf, data)
	case PhaseCompleted:
		data.Results = p.CalculateResults()
		err = completedTemplate.Execute(&buf, data)
	}

	if err != nil {
		slog.Error("failed to render poll content", "error", err, "poll_id", p.ID)
		return []discordgo.MessageComponent{discordgo.Container{
			Components: []discordgo.MessageComponent{
				discordgo.TextDisplay{Content: "Error rendering poll content"},
			},
		}}
	}

	container := discordgo.Container{
		Components: []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: buf.String()},
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
				CustomID: formID{PollID: p.ID, Kind: VoteButton}.String(),
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
	case VoteButton:
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

	switch f.Kind {
	case VoteButton,
		SubmitButton,
		LockButton,
		EndButton:
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseUpdateMessage,
			Data: &discordgo.InteractionResponseData{
				Components: poll.RenderPollComponents(),
				Flags:      discordgo.MessageFlagsIsComponentsV2,
			},
		})
		return
	}

	components := poll.RenderPollComponents()
	_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:         poll.MessageID,
		Channel:    i.ChannelID,
		Components: &components,
		Flags:      discordgo.MessageFlagsIsComponentsV2,
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
		rankLabel := fmt.Sprintf("%d%s Choice", rank+1, ordinalSuffix(rank+1))
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

	gameName := getModalField(i, "game_name")
	poll.Submissions = append(poll.Submissions, Submission{
		UserID:      i.Member.User.ID,
		Username:    i.Member.User.Username,
		GameName:    gameName,
		Description: getModalField(i, "game_description"),
		Link:        getModalField(i, "game_link"),
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

func getModalField(i *discordgo.InteractionCreate, fieldID string) string {
	for _, component := range i.ModalSubmitData().Components {
		if actionRow, ok := component.(*discordgo.ActionsRow); ok {
			for _, comp := range actionRow.Components {
				if textInput, ok := comp.(*discordgo.TextInput); ok && textInput.CustomID == fieldID {
					return textInput.Value
				}
			}
		}
	}
	return ""
}

func ordinalSuffix(n int) string {
	if n%10 == 1 && n%100 != 11 {
		return "st"
	} else if n%10 == 2 && n%100 != 12 {
		return "nd"
	} else if n%10 == 3 && n%100 != 13 {
		return "rd"
	}
	return "th"
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
