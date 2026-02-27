package main

import (
	"sync"
	"time"
)

// Server holds all dependencies for the HTTP server.
type Server struct {
	cfg             *Config
	app             *App // P28.1: application container
	state           *dispatchState
	sem             chan struct{}
	childSem        chan struct{} // sub-agent tasks (depth > 0)
	cron            *CronEngine
	secMon          *securityMonitor
	mcpHost         *MCPHost
	proactiveEngine *ProactiveEngine
	groupChatEngine *GroupChatEngine
	voiceEngine     *VoiceEngine
	slackBot        *SlackBot
	whatsappBot     *WhatsAppBot
	pluginHost      *PluginHost
	lineBot         *LINEBot
	teamsBot        *TeamsBot
	signalBot       *SignalBot
	gchatBot        *GoogleChatBot
	imessageBot     *IMessageBot
	// internal (created at start)
	taskBoardDispatcher *TaskBoardDispatcher
	canvasEngine        *CanvasEngine
	voiceRealtimeEngine *VoiceRealtimeEngine
	startTime           time.Time
	limiter             *loginLimiter
	apiLimiter          *apiRateLimiter

	// Config hot-reload support
	cfgMu sync.RWMutex

	// DegradedServices tracks services that failed to initialize.
	DegradedServices []string

	// drainCh is closed when a drain request is received, triggering graceful shutdown.
	drainCh chan struct{}
}

// Cfg returns the current config with read-lock protection.
func (s *Server) Cfg() *Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

// ReloadConfig atomically swaps the config pointer.
func (s *Server) ReloadConfig(newCfg *Config) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	s.cfg = newCfg
}
