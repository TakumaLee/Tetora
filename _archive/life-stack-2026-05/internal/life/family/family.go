// Package family provides multi-user / family mode management.
package family

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"tetora/internal/life/lifedb"
)

// Config holds settings for multi-user / family mode.
type Config struct {
	MaxUsers         int     // default 10
	DefaultBudget    float64 // monthly USD, 0=unlimited
	DefaultRateLimit int     // daily requests, default 100
}

func (c Config) maxUsersOrDefault() int {
	if c.MaxUsers > 0 {
		return c.MaxUsers
	}
	return 10
}

func (c Config) defaultRateLimitOrDefault() int {
	if c.DefaultRateLimit > 0 {
		return c.DefaultRateLimit
	}
	return 100
}

// FamilyUser represents a user in the family/multi-user system.
type FamilyUser struct {
	UserID         string  `json:"userId"`
	Role           string  `json:"role"`
	DisplayName    string  `json:"displayName"`
	RateLimitDaily int     `json:"rateLimitDaily"`
	BudgetMonthly  float64 `json:"budgetMonthly"`
	Active         bool    `json:"active"`
	JoinedAt       string  `json:"joinedAt"`
}

// SharedList represents a shared list (shopping, todo, wishlist).
type SharedList struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ListType  string `json:"listType"`
	CreatedBy string `json:"createdBy"`
	CreatedAt string `json:"createdAt"`
}

// SharedListItem represents an item in a shared list.
type SharedListItem struct {
	ID        int    `json:"id"`
	ListID    string `json:"listId"`
	Text      string `json:"text"`
	Quantity  string `json:"quantity"`
	Checked   bool   `json:"checked"`
	AddedBy   string `json:"addedBy"`
	CreatedAt string `json:"createdAt"`
}

// Service manages multi-user / family mode.
type Service struct {
	db         lifedb.DB
	dbPath     string
	historyDB  string
	familyCfg  Config
}

// New creates and initializes a family Service.
func New(dbPath, historyDB string, familyCfg Config, db lifedb.DB) (*Service, error) {
	svc := &Service{
		db:        db,
		dbPath:    dbPath,
		historyDB: historyDB,
		familyCfg: familyCfg,
	}
	if err := InitDB(dbPath); err != nil {
		return nil, fmt.Errorf("init family DB: %w", err)
	}
	db.LogInfo("family service initialized", "db", dbPath)
	return svc, nil
}

// InitDB creates the family mode database tables.
func InitDB(dbPath string) error {
	sql := `
CREATE TABLE IF NOT EXISTS family_users (
    user_id TEXT PRIMARY KEY,
    role TEXT DEFAULT 'member',
    display_name TEXT DEFAULT '',
    rate_limit_daily INTEGER DEFAULT 100,
    budget_monthly REAL DEFAULT 0,
    active INTEGER DEFAULT 1,
    joined_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS user_permissions (
    user_id TEXT NOT NULL,
    permission TEXT NOT NULL,
    allowed INTEGER DEFAULT 1,
    UNIQUE(user_id, permission)
);

CREATE TABLE IF NOT EXISTS shared_lists (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    list_type TEXT DEFAULT 'shopping',
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS shared_list_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    list_id TEXT NOT NULL,
    text TEXT NOT NULL,
    quantity TEXT DEFAULT '',
    checked INTEGER DEFAULT 0,
    added_by TEXT DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sli_list ON shared_list_items(list_id);
`
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3 init: %s: %w", string(out), err)
	}
	return nil
}

func (f *Service) execSQL(sql string) error {
	return f.db.Exec(f.dbPath, sql)
}

// --- User Management ---

