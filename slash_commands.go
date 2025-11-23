package main

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

type slashCommand struct {
	Description string
	Options     []*discordgo.ApplicationCommandOption
	Handler     func(s *discordgo.Session, i *discordgo.InteractionCreate)
}

type slashCommands map[string]slashCommand

func (c slashCommands) Register(s *discordgo.Session) {
	s.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		// Only handle application command interactions (not buttons or modals)
		if i.Type != discordgo.InteractionApplicationCommand {
			return
		}
		if h, ok := c[i.ApplicationCommandData().Name]; ok {
			h.Handler(s, i)
		}
	})
}

func (c slashCommands) CreateCommands(s *discordgo.Session, config *botConfig) error {
	for guildID := range config.guilds {
		for name, cmd := range c {
			_, err := s.ApplicationCommandCreate(s.State.User.ID, guildID, &discordgo.ApplicationCommand{
				Name:        name,
				Description: cmd.Description,
				Options:     cmd.Options,
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func spamHandler(config *botConfig, optOut bool) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		gc := config.Get(i.GuildID)
		if err := s.GuildMemberRoleAdd(i.GuildID, i.Member.User.ID, gc.requiredRoleID); err != nil {
			gc.logger.Error("could not add role to user", slog.String("err", err.Error()), slog.String("guild", i.GuildID), slog.String("user", i.Member.User.Username))
			return
		}
		content := "Thou hast been granted \"hello-there\""
		if optOut {
			content = "Thou hast had thy privileges revoked"
		}
		ephemeralNotice(content, s, i)
	}
}

func createPollHandler(pollState *PollState) func(s *discordgo.Session, i *discordgo.InteractionCreate) {
	return func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		options := i.ApplicationCommandData().Options
		if len(options) != 1 {
			ephemeralNotice("Invalid command usage. Use: /create-vgc-poll <expected-hours>", s, i)
			return
		}

		expectedHours := int(options[0].IntValue())

		// Validate hours
		if expectedHours < 1 || expectedHours > 168 {
			ephemeralNotice("Submission hours must be between 1 and 168 (1 week)", s, i)
			return
		}

		// Create the poll
		poll := CreatePoll(i.GuildID, i.ChannelID, i.Member.User.ID, i.Interaction, expectedHours)

		// Create the poll message
		components := poll.RenderPollComponents()
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Components: components,
				Flags:      discordgo.MessageFlagsIsComponentsV2,
			},
		})

		pollState.AddPoll(poll)

		// Save state
		err := pollState.SaveToFile("polls.json")
		if err != nil {
			slog.Error("failed to save poll state", "error", err)
		}

		slog.Info("created poll", "poll_id", poll.ID, "guild_id", poll.GuildID)
	}
}

func newSlashCommands(config *botConfig, pollState *PollState) slashCommands {

	return slashCommands{
		"voice-spam": {
			Description: "opts the user in to the voice-spam role",
			Handler:     spamHandler(config, false),
		},
		"no-spam": {
			Description: "opts the user out of the voice-spam role",
			Handler:     spamHandler(config, true),
		},
		"create-vgc-poll": {
			Description: "Create a ranked choice voting poll for the video game club",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "expected-hours",
					Description: "Hours for the poll (1-168)",
					Required:    true,
					MinValue:    ref(1.),
					MaxValue:    168,
				},
			},
			Handler: createPollHandler(pollState),
		},
	}
}
func ref[T any](value T) *T {
	return &value
}
