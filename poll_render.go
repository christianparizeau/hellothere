package main

import (
	"bytes"
	"fmt"
	"log/slog"
	"text/template"
	"time"

	"github.com/bwmarrin/discordgo"
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
