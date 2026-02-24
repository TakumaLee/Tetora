package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// newTestFamilyService creates a FamilyService with a temp DB for testing.
func newTestFamilyService(t *testing.T) *FamilyService {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "family_test.db")
	if err := initFamilyDB(dbPath); err != nil {
		t.Fatalf("initFamilyDB: %v", err)
	}
	return &FamilyService{
		dbPath: dbPath,
		cfg:    &Config{},
		familyCfg: FamilyConfig{
			Enabled:          true,
			MaxUsers:         10,
			DefaultBudget:    0,
			DefaultRateLimit: 100,
		},
	}
}

func TestInitFamilyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "family_init.db")
	if err := initFamilyDB(dbPath); err != nil {
		t.Fatalf("initFamilyDB failed: %v", err)
	}
	// Calling again should be idempotent.
	if err := initFamilyDB(dbPath); err != nil {
		t.Fatalf("initFamilyDB idempotent failed: %v", err)
	}

	// Verify tables exist.
	rows, err := queryDB(dbPath, `SELECT name FROM sqlite_master WHERE type='table' ORDER BY name`)
	if err != nil {
		t.Fatalf("queryDB: %v", err)
	}
	tableNames := make(map[string]bool)
	for _, row := range rows {
		tableNames[jsonStr(row["name"])] = true
	}
	for _, expected := range []string{"family_users", "user_permissions", "shared_lists", "shared_list_items"} {
		if !tableNames[expected] {
			t.Errorf("expected table %s to exist", expected)
		}
	}
}

func TestAddUser(t *testing.T) {
	fs := newTestFamilyService(t)

	// Add a member.
	if err := fs.AddUser("user1", "Alice", "member"); err != nil {
		t.Fatalf("AddUser: %v", err)
	}

	// Add an admin.
	if err := fs.AddUser("admin1", "Bob", "admin"); err != nil {
		t.Fatalf("AddUser admin: %v", err)
	}

	// Duplicate should fail.
	err := fs.AddUser("user1", "Alice2", "member")
	if err == nil {
		t.Fatal("expected error for duplicate user")
	}

	// Invalid role.
	err = fs.AddUser("user2", "Charlie", "superuser")
	if err == nil {
		t.Fatal("expected error for invalid role")
	}

	// Empty user ID.
	err = fs.AddUser("", "Nobody", "member")
	if err == nil {
		t.Fatal("expected error for empty user ID")
	}
}

func TestAddUserMaxLimit(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.familyCfg.MaxUsers = 3

	for i := 0; i < 3; i++ {
		if err := fs.AddUser(jsonStr(i), "User", "member"); err != nil {
			t.Fatalf("AddUser %d: %v", i, err)
		}
	}
	// 4th should fail.
	err := fs.AddUser("extra", "Extra", "member")
	if err == nil {
		t.Fatal("expected max users error")
	}
	if !strings.Contains(err.Error(), "max users") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRemoveUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	if err := fs.RemoveUser("user1"); err != nil {
		t.Fatalf("RemoveUser: %v", err)
	}

	// User should no longer appear in active list.
	users, _ := fs.ListUsers()
	for _, u := range users {
		if u.UserID == "user1" {
			t.Fatal("removed user should not appear in active list")
		}
	}

	// GetUser should fail for active-only.
	_, err := fs.GetUser("user1")
	if err == nil {
		t.Fatal("expected error for removed user")
	}

	// Remove empty user ID.
	if err := fs.RemoveUser(""); err == nil {
		t.Fatal("expected error for empty user ID")
	}
}

func TestRemoveAndReaddUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.RemoveUser("user1")

	// Re-adding should reactivate.
	if err := fs.AddUser("user1", "Alice Reactivated", "admin"); err != nil {
		t.Fatalf("re-add user: %v", err)
	}

	user, err := fs.GetUser("user1")
	if err != nil {
		t.Fatalf("GetUser after reactivation: %v", err)
	}
	if user.Role != "admin" {
		t.Errorf("expected role admin, got %s", user.Role)
	}
	if !user.Active {
		t.Error("expected user to be active")
	}
}

func TestGetUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	user, err := fs.GetUser("user1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if user.UserID != "user1" {
		t.Errorf("expected user1, got %s", user.UserID)
	}
	if user.DisplayName != "Alice" {
		t.Errorf("expected Alice, got %s", user.DisplayName)
	}
	if user.Role != "member" {
		t.Errorf("expected member, got %s", user.Role)
	}
	if !user.Active {
		t.Error("expected active user")
	}

	// Non-existent user.
	_, err = fs.GetUser("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestListUsers(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.AddUser("user2", "Bob", "admin")
	fs.AddUser("user3", "Charlie", "guest")

	users, err := fs.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 3 {
		t.Fatalf("expected 3 users, got %d", len(users))
	}

	// Remove one and verify count.
	fs.RemoveUser("user2")
	users, _ = fs.ListUsers()
	if len(users) != 2 {
		t.Errorf("expected 2 users after removal, got %d", len(users))
	}
}

func TestUpdateUser(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	// Update display name.
	if err := fs.UpdateUser("user1", map[string]any{"displayName": "Alice Updated"}); err != nil {
		t.Fatalf("UpdateUser displayName: %v", err)
	}
	user, _ := fs.GetUser("user1")
	if user.DisplayName != "Alice Updated" {
		t.Errorf("expected 'Alice Updated', got %s", user.DisplayName)
	}

	// Update role.
	if err := fs.UpdateUser("user1", map[string]any{"role": "admin"}); err != nil {
		t.Fatalf("UpdateUser role: %v", err)
	}
	user, _ = fs.GetUser("user1")
	if user.Role != "admin" {
		t.Errorf("expected admin, got %s", user.Role)
	}

	// Invalid role.
	if err := fs.UpdateUser("user1", map[string]any{"role": "superuser"}); err == nil {
		t.Fatal("expected error for invalid role")
	}

	// Unknown field.
	if err := fs.UpdateUser("user1", map[string]any{"unknown": "value"}); err == nil {
		t.Fatal("expected error for unknown field")
	}

	// Update rate limit and budget.
	if err := fs.UpdateUser("user1", map[string]any{
		"rateLimitDaily": float64(200),
		"budgetMonthly":  50.0,
	}); err != nil {
		t.Fatalf("UpdateUser rateLimit/budget: %v", err)
	}
	user, _ = fs.GetUser("user1")
	if user.RateLimitDaily != 200 {
		t.Errorf("expected rate limit 200, got %d", user.RateLimitDaily)
	}

	// Empty updates should be fine.
	if err := fs.UpdateUser("user1", map[string]any{}); err != nil {
		t.Fatalf("UpdateUser empty: %v", err)
	}
}

func TestGrantPermission(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	if err := fs.GrantPermission("user1", "task.write"); err != nil {
		t.Fatalf("GrantPermission: %v", err)
	}

	// Grant again (idempotent).
	if err := fs.GrantPermission("user1", "task.write"); err != nil {
		t.Fatalf("GrantPermission idempotent: %v", err)
	}

	// Empty args.
	if err := fs.GrantPermission("", "task.write"); err == nil {
		t.Fatal("expected error for empty user ID")
	}
	if err := fs.GrantPermission("user1", ""); err == nil {
		t.Fatal("expected error for empty permission")
	}
}

func TestRevokePermission(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.GrantPermission("user1", "task.write")

	if err := fs.RevokePermission("user1", "task.write"); err != nil {
		t.Fatalf("RevokePermission: %v", err)
	}

	has, err := fs.HasPermission("user1", "task.write")
	if err != nil {
		t.Fatalf("HasPermission: %v", err)
	}
	if has {
		t.Error("expected permission to be revoked")
	}
}

