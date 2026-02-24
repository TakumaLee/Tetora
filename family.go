package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// --- Family Mode Config ---

// FamilyConfig holds settings for multi-user / family mode.
type FamilyConfig struct {
	Enabled          bool    `json:"enabled"`
	MaxUsers         int     `json:"maxUsers,omitempty"`         // default 10
	DefaultBudget    float64 `json:"defaultBudget,omitempty"`    // monthly USD, 0=unlimited
	DefaultRateLimit int     `json:"defaultRateLimit,omitempty"` // daily requests, default 100
}

func (c FamilyConfig) maxUsersOrDefault() int {
	if c.MaxUsers > 0 {
		return c.MaxUsers
	}
	return 10
}

func (c FamilyConfig) defaultRateLimitOrDefault() int {
	if c.DefaultRateLimit > 0 {
		return c.DefaultRateLimit
	}
	return 100
}

// --- Types ---

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

// --- Global singleton ---

var globalFamilyService *FamilyService

// --- FamilyService ---

// FamilyService manages multi-user / family mode.
type FamilyService struct {
	dbPath    string
	cfg       *Config
	familyCfg FamilyConfig
}

// newFamilyService creates and initializes a FamilyService.
func newFamilyService(cfg *Config, familyCfg FamilyConfig) (*FamilyService, error) {
	dbPath := filepath.Join(filepath.Dir(cfg.HistoryDB), "family.db")
	fs := &FamilyService{
		dbPath:    dbPath,
		cfg:       cfg,
		familyCfg: familyCfg,
	}
	if err := initFamilyDB(dbPath); err != nil {
		return nil, fmt.Errorf("init family DB: %w", err)
	}
	logInfo("family service initialized", "db", dbPath)
	return fs, nil
}

// --- DB Init ---

// initFamilyDB creates the family mode database tables.
func initFamilyDB(dbPath string) error {
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

// familyExecSQL runs a non-query SQL statement against the family DB.
func familyExecSQL(dbPath, sql string) error {
	cmd := exec.Command("sqlite3", dbPath, sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3: %s: %w", string(out), err)
	}
	return nil
}

// --- User Management ---

// AddUser adds a new user to the family system.
func (f *FamilyService) AddUser(userID, displayName, role string) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	if role == "" {
		role = "member"
	}
	if role != "admin" && role != "member" && role != "guest" {
		return fmt.Errorf("invalid role: %s (must be admin, member, or guest)", role)
	}

	// Check max users.
	users, err := f.ListUsers()
	if err != nil {
		return fmt.Errorf("list users: %w", err)
	}
	maxUsers := f.familyCfg.maxUsersOrDefault()
	if len(users) >= maxUsers {
		return fmt.Errorf("max users limit reached (%d)", maxUsers)
	}

	// Check if user already exists (including inactive).
	existing, _ := f.getUser(userID, false)
	if existing != nil {
		if !existing.Active {
			// Reactivate.
			sql := fmt.Sprintf(
				`UPDATE family_users SET active = 1, role = '%s', display_name = '%s' WHERE user_id = '%s'`,
				escapeSQLite(role), escapeSQLite(displayName), escapeSQLite(userID))
			return familyExecSQL(f.dbPath, sql)
		}
		return fmt.Errorf("user %s already exists", userID)
	}

	rateLimit := f.familyCfg.defaultRateLimitOrDefault()
	budget := f.familyCfg.DefaultBudget
	now := time.Now().UTC().Format(time.RFC3339)

	sql := fmt.Sprintf(
		`INSERT INTO family_users (user_id, role, display_name, rate_limit_daily, budget_monthly, active, joined_at) VALUES ('%s', '%s', '%s', %d, %f, 1, '%s')`,
		escapeSQLite(userID), escapeSQLite(role), escapeSQLite(displayName),
		rateLimit, budget, escapeSQLite(now))

	return familyExecSQL(f.dbPath, sql)
}

// RemoveUser soft-deletes a user (sets active=0).
func (f *FamilyService) RemoveUser(userID string) error {
	if userID == "" {
		return fmt.Errorf("user ID is required")
	}
	sql := fmt.Sprintf(
		`UPDATE family_users SET active = 0 WHERE user_id = '%s' AND active = 1`,
		escapeSQLite(userID))
	return familyExecSQL(f.dbPath, sql)
}

// GetUser retrieves an active user by ID.
func (f *FamilyService) GetUser(userID string) (*FamilyUser, error) {
	return f.getUser(userID, true)
}

// getUser retrieves a user by ID, optionally filtering by active status.
func (f *FamilyService) getUser(userID string, activeOnly bool) (*FamilyUser, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	activeFilter := ""
	if activeOnly {
		activeFilter = " AND active = 1"
	}
	sql := fmt.Sprintf(
		`SELECT user_id, role, display_name, rate_limit_daily, budget_monthly, active, joined_at FROM family_users WHERE user_id = '%s'%s`,
		escapeSQLite(userID), activeFilter)
	rows, err := queryDB(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("user not found: %s", userID)
	}
	return rowToFamilyUser(rows[0]), nil
}

