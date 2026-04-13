package main

import "tetora/internal/discord"

func (db *DiscordBot) notifyChannelID() string {
	if len(db.cfg.Discord.ChannelIDs) > 0 {
		return db.cfg.Discord.ChannelIDs[0]
	}
	return db.cfg.Discord.ChannelID
}

func (db *DiscordBot) sendNotify(text string) {
	ch := db.notifyChannelID()
	if ch == "" {
		return
	}
	db.sendMessage(ch, text)
}

// discordPost delegates to the api client (kept for callers in other files).
func (db *DiscordBot) discordPost(path string, payload any) {
	db.api.Post(path, payload)
}

// discordRequestWithResponse delegates to the api client (kept for callers in other files).
func (db *DiscordBot) discordRequestWithResponse(method, path string, payload any) ([]byte, error) {
	return db.api.Request(method, path, payload)
}

// sendMessageReturningID sends a message and returns the message ID.
func (db *DiscordBot) sendMessageReturningID(channelID, content string) (string, error) {
	return db.api.SendMessageReturningID(channelID, content)
}

// editMessage edits an existing Discord message.
func (db *DiscordBot) editMessage(channelID, messageID, content string) error {
	return db.api.EditMessage(channelID, messageID, content)
}

// editMessageWithComponents edits an existing Discord message, replacing content and components.
func (db *DiscordBot) editMessageWithComponents(channelID, messageID, content string, components []discord.Component) error {
	return db.api.EditMessageWithComponents(channelID, messageID, content, components)
}

// deleteMessage deletes a Discord message.
func (db *DiscordBot) deleteMessage(channelID, messageID string) {
	db.api.DeleteMessage(channelID, messageID)
}

// sendMessageWithComponents sends a message with interactive components (buttons, selects, etc.).
func (db *DiscordBot) sendMessageWithComponents(channelID, content string, components []discord.Component) {
	db.api.SendMessageWithComponents(channelID, content, components)
}

// sendMessageWithComponentsReturningID sends a message with components and returns the message ID.
func (db *DiscordBot) sendMessageWithComponentsReturningID(channelID, content string, components []discord.Component) (string, error) {
	return db.api.SendMessageWithComponentsReturningID(channelID, content, components)
}

// sendEmbedWithComponents sends an embed message with interactive components.
func (db *DiscordBot) sendEmbedWithComponents(channelID string, embed discord.Embed, components []discord.Component) {
	db.api.SendEmbedWithComponents(channelID, embed, components)
}

func (db *DiscordBot) sendMessage(channelID, content string) {
	db.api.SendMessage(channelID, content)
}

func (db *DiscordBot) sendEmbed(channelID string, embed discord.Embed) {
	db.api.SendEmbed(channelID, embed)
}

func (db *DiscordBot) sendEmbedReply(channelID, replyToID string, embed discord.Embed) {
	db.api.SendEmbedReply(channelID, replyToID, embed)
}

func (db *DiscordBot) sendTyping(channelID string) {
	db.api.SendTyping(channelID)
}