// AddUser adds a new user to the family system.
func (f *Service) AddUser(userID, displayName, role string) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	if role == "" {
		role = "member"
	}
	if role != "admin" && role != "member" && role != "guest" {
		return fmt.Errorf("invalid role: %s (must be admin, member, or guest)", role)
	}

	users, err := f.ListUsers()
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	maxUsers := f.familyCfg.maxUsersOrDefault()
	if len(users) >= maxUsers {
		return fmt.Errorf("max users limit reached (%d)", maxUsers)
	}

	existing, _ := f.getUser(userID, false)
	if existing != nil {
		if !existing.Active {
			sql := fmt.Sprintf(
				`UPDATE family_users SET active = 1, role = '%s', display_name = '%s' WHERE user_id = '%s'`,
				f.db.Escape(role), f.db.Escape(displayName), f.db.Escape(userID))
			return f.execSQL(sql)
		}
		return fmt.Errorf("user %s already exists", userID)
	}

	rateLimit := f.familyCfg.defaultRateLimitOrDefault()
	budget := f.familyCfg.DefaultBudget
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO family_users (user_id, role, display_name, rate_limit_daily, budget_monthly, active, joined_at) VALUES ('%s', '%s', '%s', %d, %f, 1, '%s')`,
		f.db.Escape(userID), f.db.Escape(role), f.db.Escape(displayName),
		rateLimit, budget, f.db.Escape(now))

	return f.execSQL(sql)
}

// RemoveUser soft-deletes a user (sets active=0).
func (f *Service) RemoveUser(userID string) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	sql := fmt.Sprintf(
		`UPDATE family_users SET active = 0 WHERE user_id = '%s' AND active = 1`,
		f.db.Escape(userID))
	return f.execSQL(sql)
}

// GetUser retrieves an active user by ID.
func (f *Service) GetUser(userID string) (*FamilyUser, error) {
	return f.getUser(userID, true)
}

func (f *Service) getUser(userID string, activeOnly bool) (*FamilyUser, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	activeFilter := ""
	if activeOnly {
		activeFilter = " AND active = 1"
	}
	sql := fmt.Sprintf(
		`SELECT user_id, role, display_name, rate_limit_daily, budget_monthly, active, joined_at FROM family_users WHERE user_id = '%s'%s`,
		f.db.Escape(userID), activeFilter)
	rows, err := f.db.Query(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("user not found: %s", userID)
	}
	return rowToFamilyUser(rows[0]), nil
}

// ListUsers returns all active users.
func (f *Service) ListUsers() ([]FamilyUser, error) {
	sql := `SELECT user_id, role, display_name, rate_limit_daily, budget_monthly, active, joined_at FROM family_users WHERE active = 1 ORDER BY joined_at`
	rows, err := f.db.Query(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	users := make([]FamilyUser, 0, len(rows))
	for _, row := range rows {
		users = append(users, *rowToFamilyUser(row))
	}
	return users, nil
}

// UpdateUser updates user fields.
func (f *Service) UpdateUser(userID string, updates map[string]any) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	if len(updates) == 0 {
		return nil
	}

	var sets []string
	for key, val := range updates {
		switch key {
		case "displayName", "display_name":
			sets = append(sets, fmt.Sprintf("display_name = '%s'", f.db.Escape(fmt.Sprintf("%v", val))))
		case "role":
			r := fmt.Sprintf("%v", val)
			if r != "admin" && r != "member" && r != "guest" {
				return fmt.Errorf("invalid role: %s", r)
			}
			sets = append(sets, fmt.Sprintf("role = '%s'", f.db.Escape(r)))
		case "rateLimitDaily", "rate_limit_daily":
			sets = append(sets, fmt.Sprintf("rate_limit_daily = %d", jsonInt(val)))
		case "budgetMonthly", "budget_monthly":
			sets = append(sets, fmt.Sprintf("budget_monthly = %f", jsonFloat(val)))
		default:
			return fmt.Errorf("unknown field: %s", key)
		}
	}

	if len(sets) == 0 {
		return nil
	}

	sql := fmt.Sprintf(
		`UPDATE family_users SET %s WHERE user_id = '%s' AND active = 1`,
		strings.Join(sets, ", "), f.db.Escape(userID))
	return f.execSQL(sql)
}

func rowToFamilyUser(row map[string]any) *FamilyUser {
	active := false
	if jsonInt(row["active"]) == 1 || jsonStr(row["active"]) == "1" {
		active = true
	}
	return &FamilyUser{
		UserID:         jsonStr(row["user_id"]),
		Role:           jsonStr(row["role"]),
		DisplayName:    jsonStr(row["display_name"]),
		RateLimitDaily: jsonInt(row["rate_limit_daily"]),
		BudgetMonthly:  jsonFloat(row["budget_monthly"]),
		Active:         active,
		JoinedAt:       jsonStr(row["joined_at"]),
	}
}

// --- Permissions ---

// GrantPermission grants a permission to a user.
func (f *Service) GrantPermission(userID, permission string) error {
	if userID == "" || permission == "" {
		return fmt.Errorf("user ID and permission are required")
	}
	sql := fmt.Sprintf(
		`INSERT INTO user_permissions (user_id, permission, allowed) VALUES ('%s', '%s', 1) ON CONFLICT(user_id, permission) DO UPDATE SET allowed = 1`,
		f.db.Escape(userID), f.db.Escape(permission))
	return f.execSQL(sql)
}

// RevokePermission revokes a permission from a user.
func (f *Service) RevokePermission(userID, permission string) error {
	if userID == "" || permission == "" {
		return fmt.Errorf("user ID and permission are required")
	}
	sql := fmt.Sprintf(
		`INSERT INTO user_permissions (user_id, permission, allowed) VALUES ('%s', '%s', 0) ON CONFLICT(user_id, permission) DO UPDATE SET allowed = 0`,
		f.db.Escape(userID), f.db.Escape(permission))
	return f.execSQL(sql)
}

// HasPermission checks if a user has a specific permission.
// Admin role has all permissions.
func (f *Service) HasPermission(userID, permission string) (bool, error) {
	if userID == "" || permission == "" {
		return false, fmt.Errorf("user ID and permission are required")
	}

	user, err := f.GetUser(userID)
	if err != nil {
		return false, err
	}
	if user.Role == "admin" {
		return true, nil
	}

	sql := fmt.Sprintf(
		`SELECT allowed FROM user_permissions WHERE user_id = '%s' AND permission = '%s'`,
		f.db.Escape(userID), f.db.Escape(permission))
	rows, err := f.db.Query(f.dbPath, sql)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	return jsonInt(rows[0]["allowed"]) == 1, nil
}

// GetPermissions returns all granted permissions for a user.
func (f *Service) GetPermissions(userID string) ([]string, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	sql := fmt.Sprintf(
		`SELECT permission FROM user_permissions WHERE user_id = '%s' AND allowed = 1 ORDER BY permission`,
		f.db.Escape(userID))
	rows, err := f.db.Query(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	perms := make([]string, 0, len(rows))
	for _, row := range rows {
		perms = append(perms, jsonStr(row["permission"]))
	}
	return perms, nil
}

// --- Rate Limiting ---

// CheckRateLimit checks if a user has remaining daily quota.
// Returns (allowed, remaining, error).
func (f *Service) CheckRateLimit(userID string) (bool, int, error) {
	if userID == "" {
		return false, 0, fmt.Errorf("user ID is required")
	}

	user, err := f.GetUser(userID)
	if err != nil {
		return false, 0, err
	}

	limit := user.RateLimitDaily
	if limit <= 0 {
		return true, -1, nil
	}

	today := time.Now().UTC().Format("2006-01-02")
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM tasks WHERE source = '%s' AND created_at >= '%s'`,
		f.db.Escape(userID), f.db.Escape(today))

	count := 0
	if f.historyDB != "" {
		rows, err := f.db.Query(f.historyDB, sql)
		if err != nil {
			f.db.LogWarn("family rate limit: history query failed", "error", err)
			return true, limit, nil
		}
		if len(rows) > 0 {
			count = jsonInt(rows[0]["cnt"])
		}
	}

	remaining := limit - count
	if remaining < 0 {
		remaining = 0
	}
	return remaining > 0, remaining, nil
}

