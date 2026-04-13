package discord

import (
	"context"
	"sync"
	"time"

	"tetora/internal/config"
	dtypes "tetora/internal/dispatch"
)

// BotDeps holds injected function dependencies that the Bot needs from the main package.
// These are functions defined in wire.go / dispatch.go that haven't been moved to
// internal packages yet. Injected as closures by newDiscordBot in root discord.go.
type BotDeps struct {
	// RouteTask routes an incoming prompt to the appropriate agent.
	RouteTask func(ctx context.Context, req dtypes.RouteRequest) *dtypes.RouteResult

	// ExpandPrompt expands template variables in a prompt string.
	ExpandPrompt func(prompt, jobID, dbPath, agentName, knowledgeDir string) string

	// RecordHistory persists a completed task run to the history database.
	RecordHistory func(dbPath, jobID, name, source, role string,
		task dtypes.Task, result dtypes.TaskResult,
		startedAt, finishedAt, outputFile string)

	// MaybeCompactSession triggers session compaction when thresholds are exceeded.
	MaybeCompactSession func(dbPath, sessionID string, msgCount, tokensIn int, sem, childSem chan struct{})

	// ResolveProviderName resolves the effective provider name for a task.
	ResolveProviderName func(task dtypes.Task, agentName string) string
}

// Bot manages the Discord Gateway connection and all message handling.
// Migrated from the main-package DiscordBot struct.
//
// Field types that were previously main-package-only types are now in this package:
//   - *discordInteractionState  → defined in interactions.go
//   - *threadBindingStore       → defined in threads.go
//   - *threadParentCache        → defined in threads_cache.go
//   - *discordApprovalGate      → defined in approval.go
//   - *terminalBridge           → defined in terminal.go
//
// Dependencies on main-package types are abstracted via:
//   - StateAccessor interface   → defined in contracts.go
//   - BotDeps struct            → above, injected on construction
//   - *config.Config            → tetora/internal/config (already internal)
//   - *cron.Engine              → tetora/internal/cron (already internal)
type Bot struct {
	cfg      *config.Config
	state    StateAccessor
	sem      chan struct{}
	childSem chan struct{}
	deps     BotDeps

	botUserID string
	sessionID string
	seq       int
	seqMu     sync.Mutex

	api          *Client
	stopCh       chan struct{}
	interactions *discordInteractionState
	threads      *threadBindingStore
	threadParents *threadParentCache
	reactions    *ReactionManager
	approvalGate *discordApprovalGate
	forumBoard   *ForumBoard
	voice        *VoiceManager
	gatewayConn  *WsConn
	notifier     *TaskNotifier
	terminal     *terminalBridge
	msgSem       chan struct{}

	dedupMu   sync.Mutex
	dedupRing [128]string
	dedupIdx  int

	chatLock   map[string]string
	chatLockMu sync.RWMutex
}

// New creates a new Bot with the given dependencies.
// Called from root discord.go's newDiscordBot.
func New(
	cfg *config.Config,
	state StateAccessor,
	sem, childSem chan struct{},
	deps BotDeps,
) *Bot {
	apiClient := NewClient(cfg.Discord.BotToken)
	b := &Bot{
		cfg:           cfg,
		state:         state,
		sem:           sem,
		childSem:      childSem,
		deps:          deps,
		api:           apiClient,
		stopCh:        make(chan struct{}),
		interactions:  newDiscordInteractionState(),
		threads:       newThreadBindingStore(),
		threadParents: newThreadParentCache(),
		msgSem:        make(chan struct{}, 32),
		chatLock:      make(map[string]string),
	}

	if cfg.Discord.Reactions.Enabled {
		b.reactions = NewReactionManager(b.api, cfg.Discord.Reactions.Emojis)
	}

	if cfg.Discord.ForumBoard.Enabled {
		b.forumBoard = newDiscordForumBoard(b, cfg.Discord.ForumBoard)
	}

	b.voice = newVoiceManager(b)

	if cfg.Discord.Terminal.Enabled {
		b.terminal = newTerminalBridge(b, cfg.Discord.Terminal)
	}

	if cfg.ApprovalGates.Enabled {
		if ch := b.notifyChannelID(); ch != "" {
			b.approvalGate = newDiscordApprovalGate(b, ch)
		}
	}

	if ch := cfg.Discord.NotifyChannelID; ch != "" {
		b.notifier = NewTaskNotifier(b.api, ch)
	}

	return b
}

// Run connects to the Discord Gateway and processes events. Blocks until stopped.
func (b *Bot) Run(ctx context.Context) {
	if b.threads != nil && b.cfg.Discord.ThreadBindings.Enabled {
		go startThreadCleanup(ctx, b.threads, b.threadParents)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-b.stopCh:
			return
		default:
		}

		if err := b.connectAndRun(ctx); err != nil {
			// logged by connectAndRun
			_ = err
		}

		select {
		case <-ctx.Done():
			return
		case <-b.stopCh:
			return
		case <-time.After(5 * time.Second):
		}
	}
}

// Stop signals the bot to disconnect.
func (b *Bot) Stop() {
	select {
	case <-b.stopCh:
	default:
		close(b.stopCh)
	}
}
