package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/bwmarrin/discordgo"
)

//go:embed config.json
var configFile []byte

type botConfig struct {
	guilds map[string]GuildConfig
	mut    sync.Mutex
	logger *slog.Logger
}

func (c *botConfig) Register(s *discordgo.Session) {
	//handle the ready event to prepare a config object with guild-specific info
	s.AddHandler(func(s *discordgo.Session, vs *discordgo.Ready) {
		c.logger.Info("ready")
		for _, g := range vs.Guilds {
			err := c.registerGuild(s, g)
			if err != nil {
				c.logger.Error("error registering guild",
					slog.String("guild", g.Name),
					slog.String("err", err.Error()),
				)
				return
			}
		}
	})
}

func (c *botConfig) Get(guildID string) GuildConfig {
	guildConfig, ok := c.guilds[guildID]
	if !ok {
		c.logger.Warn("unknown guild")
		return GuildConfig{}
	}
	return guildConfig
}

// registerGuild takes a guild and returns a GuildConfig with all the roles resolved
func (c *botConfig) registerGuild(s *discordgo.Session, g *discordgo.Guild) error {
	//We have to fully resolve the guild, the incoming object is a partial because :(
	guild, err := s.Guild(g.ID)
	if err != nil {
		return err
	}
	c.mut.Lock()
	defer c.mut.Unlock()
	gc := c.guilds[guild.ID]
	gc.logger = c.logger.With(slog.String("guild", g.Name), slog.String("guild_id", g.ID))

	roles := make(map[string]*discordgo.Role, len(guild.Emojis))
	for _, role := range guild.Roles {
		roles[role.Name] = role
	}

	role, ok := roles[gc.RequiredRoleName]
	if ok {
		gc.requiredRoleID = role.ID
	}
	if gc.RoleConfig.MessageID != "" {
		for emojiName, roleName := range gc.RoleConfig.EmojiRoleConfig {
			role, ok := roles[roleName]
			if !ok {
				return fmt.Errorf("could not find role '%s'", roleName)
			}
			gc.RoleConfig.EmojiRoleConfig[emojiName] = role.ID
		}
	}
	c.guilds[guild.ID] = gc
	return nil
}

type GuildConfig struct {
	NotificationChannelID string
	EmojiID               string
	RequiredRoleName      string

	UserConfig map[string]UserConfig
	//emoji name to role name
	RoleConfig RoleConfig

	requiredRoleID string

	logger *slog.Logger
}

type RoleConfig struct {
	ManagementChannelID string
	MessageID           string

	EmojiRoleConfig map[string]string
}

func (rc RoleConfig) ReactionRelevant(logger *slog.Logger, er *discordgo.MessageReaction) (string, bool) {
	//If the emoji is of the proper kind on the proper message in the proper channel
	if er.ChannelID != rc.ManagementChannelID {
		logger.Debug("wrong channel")
		return "", false
	}
	if er.MessageID != rc.MessageID {
		logger.Debug("wrong message")
		return "", false
	}
	role, ok := rc.EmojiRoleConfig[er.Emoji.Name]
	if !ok {
		logger.Debug("unknown emoji")
		return "", false
	}
	return role, true
}

type UserConfig struct {
	OnJoinSound string
}

func newBotConfig() (*botConfig, error) {
	config := botConfig{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource:   true,
			Level:       slog.LevelDebug,
			ReplaceAttr: nil,
		})),
	}
	err := json.Unmarshal(configFile, &config.guilds)
	if err != nil {
		return nil, err
	}
	return &config, nil
}
