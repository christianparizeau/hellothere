# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

hellothere is a Discord bot that notifies users when their friends join voice channels. It uses the discordgo library to interact with Discord's API and provides features like voice join notifications, soundboard integration, role management via reactions, and slash commands.

## Build and Run Commands

**Build:**
```bash
go build -o hellothere
```

**Run (requires Discord bot token as first argument):**
```bash
go run . <DISCORD_BOT_TOKEN>
```

The bot requires the following Discord permissions:
- See presence
- See users
- Manage roles
- See voice activity
- Send messages

OAuth2 authorization URL format:
```
https://discord.com/oauth2/authorize?client_id=<CLIENT_ID>&permissions=39584871222336&integration_type=0&scope=bot
```

## Configuration

Configuration is handled through an embedded `config.json` file. The structure is:

```json
{
  "guild_id": {
    "NotificationChannelID": "channel_id",
    "EmojiID": "emoji_id",
    "RequiredRoleName": "role_name",
    "RoleConfig": {
      "ManagementChannelID": "channel_id",
      "MessageID": "message_id",
      "EmojiRoleConfig": {
        "emoji_name": "role_name"
      }
    },
    "UserConfig": {
      "username": {
        "OnJoinSound": "soundboard_sound_id"
      }
    }
  }
}
```

Configuration is embedded at build time via `//go:embed config.json`.

## Architecture

### Core Components

**main.go**
- Entry point and bot initialization
- Registers all event handlers: presence updates, voice state updates, message reactions, slash commands
- Three main handlers:
  - `playSoundOnJoin`: Plays a soundboard sound when configured users join voice channels
  - `notifyOnJoin`: Sends notifications to designated channel when users join voice (respects quiet hours: 8 AM - 10 PM)
  - `reactionHandler`: Manages role assignment/removal based on message reactions

**config.go**
- Manages bot configuration per guild
- Loads embedded `config.json` and resolves role/channel IDs during guild registration
- `botConfig` maintains a map of guild configs with mutex for thread safety
- `GuildConfig` contains per-guild settings: notification channels, emoji, role requirements, user configs

**reactions.go**
- Handles message reaction events to manage role assignment
- Users react to specific messages to self-assign roles
- `ReactionRelevant()` validates reactions are on the correct channel/message

**slash_commands.go**
- Defines slash command handlers
- `/voice-spam`: Opts user into voice notifications by adding the required role
- `/no-spam`: Opts user out by removing the role
- Commands are registered per guild on bot startup

### Key Features

**Voice Join Notifications**
- Monitors voice state updates via `discordgo.VoiceStateUpdate` events
- Filters notifications based on:
  - User must have required role (opt-in)
  - User presence status (online/idle, not DND/invisible)
  - Not a bot user
  - Outside quiet hours (before 8 AM or after 10 PM)
  - Actual voice channel join (not mute/channel change)
  - Timeout corner (5 minute cooldown to prevent spam)

**Soundboard Integration**
- Plays custom sounds when specific users join voice channels
- Bot joins voice channel temporarily, sends soundboard sound via Discord API
- 5 second delay before disconnecting (sound playback time)

**Role Management**
- Reaction-based role assignment on designated messages
- Slash command-based opt-in/opt-out for voice notifications

### Global State

- `timeoutCorner`: sync.Map tracking recent voice joins to prevent notification spam (5 minute timeout)

### Logging

All logging uses `slog` with JSON handler, debug level enabled, with source attribution.
