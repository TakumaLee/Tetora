package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tetoraConfig "tetora/internal/config"
	"tetora/internal/discord"
	"tetora/internal/provider"
)

func (db *DiscordBot) cmdModel(msg discord.Message, args string) {
	parts := strings.Fields(args)

	// !model → grouped status display
	if len(parts) == 0 {
		db.cmdModelStatus(msg)
		return
	}

	// !model pick [agent] → interactive picker
	if parts[0] == "pick" {
		agentName := ""
		if len(parts) > 1 {
			agentName = parts[1]
		}
		db.cmdModelPick(msg, agentName)
		return
	}

	// !model <model> [agent] → set model (auto-switches provider)
	model := parts[0]
	agentName := db.cfg.SmartDispatch.DefaultAgent
	if agentName == "" {
		agentName = "default"
	}
	if len(parts) > 1 {
		agentName = parts[1]
	}

	// Infer provider from model name and auto-create if needed.
	inferredProvider := ""
	if presetName, ok := provider.InferProviderFromModelWithPref(model, db.cfg.ClaudeProvider); ok {
		if err := ensureProvider(db.cfg, presetName); err != nil {
			db.sendMessage(msg.ChannelID, fmt.Sprintf("Warning: could not auto-create provider `%s`: %v", presetName, err))
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
					if err := ensureProvider(db.cfg, p.Name); err == nil {
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

	res, err := updateAgentModel(db.cfg, agentName, model, inferredProvider)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
		return
	}

	reply := fmt.Sprintf("**%s** model: `%s` → `%s`", agentName, res.OldModel, model)
	if res.NewProvider != "" {
		reply += fmt.Sprintf(" (provider: `%s` → `%s`)", res.OldProvider, res.NewProvider)
		// Auto-start new session when provider changes — old session IDs are invalid.
		db.autoNewSession(msg.ChannelID, res.OldProvider, res.NewProvider)
	}
	db.sendMessage(msg.ChannelID, reply)
}

// cmdModelStatus shows all agents grouped by Cloud / Local.
func (db *DiscordBot) cmdModelStatus(msg discord.Message) {
	type agentEntry struct {
		name     string
		model    string
		provider string
	}

	var cloudAgents, localAgents []agentEntry
	for name, ac := range db.cfg.Agents {
		m := ac.Model
		if m == "" {
			m = db.cfg.DefaultModel
		}
		p := ac.Provider
		if p == "" {
			p = db.cfg.DefaultProvider
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

	var fields []discord.EmbedField

	if len(cloudAgents) > 0 {
		var lines []string
		for _, a := range cloudAgents {
			lines = append(lines, fmt.Sprintf("`%s` — %s (%s)", a.name, a.model, a.provider))
		}
		fields = append(fields, discord.EmbedField{
			Name:  fmt.Sprintf("☁ Cloud (%d)", len(cloudAgents)),
			Value: strings.Join(lines, "\n"),
		})
	}

	if len(localAgents) > 0 {
		var lines []string
		for _, a := range localAgents {
			lines = append(lines, fmt.Sprintf("`%s` — %s (%s)", a.name, a.model, a.provider))
		}
		fields = append(fields, discord.EmbedField{
			Name:  fmt.Sprintf("🏠 Local (%d)", len(localAgents)),
			Value: strings.Join(lines, "\n"),
		})
	}

	mode := db.cfg.InferenceMode
	if mode == "" {
		mode = "mixed"
	}

	suffix := fmt.Sprintf("_%s_%d", msg.ChannelID, time.Now().UnixMilli())
	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title:  "Agent Models",
		Color:  0x5865F2,
		Fields: fields,
		Footer: &discord.EmbedFooter{Text: fmt.Sprintf("Mode: %s | !model pick [agent] | !local | !cloud", mode)},
	}, []discord.Component{
		discordActionRow(
			discordButton("model_pick_start"+suffix, "Pick Model", discord.ButtonStylePrimary),
			discordButton("mode_local"+suffix, "Switch All Local", discord.ButtonStyleSuccess),
			discordButton("mode_cloud"+suffix, "Switch All Cloud", discord.ButtonStyleSecondary),
		),
	})

	// Register button callbacks.
	db.interactions.register(&pendingInteraction{
		CustomID:  "model_pick_start" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Reusable:  true,
		Callback: func(data discord.InteractionData) {
			db.cmdModelPick(msg, "")
		},
	})
	db.interactions.register(&pendingInteraction{
		CustomID:  "mode_local" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdLocal(msg, "")
		},
	})
	db.interactions.register(&pendingInteraction{
		CustomID:  "mode_cloud" + suffix,
		ChannelID: msg.ChannelID,
		UserID:    msg.Author.ID,
		CreatedAt: time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdCloud(msg, "")
		},
	})
}