func TestHasPermission(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")
	fs.AddUser("admin1", "Bob", "admin")

	// Member without permission.
	has, err := fs.HasPermission("user1", "task.write")
	if err != nil {
		t.Fatalf("HasPermission: %v", err)
	}
	if has {
		t.Error("expected no permission for member without grant")
	}

	// Grant and check.
	fs.GrantPermission("user1", "task.write")
	has, _ = fs.HasPermission("user1", "task.write")
	if !has {
		t.Error("expected permission after grant")
	}

	// Admin has all permissions.
	has, _ = fs.HasPermission("admin1", "anything.at.all")
	if !has {
		t.Error("expected admin to have all permissions")
	}

	// Non-existent user.
	_, err = fs.HasPermission("nonexistent", "task.write")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}
}

func TestGetPermissions(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	// No permissions initially.
	perms, err := fs.GetPermissions("user1")
	if err != nil {
		t.Fatalf("GetPermissions: %v", err)
	}
	if len(perms) != 0 {
		t.Errorf("expected 0 permissions, got %d", len(perms))
	}

	// Grant some.
	fs.GrantPermission("user1", "task.write")
	fs.GrantPermission("user1", "expense.read")
	perms, _ = fs.GetPermissions("user1")
	if len(perms) != 2 {
		t.Errorf("expected 2 permissions, got %d", len(perms))
	}

	// Revoke one.
	fs.RevokePermission("user1", "task.write")
	perms, _ = fs.GetPermissions("user1")
	if len(perms) != 1 {
		t.Errorf("expected 1 permission, got %d", len(perms))
	}
}

func TestCheckRateLimit(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.AddUser("user1", "Alice", "member")

	// No history DB configured — should allow.
	allowed, remaining, err := fs.CheckRateLimit("user1")
	if err != nil {
		t.Fatalf("CheckRateLimit: %v", err)
	}
	if !allowed {
		t.Error("expected allowed with no history")
	}
	if remaining != 100 {
		t.Errorf("expected 100 remaining, got %d", remaining)
	}

	// Non-existent user.
	_, _, err = fs.CheckRateLimit("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}

	// User with 0 rate limit (unlimited).
	fs.UpdateUser("user1", map[string]any{"rateLimitDaily": float64(0)})
	allowed, remaining, _ = fs.CheckRateLimit("user1")
	if !allowed {
		t.Error("expected unlimited user to be allowed")
	}
	if remaining != -1 {
		t.Errorf("expected -1 for unlimited, got %d", remaining)
	}
}

func TestCreateList(t *testing.T) {
	fs := newTestFamilyService(t)

	list, err := fs.CreateList("Groceries", "shopping", "user1")
	if err != nil {
		t.Fatalf("CreateList: %v", err)
	}
	if list.Name != "Groceries" {
		t.Errorf("expected Groceries, got %s", list.Name)
	}
	if list.ListType != "shopping" {
		t.Errorf("expected shopping, got %s", list.ListType)
	}
	if list.ID == "" {
		t.Error("expected non-empty list ID")
	}

	// Empty name.
	_, err = fs.CreateList("", "shopping", "user1")
	if err == nil {
		t.Fatal("expected error for empty name")
	}

	// Default type.
	list2, err := fs.CreateList("Tasks", "", "user1")
	if err != nil {
		t.Fatalf("CreateList default type: %v", err)
	}
	if list2.ListType != "shopping" {
		t.Errorf("expected default type shopping, got %s", list2.ListType)
	}
}

func TestGetList(t *testing.T) {
	fs := newTestFamilyService(t)
	created, _ := fs.CreateList("Groceries", "shopping", "user1")

	got, err := fs.GetList(created.ID)
	if err != nil {
		t.Fatalf("GetList: %v", err)
	}
	if got.Name != "Groceries" {
		t.Errorf("expected Groceries, got %s", got.Name)
	}

	// Non-existent.
	_, err = fs.GetList("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent list")
	}
}

