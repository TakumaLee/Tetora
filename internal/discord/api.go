package discord

func (b *Bot) notifyChannelID() string {
	if len(b.cfg.Discord.ChannelIDs) > 0 {
		return b.cfg.Discord.ChannelIDs[0]
	}
	return b.cfg.Discord.ChannelID
}

func (b *Bot) sendNotify(text string) {
	ch := b.notifyChannelID()
	if ch == "" {
		return
	}
	b.sendMessage(ch, text)
}

// discordPost delegates to the api client (kept for callers in other files).
func (b *Bot) discordPost(path string, payload any) {
	b.api.Post(path, payload)
}

// discordRequestWithResponse delegates to the api client (kept for callers in other files).
func (b *Bot) discordRequestWithResponse(method, path string, payload any) ([]byte, error) {
	return b.api.Request(method, path, payload)
}

// sendMessageReturningID sends a message and returns the message ID.
func (b *Bot) sendMessageReturningID(channelID, content string) (string, error) {
	return b.api.SendMessageReturningID(channelID, content)
}

// editMessage edits an existing Discord message.
func (b *Bot) editMessage(channelID, messageID, content string) error {
	return b.api.EditMessage(channelID, messageID, content)
}

// editMessageWithComponents edits an existing Discord message, replacing content and components.
func (b *Bot) editMessageWithComponents(channelID, messageID, content string, components []Component) error {
	return b.api.EditMessageWithComponents(channelID, messageID, content, components)
}

// deleteMessage deletes a Discord message.
func (b *Bot) deleteMessage(channelID, messageID string) {
	b.api.DeleteMessage(channelID, messageID)
}

// sendMessageWithComponents sends a message with interactive components (buttons, selects, etc.).
func (b *Bot) sendMessageWithComponents(channelID, content string, components []Component) {
	b.api.SendMessageWithComponents(channelID, content, components)
}

// sendMessageWithComponentsReturningID sends a message with components and returns the message ID.
func (b *Bot) sendMessageWithComponentsReturningID(channelID, content string, components []Component) (string, error) {
	return b.api.SendMessageWithComponentsReturningID(channelID, content, components)
}

// sendEmbedWithComponents sends an embed message with interactive components.
func (b *Bot) sendEmbedWithComponents(channelID string, embed Embed, components []Component) {
	b.api.SendEmbedWithComponents(channelID, embed, components)
}

func (b *Bot) sendMessage(channelID, content string) {
	b.api.SendMessage(channelID, content)
}

func (b *Bot) sendEmbed(channelID string, embed Embed) {
	b.api.SendEmbed(channelID, embed)
}

func (b *Bot) sendEmbedReply(channelID, replyToID string, embed Embed) {
	b.api.SendEmbedReply(channelID, replyToID, embed)
}

func (b *Bot) sendTyping(channelID string) {
	b.api.SendTyping(channelID)
}
