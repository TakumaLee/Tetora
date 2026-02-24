package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupFinanceTestDB(t *testing.T) (string, *FinanceService) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	cfg := &Config{
		HistoryDB: dbPath,
		Finance: FinanceConfig{
			Enabled:         true,
			DefaultCurrency: "TWD",
		},
	}
	svc := newFinanceService(cfg)
	return dbPath, svc
}

func TestInitFinanceDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	if err := initFinanceDB(dbPath); err != nil {
		t.Fatalf("initFinanceDB: %v", err)
	}
	// Verify tables exist.
	rows, err := queryDB(dbPath, "SELECT name FROM sqlite_master WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	names := make(map[string]bool)
	for _, row := range rows {
		names[jsonStr(row["name"])] = true
	}
	for _, want := range []string{"expenses", "expense_budgets", "price_watches"} {
		if !names[want] {
			t.Errorf("missing table %s, have: %v", want, names)
		}
	}
}

func TestInitFinanceDB_InvalidPath(t *testing.T) {
	err := initFinanceDB("/nonexistent/path/db.sqlite")
	if err == nil {
		t.Fatal("expected error for invalid path")
	}
}

func TestParseExpenseNL_Chinese(t *testing.T) {
	tests := []struct {
		input       string
		wantAmount  float64
		wantCur     string
		wantCat     string
		wantDescLen bool // true = non-empty description
	}{
		{"午餐 350 元", 350, "TWD", "food", true},
		{"早餐 80 元", 80, "TWD", "food", true},
		{"電費 2000", 2000, "TWD", "utilities", true},
		{"計程車 250", 250, "TWD", "transport", true},
		{"Netflix 訂閱 390", 390, "TWD", "entertainment", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			amount, cur, cat, desc := parseExpenseNL(tt.input, "TWD")
			if amount != tt.wantAmount {
				t.Errorf("amount: got %f, want %f", amount, tt.wantAmount)
			}
			if cur != tt.wantCur {
				t.Errorf("currency: got %s, want %s", cur, tt.wantCur)
			}
			if cat != tt.wantCat {
				t.Errorf("category: got %s, want %s", cat, tt.wantCat)
			}
			if tt.wantDescLen && desc == "" {
				t.Error("expected non-empty description")
			}
		})
	}
}

func TestParseExpenseNL_English(t *testing.T) {
	tests := []struct {
		input      string
		wantAmount float64
		wantCur    string
		wantCat    string
	}{
		{"coffee $5.50", 5.50, "USD", "food"},
		{"$12 lunch", 12, "USD", "food"},
		{"rent 2000", 2000, "TWD", "housing"},
		{"uber 350", 350, "TWD", "transport"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			amount, cur, cat, _ := parseExpenseNL(tt.input, "TWD")
			if amount != tt.wantAmount {
				t.Errorf("amount: got %f, want %f", amount, tt.wantAmount)
			}
			if cur != tt.wantCur {
				t.Errorf("currency: got %s, want %s", cur, tt.wantCur)
			}
			if cat != tt.wantCat {
				t.Errorf("category: got %s, want %s", cat, tt.wantCat)
			}
		})
	}
}

func TestParseExpenseNL_EmptyInput(t *testing.T) {
	amount, cur, cat, desc := parseExpenseNL("", "USD")
	if amount != 0 || cur != "USD" || cat != "other" || desc != "" {
		t.Errorf("unexpected result for empty: amount=%f cur=%s cat=%s desc=%s", amount, cur, cat, desc)
	}
}

func TestParseExpenseNL_Euro(t *testing.T) {
	amount, cur, _, _ := parseExpenseNL("groceries €25", "TWD")
	if amount != 25 {
		t.Errorf("amount: got %f, want 25", amount)
	}
	if cur != "EUR" {
		t.Errorf("currency: got %s, want EUR", cur)
	}
}