func TestListLists(t *testing.T) {
	fs := newTestFamilyService(t)
	fs.CreateList("List1", "shopping", "user1")
	fs.CreateList("List2", "todo", "user1")

	lists, err := fs.ListLists()
	if err != nil {
		t.Fatalf("ListLists: %v", err)
	}
	if len(lists) != 2 {
		t.Errorf("expected 2 lists, got %d", len(lists))
	}
}

func TestDeleteList(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("ToDelete", "shopping", "user1")
	fs.AddListItem(list.ID, "Milk", "1L", "user1")

	if err := fs.DeleteList(list.ID); err != nil {
		t.Fatalf("DeleteList: %v", err)
	}

	// Should be gone.
	_, err := fs.GetList(list.ID)
	if err == nil {
		t.Fatal("expected error for deleted list")
	}

	// Items should also be gone.
	items, _ := fs.GetListItems(list.ID)
	if len(items) != 0 {
		t.Errorf("expected 0 items after delete, got %d", len(items))
	}
}

func TestAddListItem(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1")

	item, err := fs.AddListItem(list.ID, "Milk", "2L", "user1")
	if err != nil {
		t.Fatalf("AddListItem: %v", err)
	}
	if item.Text != "Milk" {
		t.Errorf("expected Milk, got %s", item.Text)
	}
	if item.Quantity != "2L" {
		t.Errorf("expected 2L, got %s", item.Quantity)
	}
	if item.Checked {
		t.Error("expected unchecked")
	}

	// Empty text.
	_, err = fs.AddListItem(list.ID, "", "", "user1")
	if err == nil {
		t.Fatal("expected error for empty text")
	}

	// Empty list ID.
	_, err = fs.AddListItem("", "Eggs", "", "user1")
	if err == nil {
		t.Fatal("expected error for empty list ID")
	}
}

func TestCheckItem(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1")
	item, _ := fs.AddListItem(list.ID, "Milk", "1L", "user1")

	// Check.
	if err := fs.CheckItem(item.ID, true); err != nil {
		t.Fatalf("CheckItem true: %v", err)
	}
	items, _ := fs.GetListItems(list.ID)
	if len(items) == 0 {
		t.Fatal("expected at least one item")
	}
	if !items[0].Checked {
		t.Error("expected item to be checked")
	}

	// Uncheck.
	if err := fs.CheckItem(item.ID, false); err != nil {
		t.Fatalf("CheckItem false: %v", err)
	}
	items, _ = fs.GetListItems(list.ID)
	if items[0].Checked {
		t.Error("expected item to be unchecked")
	}
}

func TestRemoveListItem(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1")
	item, _ := fs.AddListItem(list.ID, "Milk", "1L", "user1")

	if err := fs.RemoveListItem(item.ID); err != nil {
		t.Fatalf("RemoveListItem: %v", err)
	}

	items, _ := fs.GetListItems(list.ID)
	if len(items) != 0 {
		t.Errorf("expected 0 items after removal, got %d", len(items))
	}
}

