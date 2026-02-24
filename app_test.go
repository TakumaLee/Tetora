package main

import "testing"

func TestAppSyncToGlobals(t *testing.T) {
	// Save and restore globals.
	oldProfile := globalUserProfileService
	oldFinance := globalFinanceService
	oldScheduling := globalSchedulingService
	defer func() {
		globalUserProfileService = oldProfile
		globalFinanceService = oldFinance
		globalSchedulingService = oldScheduling
	}()

	// Clear globals.
	globalUserProfileService = nil
	globalFinanceService = nil
	globalSchedulingService = nil

	// Create App with mock services.
	cfg := &Config{}
	sched := &SchedulingService{cfg: cfg}

	app := &App{
		Cfg:        cfg,
		Scheduling: sched,
	}
	app.SyncToGlobals()

	// Verify globals are set.
	if globalSchedulingService != sched {
		t.Error("SyncToGlobals should set globalSchedulingService")
	}

	// Nil fields should NOT overwrite existing globals.
	if globalUserProfileService != nil {
		t.Error("nil App.UserProfile should not set global")
	}
}

func TestAppNilSafe(t *testing.T) {
	// App with all nil fields should not panic on SyncToGlobals.
	app := &App{Cfg: &Config{}}
	app.SyncToGlobals() // should not panic
}

func TestAppSyncToGlobals_Phase2Fields(t *testing.T) {
	// Save and restore globals.
	oldLifecycle := globalLifecycleEngine
	oldTimeTracking := globalTimeTracking
	oldSpawnTracker := globalSpawnTracker
	oldJudgeCache := globalJudgeCache
	oldImageGen := globalImageGenLimiter
	oldUMEnabled := globalUnifiedMemoryEnabled
	oldUMDB := globalUnifiedMemoryDB
	defer func() {
		globalLifecycleEngine = oldLifecycle
		globalTimeTracking = oldTimeTracking
		globalSpawnTracker = oldSpawnTracker
		globalJudgeCache = oldJudgeCache
		globalImageGenLimiter = oldImageGen
		globalUnifiedMemoryEnabled = oldUMEnabled
		globalUnifiedMemoryDB = oldUMDB
	}()

	// Clear globals.
	globalLifecycleEngine = nil
	globalTimeTracking = nil
	globalUnifiedMemoryEnabled = false
	globalUnifiedMemoryDB = ""

	cfg := &Config{}
	le := &LifecycleEngine{cfg: cfg}
	tt := &TimeTrackingService{cfg: cfg}
	st := &spawnTracker{children: make(map[string]int)}
	ig := &imageGenLimiter{}

	app := &App{
		Cfg:                  cfg,
		Lifecycle:            le,
		TimeTracking:         tt,
		SpawnTracker:         st,
		ImageGenLimiter:      ig,
		UnifiedMemoryEnabled: true,
		UnifiedMemoryDB:      "/tmp/test.db",
	}
	app.SyncToGlobals()

	if globalLifecycleEngine != le {
		t.Error("SyncToGlobals should set globalLifecycleEngine")
	}
	if globalTimeTracking != tt {
		t.Error("SyncToGlobals should set globalTimeTracking")
	}
	if globalSpawnTracker != st {
		t.Error("SyncToGlobals should set globalSpawnTracker")
	}
	if globalImageGenLimiter != ig {
		t.Error("SyncToGlobals should set globalImageGenLimiter")
	}
	if !globalUnifiedMemoryEnabled {
		t.Error("SyncToGlobals should set globalUnifiedMemoryEnabled")
	}
	if globalUnifiedMemoryDB != "/tmp/test.db" {
		t.Errorf("SyncToGlobals should set globalUnifiedMemoryDB, got %q", globalUnifiedMemoryDB)
	}
}
