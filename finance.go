package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// --- P23.4: Financial Tracking ---

// Expense represents a recorded expense.
type Expense struct {
	ID          int      `json:"id"`
	UserID      string   `json:"userId"`
	Amount      float64  `json:"amount"`
	Currency    string   `json:"currency"`
	AmountUSD   float64  `json:"amountUsd"`
	Category    string   `json:"category"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Date        string   `json:"date"`
	CreatedAt   string   `json:"createdAt"`
}

// Budget defines a monthly spending limit per category.
type Budget struct {
	ID           int     `json:"id"`
	UserID       string  `json:"userId"`
	Category     string  `json:"category"`
	MonthlyLimit float64 `json:"monthlyLimit"`
	Currency     string  `json:"currency"`
	CreatedAt    string  `json:"createdAt"`
}

// ExpenseReport is a summary of expenses for a given period.
type ExpenseReport struct {
	Period      string             `json:"period"`
	TotalAmount float64            `json:"totalAmount"`
	Currency    string             `json:"currency"`
	ByCategory  map[string]float64 `json:"byCategory"`
	Count       int                `json:"count"`
	Expenses    []Expense          `json:"expenses,omitempty"`
	Budgets     []ExpenseBudgetStatus     `json:"budgets,omitempty"`
}

// ExpenseBudgetStatus shows spending vs. budget for one category.
type ExpenseBudgetStatus struct {
	Category     string  `json:"category"`
	MonthlyLimit float64 `json:"monthlyLimit"`
	Spent        float64 `json:"spent"`
	Remaining    float64 `json:"remaining"`
	Percentage   float64 `json:"percentage"`
	OverBudget   bool    `json:"overBudget"`
}

// FinanceService manages expense tracking and budgets.
type FinanceService struct {
	cfg    *Config
	dbPath string
}

var globalFinanceService *FinanceService

// initFinanceDB creates the expense/budget/price_watches tables.
func initFinanceDB(dbPath string) error {
	ddl := `
CREATE TABLE IF NOT EXISTS expenses (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    amount REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'TWD',
    amount_usd REAL DEFAULT 0,
    category TEXT NOT NULL DEFAULT 'other',
    description TEXT DEFAULT '',
    tags TEXT DEFAULT '[]',
    date TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_expenses_user ON expenses(user_id, date);
CREATE INDEX IF NOT EXISTS idx_expenses_category ON expenses(user_id, category, date);

CREATE TABLE IF NOT EXISTS expense_budgets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    category TEXT NOT NULL,
    monthly_limit REAL NOT NULL,
    currency TEXT NOT NULL DEFAULT 'TWD',
    created_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_budget ON expense_budgets(user_id, category);

CREATE TABLE IF NOT EXISTS price_watches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL,
    from_currency TEXT DEFAULT '',
    to_currency TEXT DEFAULT '',
    condition TEXT NOT NULL,
    threshold REAL NOT NULL,
    status TEXT DEFAULT 'active',
    notify_channel TEXT DEFAULT '',
    last_checked TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
`
	cmd := exec.Command("sqlite3", dbPath, ddl)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("init finance tables: %w: %s", err, string(out))
	}
	return nil
}

func newFinanceService(cfg *Config) *FinanceService {
	return &FinanceService{
		cfg:    cfg,
		dbPath: cfg.HistoryDB,
	}
}

// --- Natural Language Expense Parsing ---

// amountRe matches numeric amounts like 350, 5.50, 2000.
var amountRe = regexp.MustCompile(`(\d+(?:\.\d+)?)`)

// parseExpenseNL parses natural language expense input.
// Supports formats like "午餐 350 元", "coffee $5.50", "2000 rent".
func parseExpenseNL(text, defaultCurrency string) (amount float64, currency string, category string, description string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, defaultCurrency, "other", ""
	}

	currency = defaultCurrency

	// Detect currency hints in text.
	lowerText := strings.ToLower(text)
	switch {
	case strings.Contains(text, "$") || strings.Contains(lowerText, "usd"):
		currency = "USD"
	case strings.Contains(text, "€") || strings.Contains(lowerText, "eur"):
		currency = "EUR"
	case strings.Contains(text, "£") || strings.Contains(lowerText, "gbp"):
		currency = "GBP"
	case strings.Contains(text, "円"):
		currency = "JPY"
	case strings.Contains(text, "元"):
		// Could be TWD or CNY; default to TWD per config.
		if currency != "TWD" && currency != "CNY" {
			currency = "TWD"
		}
	case strings.Contains(text, "¥"):
		// Ambiguous: could be JPY or CNY. Use amount heuristic.
		// Will resolve below after amount extraction.
		currency = "JPY_OR_CNY"
	}

	// Extract amount.
	matches := amountRe.FindAllStringIndex(text, -1)
	if len(matches) > 0 {
		// Pick the first numeric match.
		matchStr := text[matches[0][0]:matches[0][1]]
		fmt.Sscanf(matchStr, "%f", &amount)
	}

	// Resolve JPY_OR_CNY ambiguity based on amount.
	if currency == "JPY_OR_CNY" {
		if amount > 0 && amount < 1000 {
			currency = "CNY"
		} else {
			currency = "JPY"
		}
	}

	// Build description by removing amount and currency hints.
	desc := text
	// Remove currency symbols.
	for _, sym := range []string{"$", "€", "£", "¥", "元", "円"} {
		desc = strings.ReplaceAll(desc, sym, "")
	}
	// Remove currency codes (case-insensitive).
	for _, code := range []string{"USD", "EUR", "GBP", "JPY", "TWD", "CNY", "usd", "eur", "gbp", "jpy", "twd", "cny"} {
		desc = strings.ReplaceAll(desc, code, "")
	}
	// Remove the numeric amount from the (already symbol-stripped) description.
	// Must re-find the amount since symbol removal shifted indices.
	descMatches := amountRe.FindStringIndex(desc)
	if descMatches != nil {
		desc = desc[:descMatches[0]] + desc[descMatches[1]:]
	}
	desc = strings.TrimSpace(desc)
	// Clean up multiple spaces.
	spaceRe := regexp.MustCompile(`\s+`)
	desc = spaceRe.ReplaceAllString(desc, " ")
	description = desc

	// Auto-categorize.
	category = categorizeExpense(description)

	return amount, currency, category, description
}

// categoryKeywords maps category names to trigger keywords.
var categoryKeywords = map[string][]string{
	"food":          {"午餐", "晚餐", "早餐", "lunch", "dinner", "breakfast", "coffee", "餐", "飯", "食", "吃", "cafe", "restaurant", "pizza", "ramen", "sushi", "牛奶", "便當", "飲料", "tea", "snack"},
	"transport":     {"uber", "taxi", "計程車", "捷運", "mrt", "bus", "公車", "油費", "gas", "parking", "train", "高鐵", "加油", "停車"},
	"shopping":      {"amazon", "購物", "買", "shopping", "clothes", "衣服", "鞋", "electronics", "日用品"},
	"entertainment": {"movie", "電影", "遊戲", "game", "netflix", "spotify", "subscription", "訂閱", "書", "book"},
	"utilities":     {"電費", "水費", "internet", "phone", "手機", "網路", "瓦斯"},
	"housing":       {"rent", "房租", "mortgage", "管理費"},
	"health":        {"醫生", "doctor", "pharmacy", "藥", "gym", "健身", "牙醫", "診所", "hospital"},
}

// categorizeExpense returns a category based on description keywords.
func categorizeExpense(description string) string {
	lower := strings.ToLower(description)
	for cat, keywords := range categoryKeywords {
		for _, kw := range keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				return cat
			}
		}
	}
	return "other"
}

// --- Expense CRUD ---

// AddExpense records a new expense.
func (svc *FinanceService) AddExpense(userID string, amount float64, currency, category, description string, tags []string) (*Expense, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if userID == "" {
		userID = "default"
	}
	if currency == "" {
		currency = svc.cfg.Finance.defaultCurrencyOrTWD()
	}
	if category == "" {
		category = categorizeExpense(description)
	}
	if tags == nil {
		tags = []string{}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	date := time.Now().UTC().Format("2006-01-02")

	tagsJSON, _ := json.Marshal(tags)

	// P27.2: Encrypt description.
	encDesc := encryptField(svc.cfg, description)

	sql := fmt.Sprintf(
		`INSERT INTO expenses (user_id, amount, currency, amount_usd, category, description, tags, date, created_at)
		 VALUES ('%s', %f, '%s', 0, '%s', '%s', '%s', '%s', '%s')`,
		escapeSQLite(userID), amount, escapeSQLite(strings.ToUpper(currency)),
		escapeSQLite(category), escapeSQLite(encDesc),
		escapeSQLite(string(tagsJSON)), date, now,
	)

	// Use a single sqlite3 invocation: INSERT + SELECT last_insert_rowid().
	combinedSQL := sql + "; SELECT last_insert_rowid() as id;"
	rows, err := queryDB(svc.dbPath, combinedSQL)
	if err != nil {
		return nil, fmt.Errorf("insert expense: %w", err)
	}

	id := 0
	if len(rows) > 0 {
		id = int(jsonFloat(rows[0]["id"]))
	}

	return &Expense{
		ID:          id,
		UserID:      userID,
		Amount:      amount,
		Currency:    strings.ToUpper(currency),
		Category:    category,
		Description: description,
		Tags:        tags,
		Date:        date,
		CreatedAt:   now,
	}, nil
}

// ListExpenses returns expenses for a user within a period.
func (svc *FinanceService) ListExpenses(userID, period string, category string, limit int) ([]Expense, error) {
	if userID == "" {
		userID = "default"
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	dateFilter := periodToDateFilter(period)

	sql := fmt.Sprintf(
		`SELECT id, user_id, amount, currency, amount_usd, category, description, tags, date, created_at
		 FROM expenses WHERE user_id = '%s'`,
		escapeSQLite(userID),
	)
	if dateFilter != "" {
		sql += " AND " + dateFilter
	}
	if category != "" {
		sql += fmt.Sprintf(" AND category = '%s'", escapeSQLite(category))
	}
	sql += fmt.Sprintf(" ORDER BY date DESC, created_at DESC LIMIT %d", limit)

	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("list expenses: %w", err)
	}

	expenses := make([]Expense, 0, len(rows))
	for _, row := range rows {
		var tags []string
		tagsStr := jsonStr(row["tags"])
		if tagsStr != "" {
			json.Unmarshal([]byte(tagsStr), &tags)
		}

		// P27.2: Decrypt description.
		desc := jsonStr(row["description"])
		if k := globalEncryptionKey(); k != "" {
			if d, err := decrypt(desc, k); err == nil {
				desc = d
			}
		}
		expenses = append(expenses, Expense{
			ID:          int(jsonFloat(row["id"])),
			UserID:      jsonStr(row["user_id"]),
			Amount:      jsonFloat(row["amount"]),
			Currency:    jsonStr(row["currency"]),
			AmountUSD:   jsonFloat(row["amount_usd"]),
			Category:    jsonStr(row["category"]),
			Description: desc,
			Tags:        tags,
			Date:        jsonStr(row["date"]),
			CreatedAt:   jsonStr(row["created_at"]),
		})
	}
	return expenses, nil
}

// DeleteExpense removes an expense by ID for a given user.
func (svc *FinanceService) DeleteExpense(userID string, id int) error {
	if userID == "" {
		userID = "default"
	}
	sql := fmt.Sprintf(
		`DELETE FROM expenses WHERE id = %d AND user_id = '%s'`,
		id, escapeSQLite(userID),
	)
	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("delete expense: %w: %s", err, string(out))
	}
	return nil
}

// --- Reports ---

// GenerateReport produces an expense summary for the given period.
func (svc *FinanceService) GenerateReport(userID, period, currency string) (*ExpenseReport, error) {
	if userID == "" {
		userID = "default"
	}
	if currency == "" {
		currency = svc.cfg.Finance.defaultCurrencyOrTWD()
	}

	dateFilter := periodToDateFilter(period)

	// Get totals by category.
	sql := fmt.Sprintf(
		`SELECT category, SUM(amount) as total, COUNT(*) as cnt
		 FROM expenses WHERE user_id = '%s' AND currency = '%s'`,
		escapeSQLite(userID), escapeSQLite(strings.ToUpper(currency)),
	)
	if dateFilter != "" {
		sql += " AND " + dateFilter
	}
	sql += " GROUP BY category"

	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("report query: %w", err)
	}

	byCategory := make(map[string]float64)
	totalAmount := 0.0
	totalCount := 0
	for _, row := range rows {
		cat := jsonStr(row["category"])
		total := jsonFloat(row["total"])
		cnt := int(jsonFloat(row["cnt"]))
		byCategory[cat] = math.Round(total*100) / 100
		totalAmount += total
		totalCount += cnt
	}

	// Get recent expenses.
	expenses, _ := svc.ListExpenses(userID, period, "", 20)

	// Get budget status.
	budgets, _ := svc.CheckBudgets(userID)

	return &ExpenseReport{
		Period:      period,
		TotalAmount: math.Round(totalAmount*100) / 100,
		Currency:    strings.ToUpper(currency),
		ByCategory:  byCategory,
		Count:       totalCount,
		Expenses:    expenses,
		Budgets:     budgets,
	}, nil
}

// --- Budgets ---

// SetBudget creates or updates a monthly budget for a category.
func (svc *FinanceService) SetBudget(userID, category string, monthlyLimit float64, currency string) error {
	if userID == "" {
		userID = "default"
	}
	if category == "" {
		return fmt.Errorf("category is required")
	}
	if monthlyLimit <= 0 {
		return fmt.Errorf("monthly limit must be positive")
	}
	if currency == "" {
		currency = svc.cfg.Finance.defaultCurrencyOrTWD()
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// UPSERT: insert or replace on conflict.
	sql := fmt.Sprintf(
		`INSERT INTO expense_budgets (user_id, category, monthly_limit, currency, created_at)
		 VALUES ('%s', '%s', %f, '%s', '%s')
		 ON CONFLICT(user_id, category)
		 DO UPDATE SET monthly_limit = %f, currency = '%s'`,
		escapeSQLite(userID), escapeSQLite(category), monthlyLimit,
		escapeSQLite(strings.ToUpper(currency)), now,
		monthlyLimit, escapeSQLite(strings.ToUpper(currency)),
	)

	cmd := exec.Command("sqlite3", svc.dbPath, sql)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("set budget: %w: %s", err, string(out))
	}
	return nil
}

// GetBudgets returns all budgets for a user.
func (svc *FinanceService) GetBudgets(userID string) ([]Budget, error) {
	if userID == "" {
		userID = "default"
	}

	sql := fmt.Sprintf(
		`SELECT id, user_id, category, monthly_limit, currency, created_at
		 FROM expense_budgets WHERE user_id = '%s' ORDER BY category`,
		escapeSQLite(userID),
	)

	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("get budgets: %w", err)
	}

	budgets := make([]Budget, 0, len(rows))
	for _, row := range rows {
		budgets = append(budgets, Budget{
			ID:           int(jsonFloat(row["id"])),
			UserID:       jsonStr(row["user_id"]),
			Category:     jsonStr(row["category"]),
			MonthlyLimit: jsonFloat(row["monthly_limit"]),
			Currency:     jsonStr(row["currency"]),
			CreatedAt:    jsonStr(row["created_at"]),
		})
	}
	return budgets, nil
}

// CheckBudgets returns budget status for the current month.
func (svc *FinanceService) CheckBudgets(userID string) ([]ExpenseBudgetStatus, error) {
	if userID == "" {
		userID = "default"
	}

	budgets, err := svc.GetBudgets(userID)
	if err != nil {
		return nil, err
	}

	if len(budgets) == 0 {
		return nil, nil
	}

	// Get this month's spending per category.
	monthStart := time.Now().UTC().Format("2006-01") + "-01"
	sql := fmt.Sprintf(
		`SELECT category, SUM(amount) as total FROM expenses
		 WHERE user_id = '%s' AND date >= '%s'
		 GROUP BY category`,
		escapeSQLite(userID), monthStart,
	)

	rows, err := queryDB(svc.dbPath, sql)
	if err != nil {
		return nil, fmt.Errorf("check budgets: %w", err)
	}

	spentMap := make(map[string]float64)
	for _, row := range rows {
		cat := jsonStr(row["category"])
		spentMap[cat] = jsonFloat(row["total"])
	}

	statuses := make([]ExpenseBudgetStatus, 0, len(budgets))
	for _, b := range budgets {
		spent := math.Round(spentMap[b.Category]*100) / 100
		remaining := math.Round((b.MonthlyLimit-spent)*100) / 100
		pct := 0.0
		if b.MonthlyLimit > 0 {
			pct = math.Round(spent/b.MonthlyLimit*10000) / 100
		}

		statuses = append(statuses, ExpenseBudgetStatus{
			Category:     b.Category,
			MonthlyLimit: b.MonthlyLimit,
			Spent:        spent,
			Remaining:    remaining,
			Percentage:   pct,
			OverBudget:   spent > b.MonthlyLimit,
		})
	}
	return statuses, nil
}

// --- Tool Handlers ---

// toolExpenseAdd handles the expense_add tool.
func toolExpenseAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFinanceService == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	var args struct {
		Text        string   `json:"text"`
		Amount      float64  `json:"amount"`
		Currency    string   `json:"currency"`
		Category    string   `json:"category"`
		Description string   `json:"description"`
		UserID      string   `json:"userId"`
		Tags        []string `json:"tags"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	amount := args.Amount
	currency := args.Currency
	category := args.Category
	description := args.Description

	// If natural language text is provided, parse it.
	if args.Text != "" {
		nlAmount, nlCurrency, nlCategory, nlDesc := parseExpenseNL(args.Text, cfg.Finance.defaultCurrencyOrTWD())
		if amount <= 0 {
			amount = nlAmount
		}
		if currency == "" {
			currency = nlCurrency
		}
		if category == "" {
			category = nlCategory
		}
		if description == "" {
			description = nlDesc
		}
	}

	if amount <= 0 {
		return "", fmt.Errorf("could not determine amount; provide amount or natural language text like '午餐 350 元'")
	}

	expense, err := globalFinanceService.AddExpense(args.UserID, amount, currency, category, description, args.Tags)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(expense, "", "  ")
	return string(out), nil
}

// toolExpenseReport handles the expense_report tool.
func toolExpenseReport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFinanceService == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	var args struct {
		Period   string `json:"period"`
		Category string `json:"category"`
		UserID   string `json:"userId"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	period := args.Period
	if period == "" {
		period = "month"
	}

	report, err := globalFinanceService.GenerateReport(args.UserID, period, args.Currency)
	if err != nil {
		return "", err
	}

	out, _ := json.MarshalIndent(report, "", "  ")
	return string(out), nil
}

// toolExpenseBudget handles the expense_budget tool.
func toolExpenseBudget(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFinanceService == nil {
		return "", fmt.Errorf("finance service not initialized (enable finance in config)")
	}

	var args struct {
		Action   string  `json:"action"`
		Category string  `json:"category"`
		Limit    float64 `json:"limit"`
		Currency string  `json:"currency"`
		UserID   string  `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "set":
		if args.Category == "" {
			return "", fmt.Errorf("category is required for set action")
		}
		if args.Limit <= 0 {
			return "", fmt.Errorf("limit must be positive for set action")
		}
		err := globalFinanceService.SetBudget(args.UserID, args.Category, args.Limit, args.Currency)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Budget set: %s = %.2f %s/month",
			args.Category, args.Limit,
			func() string {
				if args.Currency != "" {
					return strings.ToUpper(args.Currency)
				}
				return cfg.Finance.defaultCurrencyOrTWD()
			}()), nil

	case "list":
		budgets, err := globalFinanceService.GetBudgets(args.UserID)
		if err != nil {
			return "", err
		}
		if len(budgets) == 0 {
			return "No budgets configured.", nil
		}
		out, _ := json.MarshalIndent(budgets, "", "  ")
		return string(out), nil

	case "check":
		statuses, err := globalFinanceService.CheckBudgets(args.UserID)
		if err != nil {
			return "", err
		}
		if len(statuses) == 0 {
			return "No budgets configured.", nil
		}
		out, _ := json.MarshalIndent(statuses, "", "  ")
		return string(out), nil

	default:
		return "", fmt.Errorf("unknown action %q (use: set, list, check)", args.Action)
	}
}

// --- Helpers ---

// periodToDateFilter returns a SQL date filter clause for the given period.
func periodToDateFilter(period string) string {
	now := time.Now().UTC()
	switch period {
	case "today":
		return fmt.Sprintf("date = '%s'", now.Format("2006-01-02"))
	case "week":
		weekAgo := now.AddDate(0, 0, -7).Format("2006-01-02")
		return fmt.Sprintf("date >= '%s'", weekAgo)
	case "month":
		monthStart := now.Format("2006-01") + "-01"
		return fmt.Sprintf("date >= '%s'", monthStart)
	case "year":
		yearStart := now.Format("2006") + "-01-01"
		return fmt.Sprintf("date >= '%s'", yearStart)
	default:
		// No filter (all time).
		return ""
	}
}