// cmdModelPick starts an interactive model picker flow.
func (db *DiscordBot) cmdModelPick(msg discord.Message, agentName string) {
	// Step 1: If no agent specified, show agent select menu.
	if agentName == "" {
		var options []discord.SelectOption
		// Sort agent names for consistent display.
		names := make([]string, 0, len(db.cfg.Agents))
		for name := range db.cfg.Agents {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			ac := db.cfg.Agents[name]
			m := ac.Model
			if m == "" {
				m = db.cfg.DefaultModel
			}
			p := ac.Provider
			if p == "" {
				p = db.cfg.DefaultProvider
			}
			desc := fmt.Sprintf("%s (%s)", m, p)
			if len(desc) > 100 {
				desc = desc[:100]
			}
			options = append(options, discord.SelectOption{
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
		db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
			Title: "Pick Model — Select Agent",
			Color: 0x5865F2,
		}, []discord.Component{
			discordActionRow(discordSelectMenu(customID, "Select an agent...", options)),
		})

		db.interactions.register(&pendingInteraction{
			CustomID:   customID,
			ChannelID:  msg.ChannelID,
			UserID:     msg.Author.ID,
			CreatedAt:  time.Now(),
			AllowedIDs: []string{msg.Author.ID},
			Callback: func(data discord.InteractionData) {
				if len(data.Values) > 0 {
					db.cmdModelPickProvider(msg, data.Values[0])
				}
			},
		})
		return
	}

	// Agent specified — go directly to provider selection.
	if _, ok := db.cfg.Agents[agentName]; !ok {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found.", agentName))
		return
	}
	db.cmdModelPickProvider(msg, agentName)
}

// cmdModelPickProvider shows provider selection buttons for an agent.
func (db *DiscordBot) cmdModelPickProvider(msg discord.Message, agentName string) {
	ts := fmt.Sprintf("%d", time.Now().UnixMilli())

	var buttons []discord.Component
	for _, preset := range provider.Presets {
		if preset.Name == "custom" {
			continue
		}
		customID := fmt.Sprintf("model_pick_prov_%s_%s_%s", agentName, preset.Name, ts)
		style := discord.ButtonStyleSecondary
		if provider.IsLocalProvider(preset.Name) {
			style = discord.ButtonStyleSuccess // green for local
		}
		buttons = append(buttons, discordButton(customID, preset.DisplayName, style))

		presetName := preset.Name // capture
		db.interactions.register(&pendingInteraction{
			CustomID:   customID,
			ChannelID:  msg.ChannelID,
			UserID:     msg.Author.ID,
			CreatedAt:  time.Now(),
			AllowedIDs: []string{msg.Author.ID},
			Callback: func(data discord.InteractionData) {
				db.cmdModelPickModel(msg, agentName, presetName)
			},
		})
	}

	// Discord allows max 5 buttons per action row.
	var rows []discord.Component
	for i := 0; i < len(buttons); i += 5 {
		end := i + 5
		if end > len(buttons) {
			end = len(buttons)
		}
		rows = append(rows, discordActionRow(buttons[i:end]...))
	}

	ac := db.cfg.Agents[agentName]
	currentModel := ac.Model
	if currentModel == "" {
		currentModel = db.cfg.DefaultModel
	}

	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title:       fmt.Sprintf("Pick Model — %s", agentName),
		Description: fmt.Sprintf("Current: `%s`\nSelect a provider:", currentModel),
		Color:       0x5865F2,
	}, rows)
}

