package discord

import (
	"tetora/internal/config"
)


// newDiscordForumBoard constructs a ForumBoard wired to Bot dependencies.
func newDiscordForumBoard(bot *Bot, cfg config.DiscordForumBoardConfig) *ForumBoard {
	var deps ForumBoardDeps
	var client *Client

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

	return NewForumBoard(client, cfg, deps)
}
