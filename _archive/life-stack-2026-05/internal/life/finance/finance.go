// Package finance implements expense tracking and budget management.
package finance

import (
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

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
	Budgets     []ExpenseBudgetStatus `json:"budgets,omitempty"`
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

// EncryptFn encrypts a field value. Returns the value unchanged if encryption is disabled.
type EncryptFn func(value string) string

// DecryptFn decrypts a field value. Returns the value unchanged if decryption fails.
type DecryptFn func(value string) string

// Service manages expense tracking and budgets.
type Service struct {
	db              lifedb.DB
	dbPath          string
	defaultCurrency string
	encrypt         EncryptFn
	decrypt         DecryptFn
}

// New creates a new finance Service.
// dbPath is the SQLite database path.
// defaultCurrency is the fallback currency (e.g., "TWD").
// encrypt/decrypt are optional field-level encryption helpers (pass nil to disable).
func New(dbPath, defaultCurrency string, db lifedb.DB, encrypt EncryptFn, decrypt DecryptFn) *Service {
	if defaultCurrency == "" {
		defaultCurrency = "TWD"
	}
	if encrypt == nil {
		encrypt = func(v string) string { return v }
	}
	if decrypt == nil {
		decrypt = func(v string) string { return v }
	}
	return &Service{
		db:              db,
		dbPath:          dbPath,
		defaultCurrency: defaultCurrency,
		encrypt:         encrypt,
		decrypt:         decrypt,
	}
}

// DBPath returns the database file path.
func (svc *Service) DBPath() string { return svc.dbPath }

// InitDB creates the expense/budget/price_watches tables.
func InitDB(dbPath string) error {
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

// --- Natural Language Expense Parsing ---

var amountRe = regexp.MustCompile(`(\d+(?:\.\d+)?)`)

// ParseExpenseNL parses natural language expense input.
func ParseExpenseNL(text, defaultCurrency string) (amount float64, currency string, category string, description string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0, defaultCurrency, "other", ""
	}

	currency = defaultCurrency

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
		if currency != "TWD" && currency != "CNY" {
			currency = "TWD"
		}
	case strings.Contains(text, "¥"):
		currency = "JPY_OR_CNY"
	}

	matches := amountRe.FindAllStringIndex(text, -1)
	if len(matches) > 0 {
		matchStr := text[matches[0][0]:matches[0][1]]
		fmt.Sscanf(matchStr, "%f", &amount)
	}

	if currency == "JPY_OR_CNY" {
		if amount > 0 && amount < 1000 {
			currency = "CNY"
		} else {
			currency = "JPY"
		}
	}

	desc := text
	for _, sym := range []string{"$", "€", "£", "¥", "元", "円"} {
		desc = strings.ReplaceAll(desc, sym, "")
	}
	for _, code := range []string{"USD", "EUR", "GBP", "JPY", "TWD", "CNY", "usd", "eur", "gbp", "jpy", "twd", "cny"} {
		desc = strings.ReplaceAll(desc, code, "")
	}
	descMatches := amountRe.FindStringIndex(desc)
	if descMatches != nil {
		desc = desc[:descMatches[0]] + desc[descMatches[1]:]
	}
	desc = strings.TrimSpace(desc)
	spaceRe := regexp.MustCompile(`\s+`)
	desc = spaceRe.ReplaceAllString(desc, " ")
	description = desc

	category = categorizeExpense(description)
	return amount, currency, category, description
}

var categoryKeywords = map[string][]string{
	"food":          {"午餐", "晚餐", "早餐", "lunch", "dinner", "breakfast", "coffee", "餐", "飯", "食", "吃", "cafe", "restaurant", "pizza", "ramen", "sushi", "牛奶", "便當", "飲料", "tea", "snack"},
	"transport":     {"uber", "taxi", "計程車", "捷運", "mrt", "bus", "公車", "油費", "gas", "parking", "train", "高鐵", "加油", "停車"},
	"shopping":      {"amazon", "購物", "買", "shopping", "clothes", "衣服", "鞋", "electronics", "日用品"},
	"entertainment": {"movie", "電影", "遊戲", "game", "netflix", "spotify", "subscription", "訂閱", "書", "book"},
	"utilities":     {"電費", "水費", "internet", "phone", "手機", "網路", "瓦斯"},
	"housing":       {"rent", "房租", "mortgage", "管理費"},
	"health":        {"醫生", "doctor", "pharmacy", "藥", "gym", "健身", "牙醫", "診所", "hospital"},
}