func TestParseExpenseNL_Yen(t *testing.T) {
	// Large yen amount should be JPY.
	amount, cur, _, _ := parseExpenseNL("ラーメン ¥1500", "TWD")
	if amount != 1500 {
		t.Errorf("amount: got %f, want 1500", amount)
	}
	if cur != "JPY" {
		t.Errorf("currency: got %s, want JPY", cur)
	}

	// Explicit 円 marker.
	amount2, cur2, _, _ := parseExpenseNL("寿司 3000 円", "TWD")
	if amount2 != 3000 {
		t.Errorf("amount: got %f, want 3000", amount2)
	}
	if cur2 != "JPY" {
		t.Errorf("currency: got %s, want JPY", cur2)
	}
}

func TestCategorizeExpense(t *testing.T) {
	tests := []struct {
		desc string
		want string
	}{
		{"午餐便當", "food"},
		{"Uber ride", "transport"},
		{"Netflix subscription", "entertainment"},
		{"電費", "utilities"},
		{"房租", "housing"},
		{"牙醫", "health"},
		{"Amazon purchase", "shopping"},
		{"something random", "other"},
		{"", "other"},
	}

	for _, tt := range tests {
		got := categorizeExpense(tt.desc)
		if got != tt.want {
			t.Errorf("categorize(%q) = %s, want %s", tt.desc, got, tt.want)
		}
	}
}

func TestAddExpense(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	exp, err := svc.AddExpense("user1", 350, "TWD", "food", "lunch", nil)
	if err != nil {
		t.Fatalf("AddExpense: %v", err)
	}
	if exp.Amount != 350 {
		t.Errorf("amount: got %f, want 350", exp.Amount)
	}
	if exp.Currency != "TWD" {
		t.Errorf("currency: got %s, want TWD", exp.Currency)
	}
	if exp.Category != "food" {
		t.Errorf("category: got %s, want food", exp.Category)
	}
	if exp.UserID != "user1" {
		t.Errorf("userID: got %s, want user1", exp.UserID)
	}
}

func TestAddExpense_Defaults(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	// Empty userID should default.
	exp, err := svc.AddExpense("", 100, "", "", "coffee", nil)
	if err != nil {
		t.Fatalf("AddExpense: %v", err)
	}
	if exp.UserID != "default" {
		t.Errorf("userID: got %s, want default", exp.UserID)
	}
	if exp.Currency != "TWD" {
		t.Errorf("currency: got %s, want TWD", exp.Currency)
	}
	if exp.Category != "food" {
		t.Errorf("category: got %s, want food (auto-categorized from 'coffee')", exp.Category)
	}
}

