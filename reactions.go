package main

import (
	"log/slog"

	"github.com/bwmarrin/discordgo"
)

type reactionHandler struct {
	config *botConfig
}

func (r reactionHandler) Register(s *discordgo.Session) {
	s.AddHandler(r.handleAdd)
	s.AddHandler(r.handleRemove)
}

func (r reactionHandler) handleAdd(s *discordgo.Session, reactionAdd *discordgo.MessageReactionAdd) {
	reaction := reactionAdd.MessageReaction
	guildConfig := r.config.Get(reaction.GuildID)
	logger := reactionLogger(guildConfig.logger, reaction)

	//If the emoji is of the proper kind on the proper message in the proper channel
	role, relevant := guildConfig.RoleConfig.ReactionRelevant(logger, reaction)
	if !relevant {
		return
	}
	err := s.GuildMemberRoleAdd(reaction.GuildID, reaction.UserID, role)
	if err != nil {
		logger.Error("failed to add role", slog.String("err", err.Error()))
		return
	}
}

func (r reactionHandler) handleRemove(s *discordgo.Session, reactionRemove *discordgo.MessageReactionRemove) {
	reaction := reactionRemove.MessageReaction
	guildConfig := r.config.Get(reaction.GuildID)
	logger := reactionLogger(guildConfig.logger, reaction)

	//If the emoji is of the proper kind on the proper message in the proper channel
	role, relevant := guildConfig.RoleConfig.ReactionRelevant(logger, reaction)
	if !relevant {
		return
	}
	err := s.GuildMemberRoleRemove(reaction.GuildID, reaction.UserID, role)
	if err != nil {
		logger.Error("failed to add role", slog.String("err", err.Error()))
		return
	}
}

func reactionLogger(logger *slog.Logger, er *discordgo.MessageReaction) *slog.Logger {
	return logger.With(
		slog.String("channel", er.ChannelID),
		slog.String("message", er.MessageID),
		slog.String("emoji", er.Emoji.Name),
		slog.String("user", er.UserID),
	)
}