// cmdModelPickModel shows model selection for a specific agent + provider.
func (db *DiscordBot) cmdModelPickModel(msg discord.Message, agentName, presetName string) {
	preset, ok := provider.GetPreset(presetName)
	if !ok {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Preset `%s` not found.", presetName))
		return
	}

	// Fetch available models.
	models, err := provider.FetchPresetModels(preset)
	if err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Cannot fetch models from `%s`: %v", presetName, err))
		return
	}
	if len(models) == 0 {
		models = preset.Models
	}
	if len(models) == 0 {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("No models available for `%s`.", preset.DisplayName))
		return
	}

	// Build select menu options.
	var options []discord.SelectOption
	for _, m := range models {
		if len(options) >= 25 {
			break
		}
		options = append(options, discord.SelectOption{
			Label: m,
			Value: m,
		})
	}

	customID := fmt.Sprintf("model_pick_model_%s_%s_%d", agentName, presetName, time.Now().UnixMilli())
	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title:       fmt.Sprintf("Pick Model — %s — %s", agentName, preset.DisplayName),
		Description: "Select a model:",
		Color:       0x5865F2,
	}, []discord.Component{
		discordActionRow(discordSelectMenu(customID, "Select model...", options)),
	})

	db.interactions.register(&pendingInteraction{
		CustomID:   customID,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			if len(data.Values) == 0 {
				return
			}
			selectedModel := data.Values[0]

			// Auto-create provider if needed.
			if err := ensureProvider(db.cfg, presetName); err != nil {
				db.sendMessage(msg.ChannelID, fmt.Sprintf("Warning: could not auto-create provider `%s`: %v", presetName, err))
			}

			res, err := updateAgentModel(db.cfg, agentName, selectedModel, presetName)
			if err != nil {
				db.sendMessage(msg.ChannelID, fmt.Sprintf("Error: %v", err))
				return
			}

			// Auto-start new session when provider changes.
			if res.NewProvider != "" {
				db.autoNewSession(msg.ChannelID, res.OldProvider, res.NewProvider)
			}

			db.sendEmbed(msg.ChannelID, discord.Embed{
				Title: "Model Updated",
				Color: 0x57F287, // green
				Fields: []discord.EmbedField{
					{Name: "Agent", Value: agentName, Inline: true},
					{Name: "Model", Value: fmt.Sprintf("`%s` → `%s`", res.OldModel, selectedModel), Inline: true},
					{Name: "Provider", Value: fmt.Sprintf("`%s` → `%s`", res.OldProvider, presetName), Inline: true},
				},
			})
		},
	})
}

// cmdLocal switches agents to local models (Ollama).
func (db *DiscordBot) cmdLocal(msg discord.Message, args string) {
	// Check Ollama is reachable.
	ollamaPreset, _ := provider.GetPreset("ollama")
	ollamaModels, err := provider.FetchPresetModels(ollamaPreset)
	if err != nil || len(ollamaModels) == 0 {
		db.sendMessage(msg.ChannelID, "Ollama is not reachable or has no models.\nStart it with: `ollama serve`")
		return
	}

	// Ensure ollama provider exists.
	if err := ensureProvider(db.cfg, "ollama"); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error creating ollama provider: %v", err))
		return
	}

	target := strings.TrimSpace(args)
	switched := 0
	pinned := 0
	updatedAgents := make(map[string]tetoraConfig.AgentConfig)

	for name, ac := range db.cfg.Agents {
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
		db.cfg.Agents[name] = ac
		updatedAgents[name] = ac
		switched++
	}

	if switched == 0 && target != "" {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found or already on local.", target))
		return
	}

	// Persist.
	configPath := findConfigPath()
	if err := tetoraConfig.SaveInferenceMode(configPath, "local", updatedAgents); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error saving config: %v", err))
		return
	}
	db.cfg.InferenceMode = "local"

	desc := fmt.Sprintf("Switched **%d** agents to local (Ollama)\nUsing model: `%s`", switched, ollamaModels[0])
	if pinned > 0 {
		desc += fmt.Sprintf("\n%d agents pinned (unchanged)", pinned)
	}

	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       "🏠 Local Mode",
		Description: desc,
		Color:       0x57F287,
	})
}

