package discord

import "context"

// --- P27.3: Discord Channel Notifier ---

type discordChannelNotifier struct {
	bot       *Bot
	channelID string
}

func (n *discordChannelNotifier) SendTyping(ctx context.Context) error {
	n.bot.sendTyping(n.channelID)
	return nil
}

func (n *discordChannelNotifier) SendStatus(ctx context.Context, msg string) error {
	n.bot.sendTyping(n.channelID)
	return nil
}