// ListUsers returns all active users.
func (f *FamilyService) ListUsers() ([]FamilyUser, error) {
	sql := `SELECT user_id, role, display_name, rate_limit_daily, budget_monthly, active, joined_at FROM family_users WHERE active = 1 ORDER BY joined_at`
	rows, err := queryDB(f.dbPath, sql)
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
func (f *FamilyService) UpdateUser(userID string, updates map[string]any) error {
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
			sets = append(sets, fmt.Sprintf("display_name = '%s'", escapeSQLite(fmt.Sprintf("%v", val))))
		case "role":
			r := fmt.Sprintf("%v", val)
			if r != "admin" && r != "member" && r != "guest" {
				return fmt.Errorf("invalid role: %s", r)
			}
			sets = append(sets, fmt.Sprintf("role = '%s'", escapeSQLite(r)))
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
		strings.Join(sets, ", "), escapeSQLite(userID))
	return familyExecSQL(f.dbPath, sql)
}

// rowToFamilyUser converts a DB row to a FamilyUser.
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
func (f *FamilyService) GrantPermission(userID, permission string) error {
	if userID == "" || permission == "" {
		return fmt.Errorf("user ID and permission are required")
	}
	sql := fmt.Sprintf(
		`INSERT INTO user_permissions (user_id, permission, allowed) VALUES ('%s', '%s', 1) ON CONFLICT(user_id, permission) DO UPDATE SET allowed = 1`,
		escapeSQLite(userID), escapeSQLite(permission))
	return familyExecSQL(f.dbPath, sql)
}

// RevokePermission revokes a permission from a user.
func (f *FamilyService) RevokePermission(userID, permission string) error {
	if userID == "" || permission == "" {
		return fmt.Errorf("user ID and permission are required")
	}
	sql := fmt.Sprintf(
		`INSERT INTO user_permissions (user_id, permission, allowed) VALUES ('%s', '%s', 0) ON CONFLICT(user_id, permission) DO UPDATE SET allowed = 0`,
		escapeSQLite(userID), escapeSQLite(permission))
	return familyExecSQL(f.dbPath, sql)
}

// HasPermission checks if a user has a specific permission.
// Admin role has all permissions.
func (f *FamilyService) HasPermission(userID, permission string) (bool, error) {
	if userID == "" || permission == "" {
		return false, fmt.Errorf("user ID and permission are required")
	}

	// Check user role first — admins have all permissions.
	user, err := f.GetUser(userID)
	if err != nil {
		return false, err
	}
	if user.Role == "admin" {
		return true, nil
	}

	sql := fmt.Sprintf(
		`SELECT allowed FROM user_permissions WHERE user_id = '%s' AND permission = '%s'`,
		escapeSQLite(userID), escapeSQLite(permission))
	rows, err := queryDB(f.dbPath, sql)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	return jsonInt(rows[0]["allowed"]) == 1, nil
}

