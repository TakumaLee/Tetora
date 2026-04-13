package discord

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tetoraConfig "tetora/internal/config"
	"tetora/internal/provider"
)

// findConfigPath returns the path to config.json, checking standard locations.
func findConfigPath() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "..", "config.json")
		if abs, err := filepath.Abs(candidate); err == nil {
			if _, err := os.Stat(abs); err == nil {
				return abs
			}
		}
	}
	home, _ := os.UserHomeDir()
	candidate := filepath.Join(home, ".tetora", "config.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return "config.json"
}

func (b *Bot) cmdModel(msg Message, args string) {
	parts := strings.Fields(args)

	// !model → grouped status display
	if len(parts) == 0 {
		b.cmdModelStatus(msg)
		return
	}

	// !model pick [agent] → interactive picker
	if parts[0] == "pick" {
		agentName := ""
		if len(parts) > 1 {
			agentName = parts[1]
		}
		b.cmdModelPick(msg, agentName)
		return
	}

	// !model <model> [agent] → set model (auto-switches provider)
	model := parts[0]
	agentName := b.cfg.SmartDispatch.DefaultAgent
	if agentName == "" {
		agentName = "default"
	}
	if len(parts) > 1 {
		agentName = parts[1]
	}

	// Infer provider from model name and auto-create if needed.
	inferredProvider := ""
	if presetName, ok := provider.InferProviderFromModelWithPref(model, b.cfg.ClaudeProvider); ok {
		if err := b.deps.EnsureProvider(presetName); err != nil {
			b.sendMessage(msg.ChannelID, fmt.Sprintf("Warning: could not auto-create provider `%s`: %v", presetName, err))
		} else {
			inferredProvider = presetName
		}
	}
	// If prefix inference failed, check dynamic providers (Ollama, LM Studio).
	if inferredProvider == "" {
		for _, p := range provider.Presets {
			if !p.Dynamic {
				continue
			}
			models, err := provider.FetchPresetModels(p)
			if err != nil {
				continue
			}
			for _, m := range models {
				// Match exact name or name without tag (e.g. "dolphin-mistral" matches "dolphin-mistral:latest").
				if m == model || strings.TrimSuffix(m, ":latest") == model {
					if err := b.deps.EnsureProvider(p.Name); err == nil {
						inferredProvider = p.Name
					}
					break
				}
			}
			if inferredProvider != "" {
				break
			}
		}
	}

	res, err := b.deps.UpdateAgentModel(agentName, model, inferredProvider)
	if err != nil {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}

	reply := fmt.Sprintf("**%s** model: `%s` → `%s`", agentName, res.OldModel, model)
	if res.NewProvider != "" {
		reply += fmt.Sprintf(" (provider: `%s` → `%s`)", res.OldProvider, res.NewProvider)
		// Auto-start new session when provider changes — old session IDs are invalid.
		b.autoNewSession(msg.ChannelID, res.OldProvider, res.NewProvider)
	}
	b.sendMessage(msg.ChannelID, reply)
}