func TestAddExpense_InvalidAmount(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	_, err := svc.AddExpense("user1", 0, "TWD", "food", "test", nil)
	if err == nil {
		t.Fatal("expected error for zero amount")
	}
	_, err = svc.AddExpense("user1", -10, "TWD", "food", "test", nil)
	if err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestListExpenses(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	// Add some expenses.
	svc.AddExpense("user1", 100, "TWD", "food", "breakfast", nil)
	svc.AddExpense("user1", 200, "TWD", "transport", "taxi", nil)
	svc.AddExpense("user1", 300, "TWD", "food", "dinner", nil)
	svc.AddExpense("user2", 500, "TWD", "food", "lunch", nil)

	// List all for user1.
	expenses, err := svc.ListExpenses("user1", "", "", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(expenses) != 3 {
		t.Errorf("expected 3 expenses, got %d", len(expenses))
	}

	// List by category.
	foodExpenses, err := svc.ListExpenses("user1", "", "food", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(foodExpenses) != 2 {
		t.Errorf("expected 2 food expenses, got %d", len(foodExpenses))
	}

	// List for user2.
	user2Expenses, err := svc.ListExpenses("user2", "", "", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(user2Expenses) != 1 {
		t.Errorf("expected 1 expense for user2, got %d", len(user2Expenses))
	}
}

func TestListExpenses_DefaultUser(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("", 100, "TWD", "food", "test", nil)

	expenses, err := svc.ListExpenses("", "", "", 10)
	if err != nil {
		t.Fatalf("ListExpenses: %v", err)
	}
	if len(expenses) != 1 {
		t.Fatalf("expected 1 expense, got %d", len(expenses))
	}
	if expenses[0].UserID != "default" {
		t.Errorf("expected default user, got %s", expenses[0].UserID)
	}
}

func TestDeleteExpense(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	exp, _ := svc.AddExpense("user1", 100, "TWD", "food", "test", nil)

	// Verify it exists.
	expenses, _ := svc.ListExpenses("user1", "", "", 10)
	if len(expenses) != 1 {
		t.Fatalf("expected 1 expense, got %d", len(expenses))
	}

	// Delete it.
	if err := svc.DeleteExpense("user1", exp.ID); err != nil {
		t.Fatalf("DeleteExpense: %v", err)
	}

	// Verify it's gone.
	expenses, _ = svc.ListExpenses("user1", "", "", 10)
	if len(expenses) != 0 {
		t.Errorf("expected 0 expenses after delete, got %d", len(expenses))
	}
}

func TestGenerateReport_Today(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("user1", 100, "TWD", "food", "breakfast", nil)
	svc.AddExpense("user1", 250, "TWD", "transport", "taxi", nil)
	svc.AddExpense("user1", 150, "TWD", "food", "lunch", nil)

	report, err := svc.GenerateReport("user1", "today", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}

	if report.TotalAmount != 500 {
		t.Errorf("total: got %f, want 500", report.TotalAmount)
	}
	if report.Count != 3 {
		t.Errorf("count: got %d, want 3", report.Count)
	}
	if report.ByCategory["food"] != 250 {
		t.Errorf("food: got %f, want 250", report.ByCategory["food"])
	}
	if report.ByCategory["transport"] != 250 {
		t.Errorf("transport: got %f, want 250", report.ByCategory["transport"])
	}
	if report.Currency != "TWD" {
		t.Errorf("currency: got %s, want TWD", report.Currency)
	}
}

func TestGenerateReport_Week(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("user1", 500, "TWD", "food", "groceries", nil)

	report, err := svc.GenerateReport("user1", "week", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.Period != "week" {
		t.Errorf("period: got %s, want week", report.Period)
	}
	if report.Count != 1 {
		t.Errorf("count: got %d, want 1", report.Count)
	}
}

func TestGenerateReport_Month(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.AddExpense("user1", 1000, "TWD", "housing", "rent", nil)
	svc.AddExpense("user1", 500, "TWD", "utilities", "electric", nil)

	report, err := svc.GenerateReport("user1", "month", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.TotalAmount != 1500 {
		t.Errorf("total: got %f, want 1500", report.TotalAmount)
	}
}

func TestGenerateReport_Empty(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	report, err := svc.GenerateReport("nobody", "today", "TWD")
	if err != nil {
		t.Fatalf("GenerateReport: %v", err)
	}
	if report.TotalAmount != 0 {
		t.Errorf("total: got %f, want 0", report.TotalAmount)
	}
	if report.Count != 0 {
		t.Errorf("count: got %d, want 0", report.Count)
	}
}

func TestSetBudget(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	err := svc.SetBudget("user1", "food", 10000, "TWD")
	if err != nil {
		t.Fatalf("SetBudget: %v", err)
	}

	budgets, err := svc.GetBudgets("user1")
	if err != nil {
		t.Fatalf("GetBudgets: %v", err)
	}
	if len(budgets) != 1 {
		t.Fatalf("expected 1 budget, got %d", len(budgets))
	}
	if budgets[0].Category != "food" {
		t.Errorf("category: got %s, want food", budgets[0].Category)
	}
	if budgets[0].MonthlyLimit != 10000 {
		t.Errorf("limit: got %f, want 10000", budgets[0].MonthlyLimit)
	}
}

func TestSetBudget_Update(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	svc.SetBudget("user1", "food", 10000, "TWD")
	// Update the same category.
	svc.SetBudget("user1", "food", 15000, "TWD")

	budgets, _ := svc.GetBudgets("user1")
	if len(budgets) != 1 {
		t.Fatalf("expected 1 budget after update, got %d", len(budgets))
	}
	if budgets[0].MonthlyLimit != 15000 {
		t.Errorf("limit after update: got %f, want 15000", budgets[0].MonthlyLimit)
	}
}

func TestSetBudget_Validation(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	if err := svc.SetBudget("user1", "", 1000, "TWD"); err == nil {
		t.Error("expected error for empty category")
	}
	if err := svc.SetBudget("user1", "food", 0, "TWD"); err == nil {
		t.Error("expected error for zero limit")
	}
	if err := svc.SetBudget("user1", "food", -100, "TWD"); err == nil {
		t.Error("expected error for negative limit")
	}
}

func TestGetBudgets_Empty(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	budgets, err := svc.GetBudgets("nobody")
	if err != nil {
		t.Fatalf("GetBudgets: %v", err)
	}
	if len(budgets) != 0 {
		t.Errorf("expected 0 budgets, got %d", len(budgets))
	}
}

func TestCheckBudgets(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	// Set budgets.
	svc.SetBudget("user1", "food", 5000, "TWD")
	svc.SetBudget("user1", "transport", 3000, "TWD")

	// Add expenses.
	svc.AddExpense("user1", 2500, "TWD", "food", "groceries", nil)
	svc.AddExpense("user1", 3500, "TWD", "transport", "taxi rides", nil)

	statuses, err := svc.CheckBudgets("user1")
	if err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	if len(statuses) != 2 {
		t.Fatalf("expected 2 budget statuses, got %d", len(statuses))
	}

	// Find food and transport statuses.
	statusMap := make(map[string]ExpenseBudgetStatus)
	for _, s := range statuses {
		statusMap[s.Category] = s
	}

	food := statusMap["food"]
	if food.Spent != 2500 {
		t.Errorf("food spent: got %f, want 2500", food.Spent)
	}
	if food.Remaining != 2500 {
		t.Errorf("food remaining: got %f, want 2500", food.Remaining)
	}
	if food.OverBudget {
		t.Error("food should not be over budget")
	}
	if food.Percentage != 50 {
		t.Errorf("food percentage: got %f, want 50", food.Percentage)
	}

	transport := statusMap["transport"]
	if transport.Spent != 3500 {
		t.Errorf("transport spent: got %f, want 3500", transport.Spent)
	}
	if !transport.OverBudget {
		t.Error("transport should be over budget")
	}
}

func TestCheckBudgets_NoBudgets(t *testing.T) {
	_, svc := setupFinanceTestDB(t)

	statuses, err := svc.CheckBudgets("nobody")
	if err != nil {
		t.Fatalf("CheckBudgets: %v", err)
	}
	if statuses != nil {
		t.Errorf("expected nil statuses, got %v", statuses)
	}
}

func TestToolExpenseAdd(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"text":   "午餐 350 元",
		"userId": "tester",
	})

	result, err := toolExpenseAdd(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseAdd: %v", err)
	}
	if !strings.Contains(result, "350") {
		t.Errorf("expected 350 in result, got: %s", result)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food category, got: %s", result)
	}
}

func TestToolExpenseAdd_ExplicitFields(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"amount":      99.99,
		"currency":    "USD",
		"category":    "entertainment",
		"description": "movie ticket",
		"userId":      "tester",
	})

	result, err := toolExpenseAdd(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseAdd: %v", err)
	}
	if !strings.Contains(result, "99.99") {
		t.Errorf("expected 99.99 in result, got: %s", result)
	}
	if !strings.Contains(result, "USD") {
		t.Errorf("expected USD, got: %s", result)
	}
}

func TestToolExpenseAdd_NoAmount(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"text": "something without number",
	})

	_, err := toolExpenseAdd(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when no amount can be determined")
	}
}

