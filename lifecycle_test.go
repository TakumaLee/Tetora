package main

import "testing"

func TestSuggestHabitForGoal_Fitness(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	suggestions := le.SuggestHabitForGoal("Run a marathon", "fitness")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for fitness goal")
	}
	if len(suggestions) > 3 {
		t.Errorf("expected max 3 suggestions, got %d", len(suggestions))
	}
}

func TestSuggestHabitForGoal_Learning(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	suggestions := le.SuggestHabitForGoal("Learn Japanese", "learning")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions for learning goal")
	}
	found := false
	for _, s := range suggestions {
		if s == "Read 30 min daily" || s == "Practice flashcards" || s == "Write summary notes" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected learning-related suggestion, got %v", suggestions)
	}
}

func TestSuggestHabitForGoal_NoMatch(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	suggestions := le.SuggestHabitForGoal("Buy a house", "personal")
	if len(suggestions) == 0 {
		t.Fatal("expected generic suggestions when no match")
	}
	// Should return generic suggestions.
	if suggestions[0] != "Review progress weekly" {
		t.Errorf("expected generic suggestion, got %q", suggestions[0])
	}
}

func TestSuggestHabitForGoal_MultipleMatches(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}

	// "health and fitness" matches both keywords.
	suggestions := le.SuggestHabitForGoal("Improve health and fitness", "")
	if len(suggestions) == 0 {
		t.Fatal("expected suggestions")
	}
	if len(suggestions) > 3 {
		t.Errorf("expected max 3 suggestions even with multiple matches, got %d", len(suggestions))
	}
}

func TestOnGoalCompleted_NoGoalsService(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}
	old := globalGoalsService
	globalGoalsService = nil
	defer func() { globalGoalsService = old }()

	err := le.OnGoalCompleted("fake-id")
	if err == nil {
		t.Error("expected error when goals service is nil")
	}
}

func TestSyncBirthdayReminders_NoContacts(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}
	old := globalContactsService
	globalContactsService = nil
	defer func() { globalContactsService = old }()

	_, err := le.SyncBirthdayReminders()
	if err == nil {
		t.Error("expected error when contacts service is nil")
	}
}

func TestRunInsightActions_NilServices(t *testing.T) {
	le := &LifecycleEngine{cfg: &Config{}}
	oldInsights := globalInsightsEngine
	oldContacts := globalContactsService
	globalInsightsEngine = nil
	globalContactsService = nil
	defer func() {
		globalInsightsEngine = oldInsights
		globalContactsService = oldContacts
	}()

	// Should not panic with nil services.
	actions, err := le.RunInsightActions()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(actions) != 0 {
		t.Errorf("expected 0 actions with nil services, got %d", len(actions))
	}
}
