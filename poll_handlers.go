package main

import (
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
)

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
		slog.Error("something went wrong", "poll_id", poll.ID, "user_id", i.Member.User.ID, "value", values[0])
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