func TestToolExpenseReport(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	svc.AddExpense("tester", 500, "TWD", "food", "dinner", nil)

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"period": "today",
		"userId": "tester",
	})

	result, err := toolExpenseReport(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseReport: %v", err)
	}
	if !strings.Contains(result, "500") {
		t.Errorf("expected 500 in report, got: %s", result)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food category in report, got: %s", result)
	}
}

func TestToolExpenseBudget_Set(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"action":   "set",
		"category": "food",
		"limit":    10000,
		"userId":   "tester",
	})

	result, err := toolExpenseBudget(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseBudget set: %v", err)
	}
	if !strings.Contains(result, "food") || !strings.Contains(result, "10000") {
		t.Errorf("unexpected result: %s", result)
	}
}

func TestToolExpenseBudget_List(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	svc.SetBudget("tester", "food", 5000, "TWD")

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"action": "list",
		"userId": "tester",
	})

	result, err := toolExpenseBudget(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseBudget list: %v", err)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food in budget list, got: %s", result)
	}
}

func TestToolExpenseBudget_Check(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	svc.SetBudget("tester", "food", 5000, "TWD")
	svc.AddExpense("tester", 2000, "TWD", "food", "groceries", nil)

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"action": "check",
		"userId": "tester",
	})

	result, err := toolExpenseBudget(context.Background(), cfg, input)
	if err != nil {
		t.Fatalf("toolExpenseBudget check: %v", err)
	}
	if !strings.Contains(result, "food") {
		t.Errorf("expected food in budget check, got: %s", result)
	}
	if !strings.Contains(result, "2000") {
		t.Errorf("expected 2000 spent, got: %s", result)
	}
}

