package main

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

var timeoutCorner sync.Map

const timeout = 5 * time.Minute

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(_ context.Context) error {
	config, err := newBotConfig()
	if err != nil {
		return err
	}

	// Initialize poll state and load existing polls
	pollState := NewPollState(config.logger, "polls.json")
	err = pollState.LoadFromFile()
	if err != nil {
		config.logger.Warn("failed to load poll state", "error", err)
	}

	//start a bot. args[1] should be the token for the bot.
	//bot needs permission to see presence, see users, manage roles, see voice activity, and send messages
	//https://discord.com/oauth2/authorize?client_id=408164522067755008&permissions=39584871222336&integration_type=0&scope=bot
	session, err := discordgo.New("Bot " + os.Args[1])
	if err != nil {
		return err
	}

	//Add presence updates
	session.Identify.Intents = discordgo.IntentsAllWithoutPrivileged | discordgo.IntentGuildPresences
	session.AddHandler(func(s *discordgo.Session, m *discordgo.PresenceUpdate) {
		config.logger.Debug("presence update", slog.String("user", m.User.ID), slog.String("status", string(m.Status)))
	})
	ready := make(chan struct{})
	session.AddHandler(func(s *discordgo.Session, m *discordgo.Ready) {
		config.logger.Debug("READY", slog.String("user", m.User.ID))
		close(ready)
	})
	config.Register(session)

	playSoundOnJoin{config: config}.Register(session)
	notifyOnJoin{config: config}.Register(session)
	reactionHandler{config: config}.Register(session)
	RegisterPollHandlers(session, pollState)
	commands := newSlashCommands(config, pollState)
	commands.Register(session)

	err = session.Open()
	if err != nil {
		return err
	}
	select {
	case <-ready:
	case <-time.After(timeout):
		return fmt.Errorf("timed out waiting for bot to start")
	}

	//create the slash commands. This must be done after the bot is open so that the bot id is known
	err = commands.CreateCommands(session, config)
	if err != nil {
		return err
	}

	fmt.Println("hello-there is now running.  Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	// Save poll state before shutting down
	slog.Info("saving poll state before shutdown")
	err = pollState.SaveToFile()
	if err != nil {
		slog.Error("failed to save poll state", "error", err)
	}

	// Cleanly close down the Discord session.
	return session.Close()
}

type playSoundOnJoin struct {
	config *botConfig
}

func (p playSoundOnJoin) Register(s *discordgo.Session) {
	s.AddHandler(func(s *discordgo.Session, vs *discordgo.VoiceStateUpdate) {
		c := p.config.Get(vs.GuildID)
		logger := c.logger.With(
			slog.String("username", vs.Member.User.Username),
			slog.String("guild", vs.GuildID),
			slog.String("channel", vs.ChannelID),
		)
		soundID := c.UserConfig[vs.Member.User.Username].OnJoinSound
		if soundID == "" {
			logger.Debug("user does not have a join sound configured")
			return
		}
		//check if the user is just joining voice. This prevents mute/change channel/etc from triggering the sound
		channelID := vs.ChannelID
		if vs.BeforeUpdate != nil && channelID == vs.BeforeUpdate.ChannelID {
			logger.Debug("user already in same channel")
			return
		}

		//in order to play a sound we must join the channel and not be muted
		vc, err := s.ChannelVoiceJoin(vs.GuildID, channelID, false, false)
		if err != nil {
			logger.Error("could not join voice channel", slog.String("err", err.Error()))
			return
		}
		defer vc.Close()

		//Then we post the sound! The sound should be from the same guild (or we need to update this to handle cross guild sounds)
		_, err = s.Request(http.MethodPost, fmt.Sprintf("%s/%s", discordgo.EndpointChannel(channelID), "send-soundboard-sound"), map[string]string{
			"sound_id": soundID,
		})
		if err != nil {
			logger.Error("could not send request", slog.String("err", err.Error()))
			return
		}
		//There's not a simple way that I can see with discords api to know when the sound is done playing,
		//or to get the length of the sound. We could listen to the channel and wait for quiet or parse the mp3 to get the length.
		//Neither of which seems worth the complexity.
		time.Sleep(5 * time.Second)
		if err := vc.Disconnect(); err != nil {
			logger.Error("could not disconnect", slog.String("err", err.Error()))
			return
		}
	})
}

type notifyOnJoin struct {
	config *botConfig
}

func (n notifyOnJoin) Register(s *discordgo.Session) {
	s.AddHandler(func(s *discordgo.Session, vs *discordgo.VoiceStateUpdate) {
		c := n.config.Get(vs.GuildID)
		logger := c.logger.With(
			slog.String("username", vs.Member.User.Username),
			slog.String("guild", vs.GuildID),
			slog.String("channel", vs.ChannelID),
		)

		logger.Info("voice state update")
		if !shouldNotify(s, vs, logger, c.requiredRoleID) {
			return
		}

		message, err := buildNotificationMessage(c, vs, s)
		if err != nil {
			logger.Error("could not build message", slog.String("err", err.Error()))
			return
		}
		if _, err := s.ChannelMessageSend(c.NotificationChannelID, message); err != nil {
			logger.Error("could not sent message", slog.String("err", err.Error()))
			return
		}

		timeoutCorner.Store(vs.UserID, true)
		time.AfterFunc(timeout, func() { timeoutCorner.Delete(vs.UserID) })
	})
}

func shouldNotify(s *discordgo.Session, vs *discordgo.VoiceStateUpdate, logger *slog.Logger, requiredRoleID string) bool {
	//skip bot users since we are a bot (and other bots are probably just spam)
	if vs.Member.User.Bot {
		return false
	}
	//check if the user is just joining voice. This prevents mute/change channel/etc from triggering the notification
	if vs.BeforeUpdate != nil {
		logger.Debug("user already in a voice channel")
		return false
	}

	//check quiet hours
	current := time.Now().Hour()
	if current < 8 || current > 22 {
		logger.Debug("quiet hours in effect")
		return false
	}

	//check the users presence
	p, err := s.State.Presence(vs.GuildID, vs.UserID)
	if err != nil {
		logger.Warn("user presence could not be detected")
		return false
	}
	//Allow DND and invisible to be ignored
	if p.Status != discordgo.StatusOnline && p.Status != discordgo.StatusIdle {
		logger.Debug("user is incognito")
		return false
	}

	//Ensure the user has opted in to notifications by adopting the role
	if !userHasRole(vs.Member.Roles, requiredRoleID) {
		logger.Debug("user does not have role")
		return false
	}

	_, ok := timeoutCorner.Load(vs.UserID)
	if ok {
		logger.Debug("user already joined recently")
		return false
	}

	return true
}

func buildNotificationMessage(c GuildConfig, vs *discordgo.VoiceStateUpdate, session *discordgo.Session) (string, error) {
	b := strings.Builder{}

	b.WriteString(c.EmojiID + " looks like ")
	if vs.Member.Nick != "" {
		b.WriteString(vs.Member.Nick)
	} else {
		b.WriteString(vs.Member.User.Username)
	}
	b.WriteString(" just joined ")

	channel, err := session.Channel(vs.ChannelID)
	if err != nil {
		return "", nil
	}

	b.WriteString(channel.Name)
	return b.String(), nil
}

func userHasRole(userRoleIDs []string, serverRoleID string) bool {
	return slices.Contains(userRoleIDs, serverRoleID)
}