// --- Shared Lists ---

// CreateList creates a new shared list.
func (f *Service) CreateList(name, listType, createdBy string, newUUID func() string) (*SharedList, error) {
	if name == "" {
		return nil, fmt.Errorf("list name is required")
	}
	if listType == "" {
		listType = "shopping"
	}
	if createdBy == "" {
		createdBy = "default"
	}

	id := newUUID()
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO shared_lists (id, name, list_type, created_by, created_at) VALUES ('%s', '%s', '%s', '%s', '%s')`,
		f.db.Escape(id), f.db.Escape(name), f.db.Escape(listType),
		f.db.Escape(createdBy), f.db.Escape(now))

	if err := f.execSQL(sql); err != nil {
		return nil, err
	}

	return &SharedList{
		ID:        id,
		Name:      name,
		ListType:  listType,
		CreatedBy: createdBy,
		CreatedAt: now,
	}, nil
}

// GetList retrieves a shared list by ID.
func (f *Service) GetList(listID string) (*SharedList, error) {
	if listID == "" {
		return nil, fmt.Errorf("list ID is required")
	}
	sql := fmt.Sprintf(
		`SELECT id, name, list_type, created_by, created_at FROM shared_lists WHERE id = '%s'`,
		f.db.Escape(listID))
	rows, err := f.db.Query(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("list not found: %s", listID)
	}
	return rowToSharedList(rows[0]), nil
}

// ListLists returns all shared lists.
func (f *Service) ListLists() ([]SharedList, error) {
	sql := `SELECT id, name, list_type, created_by, created_at FROM shared_lists ORDER BY created_at`
	rows, err := f.db.Query(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	lists := make([]SharedList, 0, len(rows))
	for _, row := range rows {
		lists = append(lists, *rowToSharedList(row))
	}
	return lists, nil
}

// DeleteList deletes a shared list and its items.
func (f *Service) DeleteList(listID string) error {
	if listID == "" {
		return fmt.Errorf("list ID is required")
	}
	sql := fmt.Sprintf(
		`DELETE FROM shared_list_items WHERE list_id = '%s'; DELETE FROM shared_lists WHERE id = '%s'`,
		f.db.Escape(listID), f.db.Escape(listID))
	return f.execSQL(sql)
}

// AddListItem adds an item to a shared list.
func (f *Service) AddListItem(listID, text, quantity, addedBy string) (*SharedListItem, error) {
	if listID == "" {
		return nil, fmt.Errorf("list ID is required")
	}
	if text == "" {
		return nil, fmt.Errorf("item text is required")
	}
	if addedBy == "" {
		addedBy = "default"
	}
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO shared_list_items (list_id, text, quantity, checked, added_by, created_at) VALUES ('%s', '%s', '%s', 0, '%s', '%s')`,
		f.db.Escape(listID), f.db.Escape(text), f.db.Escape(quantity),
		f.db.Escape(addedBy), f.db.Escape(now))

	if err := f.execSQL(sql); err != nil {
		return nil, err
	}

	idRows, err := f.db.Query(f.dbPath, `SELECT last_insert_rowid() as id`)
	if err != nil {
		return nil, err
	}
	id := 0
	if len(idRows) > 0 {
		id = jsonInt(idRows[0]["id"])
	}
	if id == 0 {
		idRows2, _ := f.db.Query(f.dbPath, fmt.Sprintf(
			`SELECT id FROM shared_list_items WHERE list_id = '%s' AND text = '%s' ORDER BY id DESC LIMIT 1`,
			f.db.Escape(listID), f.db.Escape(text)))
		if len(idRows2) > 0 {
			id = jsonInt(idRows2[0]["id"])
		}
	}

	return &SharedListItem{
		ID:        id,
		ListID:    listID,
		Text:      text,
		Quantity:  quantity,
		Checked:   false,
		AddedBy:   addedBy,
		CreatedAt: now,
	}, nil
}

