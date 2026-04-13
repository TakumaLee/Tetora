package discord

import (
	"context"
	"sync"
	"time"

	"tetora/internal/config"
	"tetora/internal/cron"
	dtypes "tetora/internal/dispatch"
)

// ModelChangeResult holds the before/after state from an UpdateAgentModel call.
type ModelChangeResult struct {
	OldModel    string
	OldProvider string
	NewProvider string // empty if provider was not changed
}

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

	// RunSingleTask executes a single task synchronously and returns the result.
	RunSingleTask func(ctx context.Context, task dtypes.Task, agentName string) dtypes.TaskResult

	// EnsureProvider validates that the named provider preset exists.
	EnsureProvider func(presetName string) error

	// UpdateAgentModel updates an agent's model in config.
	UpdateAgentModel func(agentName, model, providerName string) (ModelChangeResult, error)

	// SetMemory persists a key/value memory entry for an agent's workspace.
	SetMemory func(agent, key, value string)

	// Version is the application version string (e.g. "1.2.3" or "dev").
	Version string
}

// Bot manages the Discord Gateway connection and all message handling.
// Migrated from the main-package Bot struct.
//
// Field types that were previously main-package-only types are now in this package:
//   - *discordInteractionState  → defined in interactions.go
//   - *threadBindingStore       → defined in threads.go
//   - *threadParentCache        → defined in threads_cache.go
//   - *discordApprovalGate      → defined in approval.go
//   - *TerminalBridge           → defined in terminal.go
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
	cronEng  *cron.Engine
	deps     BotDeps

	botUserID string
	sessionID string
	seq       int
	seqMu     sync.Mutex

	api          *Client
	stopCh       chan struct{}
	interactions  *DiscordInteractionState
	threads       *ThreadBindingStore
	threadParents *ThreadParentCache
	reactions     *ReactionManager
	approvalGate  *DiscordApprovalGate
	forumBoard    *ForumBoard
	voice         *VoiceManager
	gatewayConn   *WsConn
	notifier      *TaskNotifier
	terminal      *TerminalBridge
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
	cronEng *cron.Engine,
	sem, childSem chan struct{},
	deps BotDeps,
) *Bot {
	apiClient := NewClient(cfg.Discord.BotToken)
	b := &Bot{
		cfg:           cfg,
		state:         state,
		sem:           sem,
		childSem:      childSem,
		cronEng:       cronEng,
		deps:          deps,
		api:           apiClient,
		stopCh:        make(chan struct{}),
		interactions:  NewDiscordInteractionState(),
		threads:       NewThreadBindingStore(),
		threadParents: NewThreadParentCache(),
		msgSem:        make(chan struct{}, 32),
		chatLock:      make(map[string]string),
	}

	if cfg.Discord.Reactions.Enabled {
		b.reactions = NewReactionManager(b.api, cfg.Discord.Reactions.Emojis)
	}

	if cfg.Discord.ForumBoard.Enabled {
		b.forumBoard = newDiscordForumBoard(b, cfg.Discord.ForumBoard)
	}

	b.voice = newDiscordVoiceManager(b)

	if cfg.Discord.Terminal.Enabled {
		b.terminal = newTerminalBridge(b, cfg.Discord.Terminal)
	}

	if cfg.ApprovalGates.Enabled {
		if ch := b.notifyChannelID(); ch != "" {
			b.approvalGate = NewDiscordApprovalGate(b, ch)
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