// cmdModelStatus shows all agents grouped by Cloud / Local.
func (b *Bot) cmdModelStatus(msg Message) {
	type agentEntry struct {
		name     string
		model    string
		provider string
	}

	var cloudAgents, localAgents []agentEntry
	for name, ac := range b.cfg.Agents {
		m := ac.Model
		if m == "" {
			m = b.cfg.DefaultModel
		}
		p := ac.Provider
		if p == "" {
			p = b.cfg.DefaultProvider
		}
		if p == "" {
			p = "claude"
		}
		entry := agentEntry{name: name, model: m, provider: p}
		if provider.IsLocalProvider(p) {
			localAgents = append(localAgents, entry)
		} else {
			cloudAgents = append(cloudAgents, entry)
		}
	}

	sort.Slice(cloudAgents, func(i, j int) bool { return cloudAgents[i].name < cloudAgents[j].name })
	sort.Slice(localAgents, func(i, j int) bool { return localAgents[i].name < localAgents[j].name })

	var fields []EmbedField

	if len(cloudAgents) > 0 {
		var lines []string
		for _, a := range cloudAgents {
			lines = append(lines, fmt.Sprintf("`%s` — %s (%s)", a.name, a.model, a.provider))
		}
		fields = append(fields, EmbedField{
			Name:  fmt.Sprintf("☁ Cloud (%d)", len(cloudAgents)),
			Value: strings.Join(lines, "\n"),
		})
	}

	if len(localAgents) > 0 {
		var lines []string
		for _, a := range localAgents {
			lines = append(lines, fmt.Sprintf("`%s` — %s (%s)", a.name, a.model, a.provider))
		}
		fields = append(fields, EmbedField{
			Name:  fmt.Sprintf("🏠 Local (%d)", len(localAgents)),
			Value: strings.Join(lines, "\n"),
		})
	}

	mode := b.cfg.InferenceMode
	if mode == "" {
		mode = "mixed"
	}

	suffix := fmt.Sprintf("_%s_%d", msg.ChannelID, time.Now().UnixMilli())
	b.sendEmbedWithComponents(msg.ChannelID, Embed{
		Title:  "Agent Models",
		Color:  0x5865F2,
		Fields: fields,
		Footer: &EmbedFooter{Text: fmt.Sprintf("Mode: %s | !model pick [agent] | !local | !cloud", mode)},
	}, []Component{
		discordActionRow(
			discordButton("model_pick_start"+suffix, "Pick Model", ButtonStylePrimary),
			discordButton("mode_local"+suffix, "Switch All Local", ButtonStyleSuccess),
			discordButton("mode_cloud"+suffix, "Switch All Cloud", ButtonStyleSecondary),
		),
	})

	// Register button callbacks.
	b.interactions.register(&pendingInteraction{
		CustomID:  "model_pick_start" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Reusable:  true,
		Callback: func(data InteractionData) {
			b.cmdModelPick(msg, "")
		},
	})
	b.interactions.register(&pendingInteraction{
		CustomID:  "mode_local" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data InteractionData) {
			b.cmdLocal(msg, "")
		},
	})
	b.interactions.register(&pendingInteraction{
		CustomID:  "mode_cloud" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data InteractionData) {
			b.cmdCloud(msg, "")
		},
	})
}

// cmdModelPick starts an interactive model picker flow.
func (b *Bot) cmdModelPick(msg Message, agentName string) {
	// Step 1: If no agent specified, show agent select menu.
	if agentName == "" {
		var options []SelectOption
		// Sort agent names for consistent display.
		names := make([]string, 0, len(b.cfg.Agents))
		for name := range b.cfg.Agents {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			ac := b.cfg.Agents[name]
			m := ac.Model
			if m == "" {
				m = b.cfg.DefaultModel
			}
			p := ac.Provider
			if p == "" {
				p = b.cfg.DefaultProvider
			}
			desc := fmt.Sprintf("%s (%s)", m, p)
			if len(desc) > 100 {
				desc = desc[:100]
			}
			options = append(options, SelectOption{
				Label:       name,
				Value:       name,
				Description: desc,
			})
		}

		// Discord limits to 25 options.
		if len(options) > 25 {
			options = options[:25]
		}

		customID := fmt.Sprintf("model_pick_agent_%d", time.Now().UnixMilli())
		b.sendEmbedWithComponents(msg.ChannelID, Embed{
			Title: "Pick Model — Select Agent",
			Color: 0x5865F2,
		}, []Component{
			discordActionRow(discordSelectMenu(customID, "Select an agent...", options)),
		})

		b.interactions.register(&pendingInteraction{
			CustomID:   customID,
			ChannelID:  msg.ChannelID,
			UserID:     msg.Author.ID,
			CreatedAt:  time.Now(),
			AllowedIDs: []string{msg.Author.ID},
			Callback: func(data InteractionData) {
				if len(data.Values) > 0 {
					b.cmdModelPickProvider(msg, data.Values[0])
				}
			},
		})
		return
	}

	// Agent specified — go directly to provider selection.
	if _, ok := b.cfg.Agents[agentName]; !ok {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found.", agentName))
		return
	}
	b.cmdModelPickProvider(msg, agentName)
}

// cmdModelPickProvider shows provider selection buttons for an agent.
func (b *Bot) cmdModelPickProvider(msg Message, agentName string) {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())

	var buttons []Component
	for _, preset := range provider.Presets {
		if preset.Name == "custom" {
			continue
		}
		customID := fmt.Sprintf("model_pick_prov_%s_%s_%s", agentName, preset.Name, ts)
		style := ButtonStyleSecondary
		if provider.IsLocalProvider(preset.Name) {
			style = ButtonStyleSuccess // green for local
		}
		buttons = append(buttons, discordButton(customID, preset.DisplayName, style))

		presetName := preset.Name // capture
		b.interactions.register(&pendingInteraction{
			CustomID:   customID,
			ChannelID:  msg.ChannelID,
			UserID:     msg.Author.ID,
			CreatedAt:  time.Now(),
			AllowedIDs: []string{msg.Author.ID},
			Callback: func(data InteractionData) {
				b.cmdModelPickModel(msg, agentName, presetName)
			},
		})
	}

	// Discord allows max 5 buttons per action row.
	var rows []Component
	for i := 0; i < len(buttons); i += 5 {
		end := i + 5
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, discordActionRow(buttons[i:end]...))
	}

	ac := b.cfg.Agents[agentName]
	currentModel := ac.Model
	if currentModel == "" {
		currentModel = b.cfg.DefaultModel
	}

	b.sendEmbedWithComponents(msg.ChannelID, Embed{
		Title:       fmt.Sprintf("Pick Model — %s", agentName),
		Description: fmt.Sprintf("Current: `%s`\nSelect a provider:", currentModel),
		Color:       0x5865F2,
	}, rows)
}