func TestGetListItems(t *testing.T) {
	fs := newTestFamilyService(t)
	list, _ := fs.CreateList("Groceries", "shopping", "user1")
	fs.AddListItem(list.ID, "Milk", "1L", "user1")
	fs.AddListItem(list.ID, "Eggs", "12", "user2")
	fs.AddListItem(list.ID, "Bread", "", "user1")

	items, err := fs.GetListItems(list.ID)
	if err != nil {
		t.Fatalf("GetListItems: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
	if items[0].Text != "Milk" {
		t.Errorf("expected Milk first, got %s", items[0].Text)
	}
	if items[1].AddedBy != "user2" {
		t.Errorf("expected user2 for Eggs, got %s", items[1].AddedBy)
	}
}

func TestToolFamilyListAdd(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	// Add without listId — should create default shopping list.
	input, _ := json.Marshal(map[string]any{"text": "Apples", "quantity": "3"})
	result, err := toolFamilyListAdd(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolFamilyListAdd: %v", err)
	}
	if !strings.Contains(result, "added") {
		t.Errorf("expected 'added' in result, got: %s", result)
	}

	// View to check it's there.
	viewInput, _ := json.Marshal(map[string]any{"listType": "shopping"})
	viewResult, err := toolFamilyListView(ctx, cfg, viewInput)
	if err != nil {
		t.Fatalf("toolFamilyListView: %v", err)
	}
	if !strings.Contains(viewResult, "Shopping") {
		t.Errorf("expected 'Shopping' list in result, got: %s", viewResult)
	}

	// Parse the list ID from the created list.
	lists, _ := fs.ListLists()
	if len(lists) == 0 {
		t.Fatal("expected at least one list")
	}

	// Add with explicit listId.
	input2, _ := json.Marshal(map[string]any{"listId": lists[0].ID, "text": "Bananas"})
	result2, err := toolFamilyListAdd(ctx, cfg, input2)
	if err != nil {
		t.Fatalf("toolFamilyListAdd with listId: %v", err)
	}
	if !strings.Contains(result2, "Bananas") {
		t.Errorf("expected 'Bananas' in result, got: %s", result2)
	}

	// Missing text.
	input3, _ := json.Marshal(map[string]any{"listId": lists[0].ID})
	_, err = toolFamilyListAdd(ctx, cfg, input3)
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

func TestToolFamilyListView(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	// Create lists.
	list1, _ := fs.CreateList("Groceries", "shopping", "user1")
	fs.CreateList("Tasks", "todo", "user1")
	fs.AddListItem(list1.ID, "Milk", "1L", "user1")

	// View all lists.
	input, _ := json.Marshal(map[string]any{})
	result, err := toolFamilyListView(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolFamilyListView all: %v", err)
	}
	if !strings.Contains(result, "Groceries") || !strings.Contains(result, "Tasks") {
		t.Errorf("expected both lists in result, got: %s", result)
	}

	// View by type.
	input2, _ := json.Marshal(map[string]any{"listType": "todo"})
	result2, err := toolFamilyListView(ctx, cfg, input2)
	if err != nil {
		t.Fatalf("toolFamilyListView by type: %v", err)
	}
	if !strings.Contains(result2, "Tasks") {
		t.Errorf("expected Tasks in result, got: %s", result2)
	}
	if strings.Contains(result2, "Groceries") {
		t.Errorf("did not expect Groceries in todo filter, got: %s", result2)
	}

	// View specific list items.
	input3, _ := json.Marshal(map[string]any{"listId": list1.ID})
	result3, err := toolFamilyListView(ctx, cfg, input3)
	if err != nil {
		t.Fatalf("toolFamilyListView items: %v", err)
	}
	if !strings.Contains(result3, "Milk") {
		t.Errorf("expected Milk in items, got: %s", result3)
	}
}

func TestToolUserSwitch(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	fs.AddUser("user1", "Alice", "member")
	fs.GrantPermission("user1", "task.write")

	input, _ := json.Marshal(map[string]any{"userId": "user1"})
	result, err := toolUserSwitch(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolUserSwitch: %v", err)
	}
	if !strings.Contains(result, "switched") {
		t.Errorf("expected 'switched' in result, got: %s", result)
	}
	if !strings.Contains(result, "Alice") {
		t.Errorf("expected 'Alice' in result, got: %s", result)
	}
	if !strings.Contains(result, "task.write") {
		t.Errorf("expected 'task.write' in result, got: %s", result)
	}

	// Non-existent user.
	input2, _ := json.Marshal(map[string]any{"userId": "nonexistent"})
	_, err = toolUserSwitch(ctx, cfg, input2)
	if err == nil {
		t.Fatal("expected error for nonexistent user")
	}

	// Empty userId.
	input3, _ := json.Marshal(map[string]any{})
	_, err = toolUserSwitch(ctx, cfg, input3)
	if err == nil {
		t.Fatal("expected error for empty userId")
	}
}

func TestToolFamilyManage(t *testing.T) {
	fs := newTestFamilyService(t)
	oldGlobal := globalFamilyService
	globalFamilyService = fs
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()

	// Add user.
	input, _ := json.Marshal(map[string]any{"action": "add", "userId": "user1", "displayName": "Alice", "role": "member"})
	result, err := toolFamilyManage(ctx, cfg, input)
	if err != nil {
		t.Fatalf("toolFamilyManage add: %v", err)
	}
	if !strings.Contains(result, "added") {
		t.Errorf("expected 'added' in result, got: %s", result)
	}

	// List users.
	input2, _ := json.Marshal(map[string]any{"action": "list"})
	result2, err := toolFamilyManage(ctx, cfg, input2)
	if err != nil {
		t.Fatalf("toolFamilyManage list: %v", err)
	}
	if !strings.Contains(result2, "Alice") {
		t.Errorf("expected 'Alice' in list result, got: %s", result2)
	}

	// Update user.
	input3, _ := json.Marshal(map[string]any{"action": "update", "userId": "user1", "displayName": "Alice Updated"})
	result3, err := toolFamilyManage(ctx, cfg, input3)
	if err != nil {
		t.Fatalf("toolFamilyManage update: %v", err)
	}
	if !strings.Contains(result3, "updated") {
		t.Errorf("expected 'updated' in result, got: %s", result3)
	}

	// Permissions: grant.
	input4, _ := json.Marshal(map[string]any{"action": "permissions", "userId": "user1", "permission": "task.write", "grant": true})
	result4, err := toolFamilyManage(ctx, cfg, input4)
	if err != nil {
		t.Fatalf("toolFamilyManage permissions grant: %v", err)
	}
	if !strings.Contains(result4, "task.write") {
		t.Errorf("expected 'task.write' in result, got: %s", result4)
	}

	// Permissions: revoke.
	input5, _ := json.Marshal(map[string]any{"action": "permissions", "userId": "user1", "permission": "task.write", "grant": false})
	_, err = toolFamilyManage(ctx, cfg, input5)
	if err != nil {
		t.Fatalf("toolFamilyManage permissions revoke: %v", err)
	}

	// Remove user.
	input6, _ := json.Marshal(map[string]any{"action": "remove", "userId": "user1"})
	result6, err := toolFamilyManage(ctx, cfg, input6)
	if err != nil {
		t.Fatalf("toolFamilyManage remove: %v", err)
	}
	if !strings.Contains(result6, "removed") {
		t.Errorf("expected 'removed' in result, got: %s", result6)
	}

	// Unknown action.
	input7, _ := json.Marshal(map[string]any{"action": "unknown"})
	_, err = toolFamilyManage(ctx, cfg, input7)
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

func TestToolFamilyNotEnabled(t *testing.T) {
	oldGlobal := globalFamilyService
	globalFamilyService = nil
	defer func() { globalFamilyService = oldGlobal }()

	cfg := &Config{}
	ctx := context.Background()
	input, _ := json.Marshal(map[string]any{})

	if _, err := toolFamilyListAdd(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
	if _, err := toolFamilyListView(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
	if _, err := toolUserSwitch(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
	if _, err := toolFamilyManage(ctx, cfg, input); err == nil {
		t.Fatal("expected error when family not enabled")
	}
}

func TestFamilyConfigDefaults(t *testing.T) {
	c := FamilyConfig{}
	if c.maxUsersOrDefault() != 10 {
		t.Errorf("expected default maxUsers 10, got %d", c.maxUsersOrDefault())
	}
	if c.defaultRateLimitOrDefault() != 100 {
		t.Errorf("expected default rateLimit 100, got %d", c.defaultRateLimitOrDefault())
	}

	c2 := FamilyConfig{MaxUsers: 5, DefaultRateLimit: 50}
	if c2.maxUsersOrDefault() != 5 {
		t.Errorf("expected maxUsers 5, got %d", c2.maxUsersOrDefault())
	}
	if c2.defaultRateLimitOrDefault() != 50 {
		t.Errorf("expected rateLimit 50, got %d", c2.defaultRateLimitOrDefault())
	}
}