// cmdCloud switches agents back to cloud models.
func (db *DiscordBot) cmdCloud(msg discord.Message, args string) {
	target := strings.TrimSpace(args)
	switched := 0
	pinned := 0
	updatedAgents := make(map[string]tetoraConfig.AgentConfig)

	for name, ac := range db.cfg.Agents {
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
			if preset, ok := provider.InferProviderFromModelWithPref(ac.CloudModel, db.cfg.ClaudeProvider); ok {
				ac.Provider = preset
			} else {
				ac.Provider = db.cfg.DefaultProvider
			}
		} else {
			ac.Model = db.cfg.DefaultModel
			ac.Provider = db.cfg.DefaultProvider
		}
		db.cfg.Agents[name] = ac
		updatedAgents[name] = ac
		switched++
	}

	if switched == 0 && target != "" {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Agent `%s` not found or already on cloud.", target))
		return
	}

	configPath := findConfigPath()
	if err := tetoraConfig.SaveInferenceMode(configPath, "", updatedAgents); err != nil {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Error saving config: %v", err))
		return
	}
	db.cfg.InferenceMode = ""

	desc := fmt.Sprintf("Restored **%d** agents to cloud models", switched)
	if pinned > 0 {
		desc += fmt.Sprintf("\n%d agents pinned (unchanged)", pinned)
	}

	db.sendEmbed(msg.ChannelID, discord.Embed{
		Title:       "☁ Cloud Mode",
		Description: desc,
		Color:       0x5865F2,
	})
}

// cmdMode shows current inference mode summary.
func (db *DiscordBot) cmdMode(msg discord.Message) {
	cloud, local := 0, 0
	for _, ac := range db.cfg.Agents {
		p := ac.Provider
		if p == "" {
			p = db.cfg.DefaultProvider
		}
		if provider.IsLocalProvider(p) {
			local++
		} else {
			cloud++
		}
	}

	mode := db.cfg.InferenceMode
	if mode == "" {
		mode = "mixed"
	}

	modeSuffix := fmt.Sprintf("_%s_%d", msg.ChannelID, time.Now().UnixMilli())
	db.sendEmbedWithComponents(msg.ChannelID, discord.Embed{
		Title: "Inference Mode",
		Color: 0x5865F2,
		Fields: []discord.EmbedField{
			{Name: "Mode", Value: mode, Inline: true},
			{Name: "Cloud", Value: fmt.Sprintf("%d agents", cloud), Inline: true},
			{Name: "Local", Value: fmt.Sprintf("%d agents", local), Inline: true},
		},
	}, []discord.Component{
		discordActionRow(
			discordButton("mode_local_cmd"+modeSuffix, "Switch All Local", discord.ButtonStyleSuccess),
			discordButton("mode_cloud_cmd"+modeSuffix, "Switch All Cloud", discord.ButtonStyleSecondary),
		),
	})

	db.interactions.register(&pendingInteraction{
		CustomID:   "mode_local_cmd" + modeSuffix,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdLocal(msg, "")
		},
	})
	db.interactions.register(&pendingInteraction{
		CustomID:   "mode_cloud_cmd" + modeSuffix,
		ChannelID:  msg.ChannelID,
		UserID:     msg.Author.ID,
		CreatedAt:  time.Now(),
		AllowedIDs: []string{msg.Author.ID},
		Callback: func(data discord.InteractionData) {
			db.cmdCloud(msg, "")
		},
	})
}

func (db *DiscordBot) cmdCancel(msg discord.Message) {
	if db.state == nil {
		db.sendMessage(msg.ChannelID, "No dispatch state.")
		return
	}
	db.state.mu.Lock()
	count := 0
	for _, ts := range db.state.running {
		if ts.cancelFn != nil {
			ts.cancelFn()
			count++
		}
	}
	cancelFn := db.state.cancel
	db.state.mu.Unlock()
	if cancelFn != nil {
		cancelFn()
		count++
	}
	if count == 0 {
		db.sendMessage(msg.ChannelID, "Nothing running to cancel.")
	} else {
		db.sendMessage(msg.ChannelID, fmt.Sprintf("Cancelling %d task(s).", count))
	}
}