// cmdModelPickModel shows model selection for a specific agent + provider.
func (b *Bot) cmdModelPickModel(msg Message, agentName, presetName string) {
	preset, ok := provider.GetPreset(presetName)
	if !ok {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Preset `%s` not found.", presetName))
		return
	}

	// Fetch available models.
	models, err := provider.FetchPresetModels(preset)
	if err != nil {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Cannot fetch models from `%s`: %v", presetName, err))
		return
	}
	if len(models) == 0 {
		models = preset.Models
	}
	if len(models) == 0 {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("No models available for `%s`.", preset.DisplayName))
		return
	}

	// Build select menu options.
	var options []SelectOption
	for _, m := range models {
		if len(options) >= 25 {
			break
		}
		options = append(options, SelectOption{
			Label: m,
			Value: m,
		})
	}

	customID := fmt.Sprintf("model_pick_model_%s_%s_%d", agentName, presetName, time.Now().UnixMilli())
	b.sendEmbedWithComponents(msg.ChannelID, Embed{
		Title:       fmt.Sprintf("Pick Model — %s — %s", agentName, preset.DisplayName),
		Description: "Select a model:",
		Color:       0x5865F2,
	}, []Component{
		discordActionRow(discordSelectMenu(customID, "Select model...", options)),
	})

	b.interactions.register(&pendingInteraction{
		CustomID:   customID,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data InteractionData) {
			if len(data.Values) == 0 {
				return
			}
			selectedModel := data.Values[0]

			// Auto-create provider if needed.
			if err := b.deps.EnsureProvider(presetName); err != nil {
				b.sendMessage(msg.ChannelID, fmt.Sprintf("Warning: could not auto-create provider `%s`: %v", presetName, err))
			}

			res, err := b.deps.UpdateAgentModel(agentName, selectedModel, presetName)
			if err != nil {
				b.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
				return
			}

			// Auto-start new session when provider changes.
			if res.NewProvider != "" {
				b.autoNewSession(msg.ChannelID, res.OldProvider, res.NewProvider)
			}

			b.sendEmbed(msg.ChannelID, Embed{
				Title: "Model Updated",
				Color: 0x57F287, // green
				Fields: []EmbedField{
					{Name: "Agent", Value: agentName, Inline: true},
					{Name: "Model", Value: fmt.Sprintf("`%s` → `%s`", res.OldModel, selectedModel), Inline: true},
					{Name: "Provider", Value: fmt.Sprintf("`%s` → `%s`", res.OldProvider, presetName), Inline: true},
				},
			})
		},
	})
}

// cmdLocal switches agents to local models (Ollama).
func (b *Bot) cmdLocal(msg Message, args string) {
	// Check Ollama is reachable.
	ollamaPreset, _ := provider.GetPreset("ollama")
	ollamaModels, err := provider.FetchPresetModels(ollamaPreset)
	if err != nil || len(ollamaModels) == 0 {
		b.sendMessage(msg.ChannelID, "Ollama is not reachable or has no models.\nStart it with: `ollama serve`")
		return
	}

	// Ensure ollama provider exists.
	if err := b.deps.EnsureProvider("ollama"); err != nil {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Error creating ollama provider: %v", err))
		return
	}

	target := strings.TrimSpace(args)
	switched := 0
	pinned := 0
	updatedAgents := make(map[string]tetoraConfig.AgentConfig)

	for name, ac := range b.cfg.Agents {
		if target != "" && name != target {
			continue
		}
		if ac.PinMode != "" {
			pinned++
			continue
		}
		if provider.IsLocalProvider(ac.Provider) {
			continue // already local
		}
		ac.CloudModel = ac.Model
		if ac.LocalModel != "" {
			ac.Model = ac.LocalModel
		} else {
			ac.Model = ollamaModels[0]
		}
		ac.Provider = "ollama"
		b.cfg.Agents[name] = ac
		updatedAgents[name] = ac
		switched++
	}

	if switched == 0 && target != "" {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found or already on local.", target))
		return
	}

	// Persist.
	configPath := findConfigPath()
	if err := tetoraConfig.SaveInferenceMode(configPath, "local", updatedAgents); err != nil {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Error saving config: %v", err))
		return
	}
	b.cfg.InferenceMode = "local"

	desc := fmt.Sprintf("Switched **%d** agents to local (Ollama)\nUsing model: `%s`", switched, ollamaModels[0])
	if pinned > 0 {
		desc += fmt.Sprintf("\n%d agents pinned (unchanged)", pinned)
	}

	b.sendEmbed(msg.ChannelID, Embed{
		Title:       "🏠 Local Mode",
		Description: desc,
		Color:       0x57F287,
	})
}