// CategorizeExpense is an exported alias for use in tests.
func CategorizeExpense(description string) string { return categorizeExpense(description) }

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
func (svc *Service) AddExpense(userID string, amount float64, currency, category, description string, tags []string) (*Expense, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	if userID == "" {
		userID = "default"
	}
	if currency == "" {
		currency = svc.defaultCurrency
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
	encDesc := svc.encrypt(description)

	sql := fmt.Sprintf(
		`INSERT INTO expenses (user_id, amount, currency, amount_usd, category, description, tags, date, created_at)
		 VALUES ('%s', %f, '%s', 0, '%s', '%s', '%s', '%s', '%s')`,
		svc.db.Escape(userID), amount, svc.db.Escape(strings.ToUpper(currency)),
		svc.db.Escape(category), svc.db.Escape(encDesc),
		svc.db.Escape(string(tagsJSON)), date, now,
	)

	combinedSQL := sql + "; SELECT last_insert_rowid() as id;"
	rows, err := svc.db.Query(svc.dbPath, combinedSQL)
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
func (svc *Service) ListExpenses(userID, period string, category string, limit int) ([]Expense, error) {
	if userID == "" {
		userID = "default"
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	dateFilter := PeriodToDateFilter(period)

	sql := fmt.Sprintf(
		`SELECT id, user_id, amount, currency, amount_usd, category, description, tags, date, created_at
		 FROM expenses WHERE user_id = '%s'`,
		svc.db.Escape(userID),
	)
	if dateFilter != "" {
		sql += " AND " + dateFilter
	}
	if category != "" {
		sql += fmt.Sprintf(" AND category = '%s'", svc.db.Escape(category))
	}
	sql += fmt.Sprintf(" ORDER BY date DESC, created_at DESC LIMIT %d", limit)

	rows, err := svc.db.Query(svc.dbPath, sql)
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

		desc := svc.decrypt(jsonStr(row["description"]))
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
func (svc *Service) DeleteExpense(userID string, id int) error {
	if userID == "" {
		userID = "default"
	}
	sql := fmt.Sprintf(
		`DELETE FROM expenses WHERE id = %d AND user_id = '%s'`,
		id, svc.db.Escape(userID),
	)
	return svc.db.Exec(svc.dbPath, sql)
}

// --- Reports ---

// GenerateReport produces an expense summary for the given period.
func (svc *Service) GenerateReport(userID, period, currency string) (*ExpenseReport, error) {
	if userID == "" {
		userID = "default"
	}
	if currency == "" {
		currency = svc.defaultCurrency
	}

	dateFilter := PeriodToDateFilter(period)

	sql := fmt.Sprintf(
		`SELECT category, SUM(amount) as total, COUNT(*) as cnt
		 FROM expenses WHERE user_id = '%s' AND currency = '%s'`,
		svc.db.Escape(userID), svc.db.Escape(strings.ToUpper(currency)),
	)
	if dateFilter != "" {
		sql += " AND " + dateFilter
	}
	sql += " GROUP BY category"

	rows, err := svc.db.Query(svc.dbPath, sql)
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

	expenses, _ := svc.ListExpenses(userID, period, "", 20)
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
func (svc *Service) SetBudget(userID, category string, monthlyLimit float64, currency string) error {
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
		currency = svc.defaultCurrency
	}

	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO expense_budgets (user_id, category, monthly_limit, currency, created_at)
		 VALUES ('%s', '%s', %f, '%s', '%s')
		 ON CONFLICT(user_id, category)
		 DO UPDATE SET monthly_limit = %f, currency = '%s'`,
		svc.db.Escape(userID), svc.db.Escape(category), monthlyLimit,
		svc.db.Escape(strings.ToUpper(currency)), now,
		monthlyLimit, svc.db.Escape(strings.ToUpper(currency)),
	)

	return svc.db.Exec(svc.dbPath, sql)
}

// GetBudgets returns all budgets for a user.
func (svc *Service) GetBudgets(userID string) ([]Budget, error) {
	if userID == "" {
		userID = "default"
	}

	sql := fmt.Sprintf(
		`SELECT id, user_id, category, monthly_limit, currency, created_at
		 FROM expense_budgets WHERE user_id = '%s' ORDER BY category`,
		svc.db.Escape(userID),
	)

	rows, err := svc.db.Query(svc.dbPath, sql)
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
func (svc *Service) CheckBudgets(userID string) ([]ExpenseBudgetStatus, error) {
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

	monthStart := time.Now().UTC().Format("2006-01") + "-01"
	sql := fmt.Sprintf(
		`SELECT category, SUM(amount) as total FROM expenses
		 WHERE user_id = '%s' AND date >= '%s'
		 GROUP BY category`,
		svc.db.Escape(userID), monthStart,
	)

	rows, err := svc.db.Query(svc.dbPath, sql)
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

// PeriodToDateFilter returns a SQL date filter clause for the given period.
func PeriodToDateFilter(period string) string {
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
		return ""
	}
}

// --- JSON helpers (local copies to avoid root-package dependency) ---

func jsonStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

func jsonFloat(v any) float64 {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case string:
		var f float64
		fmt.Sscanf(x, "%f", &f)
		return f
	}
	return 0
}