// CheckItem toggles the checked status of a list item.
func (f *Service) CheckItem(itemID int, checked bool) error {
	val := 0
	if checked {
		val = 1
	}
	sql := fmt.Sprintf(
		`UPDATE shared_list_items SET checked = %d WHERE id = %d`,
		val, itemID)
	return f.execSQL(sql)
}

// RemoveListItem deletes a list item.
func (f *Service) RemoveListItem(itemID int) error {
	sql := fmt.Sprintf(`DELETE FROM shared_list_items WHERE id = %d`, itemID)
	return f.execSQL(sql)
}

// GetListItems returns all items in a list.
func (f *Service) GetListItems(listID string) ([]SharedListItem, error) {
	if listID == "" {
		return nil, fmt.Errorf("list ID is required")
	}
	sql := fmt.Sprintf(
		`SELECT id, list_id, text, quantity, checked, added_by, created_at FROM shared_list_items WHERE list_id = '%s' ORDER BY id`,
		f.db.Escape(listID))
	rows, err := f.db.Query(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	items := make([]SharedListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, *rowToSharedListItem(row))
	}
	return items, nil
}

func rowToSharedList(row map[string]any) *SharedList {
	return &SharedList{
		ID:        jsonStr(row["id"]),
		Name:      jsonStr(row["name"]),
		ListType:  jsonStr(row["list_type"]),
		CreatedBy: jsonStr(row["created_by"]),
		CreatedAt: jsonStr(row["created_at"]),
	}
}

func rowToSharedListItem(row map[string]any) *SharedListItem {
	checked := false
	if jsonInt(row["checked"]) == 1 || jsonStr(row["checked"]) == "1" {
		checked = true
	}
	return &SharedListItem{
		ID:        jsonInt(row["id"]),
		ListID:    jsonStr(row["list_id"]),
		Text:      jsonStr(row["text"]),
		Quantity:  jsonStr(row["quantity"]),
		Checked:   checked,
		AddedBy:   jsonStr(row["added_by"]),
		CreatedAt: jsonStr(row["created_at"]),
	}
}

// --- local helpers ---

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

func jsonInt(v any) int {
	if v == nil {
		return 0
	}
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		var i int
		fmt.Sscanf(x, "%d", &i)
		return i
	}
	return 0
}