// cmdCloud switches agents back to cloud models.
func (b *Bot) cmdCloud(msg Message, args string) {
	target := strings.TrimSpace(args)
	switched := 0
	pinned := 0
	updatedAgents := make(map[string]tetoraConfig.AgentConfig)

	for name, ac := range b.cfg.Agents {
		if target != "" && name != target {
			continue
		}
		if ac.PinMode != "" {
			pinned++
			continue
		}
		if !provider.IsLocalProvider(ac.Provider) {
			continue // already on cloud
		}
		if ac.CloudModel != "" {
			ac.Model = ac.CloudModel
			if preset, ok := provider.InferProviderFromModelWithPref(ac.CloudModel, b.cfg.ClaudeProvider); ok {
				ac.Provider = preset
			} else {
				ac.Provider = b.cfg.DefaultProvider
			}
		} else {
			ac.Model = b.cfg.DefaultModel
			ac.Provider = b.cfg.DefaultProvider
		}
		b.cfg.Agents[name] = ac
		updatedAgents[name] = ac
		switched++
	}

	if switched == 0 && target != "" {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found or already on cloud.", target))
		return
	}

	configPath := findConfigPath()
	if err := tetoraConfig.SaveInferenceMode(configPath, "", updatedAgents); err != nil {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Error saving config: %v", err))
		return
	}
	b.cfg.InferenceMode = ""

	desc := fmt.Sprintf("Restored **%d** agents to cloud models", switched)
	if pinned > 0 {
		desc += fmt.Sprintf("\n%d agents pinned (unchanged)", pinned)
	}

	b.sendEmbed(msg.ChannelID, Embed{
		Title:       "☁ Cloud Mode",
		Description: desc,
		Color:       0x5865F2,
	})
}

// cmdMode shows current inference mode summary.
func (b *Bot) cmdMode(msg Message) {
	cloud, local := 0, 0
	for _, ac := range b.cfg.Agents {
		p := ac.Provider
		if p == "" {
			p = b.cfg.DefaultProvider
		}
		if provider.IsLocalProvider(p) {
			local++
		} else {
			cloud++
		}
	}

	mode := b.cfg.InferenceMode
	if mode == "" {
		mode = "mixed"
	}

	modeSuffix := fmt.Sprintf("_%s_%d", msg.ChannelID, time.Now().UnixMilli())
	b.sendEmbedWithComponents(msg.ChannelID, Embed{
		Title: "Inference Mode",
		Color: 0x5865F2,
		Fields: []EmbedField{
			{Name: "Mode", Value: mode, Inline: true},
			{Name: "Cloud", Value: fmt.Sprintf("%d agents", cloud), Inline: true},
			{Name: "Local", Value: fmt.Sprintf("%d agents", local), Inline: true},
		},
	}, []Component{
		discordActionRow(
			discordButton("mode_local_cmd"+modeSuffix, "Switch All Local", ButtonStyleSuccess),
			discordButton("mode_cloud_cmd"+modeSuffix, "Switch All Cloud", ButtonStyleSecondary),
		),
	})

	b.interactions.register(&pendingInteraction{
		CustomID:   "mode_local_cmd" + modeSuffix,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data InteractionData) {
			b.cmdLocal(msg, "")
		},
	})
	b.interactions.register(&pendingInteraction{
		CustomID:   "mode_cloud_cmd" + modeSuffix,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data InteractionData) {
			b.cmdCloud(msg, "")
		},
	})
}

func (b *Bot) cmdCancel(msg Message) {
	if b.state == nil {
		b.sendMessage(msg.ChannelID, "No dispatch state.")
		return
	}
	count := b.state.RunningCount()
	b.state.CancelAll()
	if count == 0 {
		b.sendMessage(msg.ChannelID, "Nothing running to cancel.")
	} else {
		b.sendMessage(msg.ChannelID, fmt.Sprintf("Cancelling %d task(s).", count))
	}
}
