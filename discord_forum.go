package main

import "tetora/internal/discord"

// newDiscordForumBoard constructs a ForumBoard wired to DiscordBot dependencies.
func newDiscordForumBoard(bot *DiscordBot, cfg DiscordForumBoardConfig) *discord.ForumBoard {
	var deps discord.ForumBoardDeps
	var client *discord.Client

	if bot != nil {
		client = bot.api

		if bot.cfg != nil {
			deps.ThreadBindingsEnabled = bot.cfg.Discord.ThreadBindings.Enabled

			deps.ValidateAgent = func(name string) bool {
				if bot.cfg.Agents == nil {
					return false
				}
				_, ok := bot.cfg.Agents[name]
				return ok
			}
		}

		deps.AvailableRoles = func() []string {
			return bot.availableRoleNames()
		}

		if bot.threads != nil && bot.cfg != nil {
			deps.BindThread = func(guildID, threadID, role string) string {
				ttl := bot.cfg.Discord.ThreadBindings.ThreadBindingsTTL()
				return bot.threads.bind(guildID, threadID, role, ttl)
			}
		}
	}

	return discord.NewForumBoard(client, cfg, deps)
}