func TestToolExpenseBudget_InvalidAction(t *testing.T) {
	_, svc := setupFinanceTestDB(t)
	oldSvc := globalFinanceService
	globalFinanceService = svc
	defer func() { globalFinanceService = oldSvc }()

	cfg := svc.cfg
	input, _ := json.Marshal(map[string]any{
		"action": "invalid",
	})

	_, err := toolExpenseBudget(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
}

func TestToolExpenseAdd_NotInitialized(t *testing.T) {
	oldSvc := globalFinanceService
	globalFinanceService = nil
	defer func() { globalFinanceService = oldSvc }()

	cfg := &Config{}
	input, _ := json.Marshal(map[string]any{"text": "lunch 100"})
	_, err := toolExpenseAdd(context.Background(), cfg, input)
	if err == nil {
		t.Fatal("expected error when service not initialized")
	}
}

func TestFinanceConfig_DefaultCurrency(t *testing.T) {
	cfg := FinanceConfig{}
	if got := cfg.defaultCurrencyOrTWD(); got != "TWD" {
		t.Errorf("default currency: got %s, want TWD", got)
	}

	cfg.DefaultCurrency = "JPY"
	if got := cfg.defaultCurrencyOrTWD(); got != "JPY" {
		t.Errorf("custom currency: got %s, want JPY", got)
	}
}

func TestPeriodToDateFilter(t *testing.T) {
	tests := []struct {
		period string
		hasSQL bool
	}{
		{"today", true},
		{"week", true},
		{"month", true},
		{"year", true},
		{"all", false},
		{"", false},
	}

	for _, tt := range tests {
		result := periodToDateFilter(tt.period)
		if tt.hasSQL && result == "" {
			t.Errorf("period %q: expected SQL filter, got empty", tt.period)
		}
		if !tt.hasSQL && result != "" {
			t.Errorf("period %q: expected empty filter, got %s", tt.period, result)
		}
	}
}

// Ensure the test DB file gets cleaned up.
func TestFinanceDB_Cleanup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "cleanup.db")
	initFinanceDB(dbPath)
	_, err := os.Stat(dbPath)
	if err != nil {
		t.Fatalf("DB file should exist: %v", err)
	}
}