// GetPermissions returns all granted permissions for a user.
func (f *FamilyService) GetPermissions(userID string) ([]string, error) {
	if userID == "" {
		return nil, fmt.Errorf("user ID is required")
	}
	sql := fmt.Sprintf(
		`SELECT permission FROM user_permissions WHERE user_id = '%s' AND allowed = 1 ORDER BY permission`,
		escapeSQLite(userID))
	rows, err := queryDB(f.dbPath, sql)
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
func (f *FamilyService) CheckRateLimit(userID string) (bool, int, error) {
	if userID == "" {
		return false, 0, fmt.Errorf("user ID is required")
	}

	user, err := f.GetUser(userID)
	if err != nil {
		return false, 0, err
	}

	limit := user.RateLimitDaily
	if limit <= 0 {
		// 0 or negative means unlimited.
		return true, -1, nil
	}

	// Count today's tasks in history DB.
	today := time.Now().UTC().Format("2006-01-02")
	sql := fmt.Sprintf(
		`SELECT COUNT(*) as cnt FROM tasks WHERE source = '%s' AND created_at >= '%s'`,
		escapeSQLite(userID), escapeSQLite(today))

	count := 0
	if f.cfg != nil && f.cfg.HistoryDB != "" {
		rows, err := queryDB(f.cfg.HistoryDB, sql)
		if err != nil {
			// If history DB query fails, allow by default.
			logWarn("family rate limit: history query failed", "error", err)
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
func (f *FamilyService) CreateList(name, listType, createdBy string) (*SharedList, error) {
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
		escapeSQLite(id), escapeSQLite(name), escapeSQLite(listType),
		escapeSQLite(createdBy), escapeSQLite(now))

	if err := familyExecSQL(f.dbPath, sql); err != nil {
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
func (f *FamilyService) GetList(listID string) (*SharedList, error) {
	if listID == "" {
		return nil, fmt.Errorf("list ID is required")
	}
	sql := fmt.Sprintf(
		`SELECT id, name, list_type, created_by, created_at FROM shared_lists WHERE id = '%s'`,
		escapeSQLite(listID))
	rows, err := queryDB(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("list not found: %s", listID)
	}
	return rowToSharedList(rows[0]), nil
}

// ListLists returns all shared lists.
func (f *FamilyService) ListLists() ([]SharedList, error) {
	sql := `SELECT id, name, list_type, created_by, created_at FROM shared_lists ORDER BY created_at`
	rows, err := queryDB(f.dbPath, sql)
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
func (f *FamilyService) DeleteList(listID string) error {
	if listID == "" {
		return fmt.Errorf("list ID is required")
	}
	sql := fmt.Sprintf(
		`DELETE FROM shared_list_items WHERE list_id = '%s'; DELETE FROM shared_lists WHERE id = '%s'`,
		escapeSQLite(listID), escapeSQLite(listID))
	return familyExecSQL(f.dbPath, sql)
}

// AddListItem adds an item to a shared list.
func (f *FamilyService) AddListItem(listID, text, quantity, addedBy string) (*SharedListItem, error) {
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
		escapeSQLite(listID), escapeSQLite(text), escapeSQLite(quantity),
		escapeSQLite(addedBy), escapeSQLite(now))

	if err := familyExecSQL(f.dbPath, sql); err != nil {
		return nil, err
	}

	// Get the inserted ID.
	idRows, err := queryDB(f.dbPath, `SELECT last_insert_rowid() as id`)
	if err != nil {
		return nil, err
	}
	id := 0
	if len(idRows) > 0 {
		id = jsonInt(idRows[0]["id"])
	}
	// Fallback: query by content if last_insert_rowid returned 0.
	if id == 0 {
		idRows2, _ := queryDB(f.dbPath, fmt.Sprintf(
			`SELECT id FROM shared_list_items WHERE list_id = '%s' AND text = '%s' ORDER BY id DESC LIMIT 1`,
			escapeSQLite(listID), escapeSQLite(text)))
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
func (f *FamilyService) CheckItem(itemID int, checked bool) error {
	val := 0
	if checked {
		val = 1
	}
	sql := fmt.Sprintf(
		`UPDATE shared_list_items SET checked = %d WHERE id = %d`,
		val, itemID)
	return familyExecSQL(f.dbPath, sql)
}

// RemoveListItem deletes a list item.
func (f *FamilyService) RemoveListItem(itemID int) error {
	sql := fmt.Sprintf(`DELETE FROM shared_list_items WHERE id = %d`, itemID)
	return familyExecSQL(f.dbPath, sql)
}

// GetListItems returns all items in a list.
func (f *FamilyService) GetListItems(listID string) ([]SharedListItem, error) {
	if listID == "" {
		return nil, fmt.Errorf("list ID is required")
	}
	sql := fmt.Sprintf(
		`SELECT id, list_id, text, quantity, checked, added_by, created_at FROM shared_list_items WHERE list_id = '%s' ORDER BY id`,
		escapeSQLite(listID))
	rows, err := queryDB(f.dbPath, sql)
	if err != nil {
		return nil, err
	}
	items := make([]SharedListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, *rowToSharedListItem(row))
	}
	return items, nil
}

// rowToSharedList converts a DB row to a SharedList.
func rowToSharedList(row map[string]any) *SharedList {
	return &SharedList{
		ID:        jsonStr(row["id"]),
		Name:      jsonStr(row["name"]),
		ListType:  jsonStr(row["list_type"]),
		CreatedBy: jsonStr(row["created_by"]),
		CreatedAt: jsonStr(row["created_at"]),
	}
}

// rowToSharedListItem converts a DB row to a SharedListItem.
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

// --- Tool Handlers ---

// toolFamilyListAdd adds an item to a shared list.
// Input: {"listId":"...", "text":"...", "quantity":"...", "addedBy":"default"}
func toolFamilyListAdd(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		Text     string `json:"text"`
		Quantity string `json:"quantity"`
		AddedBy  string `json:"addedBy"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Text == "" {
		return "", fmt.Errorf("text is required")
	}
	if args.AddedBy == "" {
		args.AddedBy = "default"
	}

	// If listId not provided, use the first shopping list or create one.
	if args.ListID == "" {
		lists, err := globalFamilyService.ListLists()
		if err != nil {
			return "", err
		}
		for _, l := range lists {
			if l.ListType == "shopping" {
				args.ListID = l.ID
				break
			}
		}
		if args.ListID == "" {
			list, err := globalFamilyService.CreateList("Shopping", "shopping", args.AddedBy)
			if err != nil {
				return "", fmt.Errorf("create default shopping list: %w", err)
			}
			args.ListID = list.ID
		}
	}

	item, err := globalFamilyService.AddListItem(args.ListID, args.Text, args.Quantity, args.AddedBy)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(map[string]any{
		"status": "added",
		"item":   item,
	})
	return string(b), nil
}

// toolFamilyListView lists shared lists or items.
// Input: {"listId":"...", "listType":"shopping"}
func toolFamilyListView(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		ListID   string `json:"listId"`
		ListType string `json:"listType"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	// If listId provided, show items for that list.
	if args.ListID != "" {
		items, err := globalFamilyService.GetListItems(args.ListID)
		if err != nil {
			return "", err
		}
		list, _ := globalFamilyService.GetList(args.ListID)
		result := map[string]any{
			"items": items,
		}
		if list != nil {
			result["list"] = list
		}
		b, _ := json.Marshal(result)
		return string(b), nil
	}

	// Otherwise, show all lists (optionally filtered by type).
	lists, err := globalFamilyService.ListLists()
	if err != nil {
		return "", err
	}
	if args.ListType != "" {
		var filtered []SharedList
		for _, l := range lists {
			if l.ListType == args.ListType {
				filtered = append(filtered, l)
			}
		}
		lists = filtered
	}

	b, _ := json.Marshal(map[string]any{"lists": lists})
	return string(b), nil
}

// toolUserSwitch switches the active user context.
// Input: {"userId":"..."}
func toolUserSwitch(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.UserID == "" {
		return "", fmt.Errorf("userId is required")
	}

	user, err := globalFamilyService.GetUser(args.UserID)
	if err != nil {
		return "", fmt.Errorf("user not found or inactive: %w", err)
	}

	// Check rate limit.
	allowed, remaining, _ := globalFamilyService.CheckRateLimit(args.UserID)

	perms, _ := globalFamilyService.GetPermissions(args.UserID)

	b, _ := json.Marshal(map[string]any{
		"status":      "switched",
		"user":        user,
		"permissions": perms,
		"rateLimit": map[string]any{
			"allowed":   allowed,
			"remaining": remaining,
		},
	})
	return string(b), nil
}

// toolFamilyManage is a multipurpose family management tool.
// Input: {"action":"add|remove|list|update|permissions", "userId":"...", "displayName":"...", "role":"member", "permission":"...", "grant":true}
func toolFamilyManage(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalFamilyService == nil {
		return "", fmt.Errorf("family mode not enabled")
	}

	var args struct {
		Action      string  `json:"action"`
		UserID      string  `json:"userId"`
		DisplayName string  `json:"displayName"`
		Role        string  `json:"role"`
		Permission  string  `json:"permission"`
		Grant       bool    `json:"grant"`
		RateLimit   int     `json:"rateLimit"`
		Budget      float64 `json:"budget"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	switch args.Action {
	case "add":
		if args.Role == "" {
			args.Role = "member"
		}
		if err := globalFamilyService.AddUser(args.UserID, args.DisplayName, args.Role); err != nil {
			return "", err
		}
		user, _ := globalFamilyService.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "added", "user": user})
		return string(b), nil

	case "remove":
		if err := globalFamilyService.RemoveUser(args.UserID); err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"status": "removed", "userId": args.UserID})
		return string(b), nil

	case "list":
		users, err := globalFamilyService.ListUsers()
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"users": users})
		return string(b), nil

	case "update":
		updates := make(map[string]any)
		if args.DisplayName != "" {
			updates["displayName"] = args.DisplayName
		}
		if args.Role != "" {
			updates["role"] = args.Role
		}
		if args.RateLimit > 0 {
			updates["rateLimitDaily"] = float64(args.RateLimit)
		}
		if args.Budget > 0 {
			updates["budgetMonthly"] = args.Budget
		}
		if err := globalFamilyService.UpdateUser(args.UserID, updates); err != nil {
			return "", err
		}
		user, _ := globalFamilyService.GetUser(args.UserID)
		b, _ := json.Marshal(map[string]any{"status": "updated", "user": user})
		return string(b), nil

	case "permissions":
		if args.Permission != "" {
			if args.Grant {
				if err := globalFamilyService.GrantPermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			} else {
				if err := globalFamilyService.RevokePermission(args.UserID, args.Permission); err != nil {
					return "", err
				}
			}
		}
		perms, err := globalFamilyService.GetPermissions(args.UserID)
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(map[string]any{"userId": args.UserID, "permissions": perms})
		return string(b), nil

	default:
		return "", fmt.Errorf("unknown action: %s (use add, remove, list, update, or permissions)", args.Action)
	}
}
