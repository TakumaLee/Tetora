package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tetora/internal/discord"
	"tetora/internal/history"
	"tetora/internal/log"
)

func (db *DiscordBot) sendRouteResponse(channelID string, route *RouteResult, result TaskResult, task Task, skipOutput bool, replyMsgID string) {
	color := 0x57F287
	if result.Status != "success" {
		color = 0xED4245
	}

	if !skipOutput {
		output := result.Output
		if result.Status != "success" {
			output = result.Error
			if output == "" {
				output = result.Status
			}
		}
		// Fallback for empty/whitespace output on success (e.g. tool-only responses).
		if strings.TrimSpace(output) == "" && result.Status == "success" {
			parts := []string{"Task completed successfully."}
			if result.TokensIn > 0 || result.TokensOut > 0 {
				parts = append(parts, fmt.Sprintf("Tokens: %d in / %d out", result.TokensIn, result.TokensOut))
			}
			if result.OutputFile != "" {
				parts = append(parts, fmt.Sprintf("Output saved: `%s`", result.OutputFile))
			}
			output = strings.Join(parts, "\n")
		}

		// Persist full output to disk before sending, so it can be retrieved
		// even if Discord drops or truncates the message.
		if result.Status == "success" && strings.TrimSpace(output) != "" && db.cfg.BaseDir != "" {
			outDir := filepath.Join(db.cfg.BaseDir, "outputs")
			if err := os.MkdirAll(outDir, 0o755); err == nil {
				outPath := filepath.Join(outDir,
					fmt.Sprintf("%s_%s.txt", task.ID, time.Now().Format("20060102-150405")))
				if err := os.WriteFile(outPath, []byte(output), 0o644); err != nil {
					log.Warn("discord: failed to save output file", "task", task.ID, "err", err)
				}
			}
		}

		// Send output as plain text messages (split into 2000-char chunks).
		// For very long outputs, truncate the middle to preserve the conclusion.
		const maxChunk = 1900 // leave room for markdown formatting
		const maxTotal = 5700 // 3 messages max — Discord rate-limits beyond this
		if len(output) > maxTotal {
			// Keep beginning (context) + end (conclusion), separated by "...".
			headSize := maxTotal * 2 / 5
			tailSize := maxTotal * 3 / 5
			// Find clean break points at newlines.
			if idx := strings.LastIndex(output[:headSize], "\n"); idx > headSize/2 {
				headSize = idx
			}
			tailStart := len(output) - tailSize
			if idx := strings.Index(output[tailStart:], "\n"); idx >= 0 && idx < tailSize/3 {
				tailStart += idx + 1
			}
			output = output[:headSize] + "\n\n... (truncated) ...\n\n" + output[tailStart:]
		}
		chunkCount := 0
		for len(output) > 0 {
			chunk := output
			if len(chunk) > maxChunk {
				// Try to split at a newline boundary.
				cut := maxChunk
				if idx := strings.LastIndex(chunk[:maxChunk], "\n"); idx > maxChunk/2 {
					cut = idx + 1
				}
				chunk = output[:cut]
				output = output[cut:]
			} else {
				output = ""
			}
			if chunkCount > 0 {
				time.Sleep(300 * time.Millisecond) // avoid Discord rate limiting
			}
			db.sendMessage(channelID, chunk)
			chunkCount++
		}
	}

	// Query today's cumulative token usage (this task already recorded before this call).
	todayIn, todayOut := history.TodayTotalTokens(db.cfg.HistoryDB)

	// Send metadata as a small embed at the end, as a reply to the original message.
	db.sendEmbedReply(channelID, replyMsgID, discord.Embed{
		Color: color,
		Fields: []discord.EmbedField{
			{Name: "Agent", Value: fmt.Sprintf("%s (%s)", route.Agent, route.Method), Inline: true},
			{Name: "Status", Value: result.Status, Inline: true},
			{Name: "Cost", Value: fmt.Sprintf("$%.4f", result.CostUSD), Inline: true},
			{Name: "Duration", Value: formatDurationMs(result.DurationMs), Inline: true},
			{Name: "今日 Token", Value: formatTokenField(todayIn, todayOut, db.cfg.CostAlert.DailyTokenLimit), Inline: true},
		},
		Footer:    &discord.EmbedFooter{Text: fmt.Sprintf("Task: %s", task.ID[:8])},
		Timestamp: time.Now().Format(time.RFC3339),
	})
}
