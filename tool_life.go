package main

import (
	"encoding/json"
)

// registerLifeTools registers life management tools (tasks, expenses, contacts,
// habits, goals, briefing, insights, scheduling, lifecycle, quick capture, time tracking).
// Note: the tool handler functions are defined in their own files
// (goals.go, contacts.go, habits.go, etc.), not here.
func registerLifeTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	// --- P23.2: Task Management Tools ---
	if enabled("task_create") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_create",
			Description: "Create a personal task with optional project, priority, due date, tags, and subtask decomposition",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Task title (required)"},
					"description": {"type": "string", "description": "Task description"},
					"project": {"type": "string", "description": "Project name (default: inbox)"},
					"priority": {"type": "number", "description": "Priority 1-4 (1=urgent, 4=low, default 2)"},
					"dueAt": {"type": "string", "description": "Due date/time (RFC3339 or YYYY-MM-DD)"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"},
					"decompose": {"type": "boolean", "description": "If true, also create subtasks"},
					"subtasks": {"type": "array", "items": {"type": "string"}, "description": "Subtask titles (used when decompose=true)"}
				},
				"required": ["title"]
			}`),
			Handler: toolTaskCreate,
			Builtin: true,
		})
	}
	if enabled("task_list") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_list",
			Description: "List personal tasks with optional filtering by status, project, priority, due date, or tag",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "description": "Filter by status (todo, in_progress, done, cancelled)"},
					"project": {"type": "string", "description": "Filter by project name"},
					"priority": {"type": "number", "description": "Filter by priority (1-4)"},
					"dueDate": {"type": "string", "description": "Filter tasks due before this date"},
					"tag": {"type": "string", "description": "Filter by tag"},
					"limit": {"type": "number", "description": "Max results (default 50)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolTaskList,
			Builtin: true,
		})
	}
	if enabled("task_complete") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_complete",
			Description: "Mark a task as done (also completes all subtasks)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"taskId": {"type": "string", "description": "Task ID to complete (required)"}
				},
				"required": ["taskId"]
			}`),
			Handler: toolTaskComplete,
			Builtin: true,
		})
	}
	if enabled("task_review") && cfg.TaskManager.Enabled {
		r.Register(&ToolDef{
			Name:        "task_review",
			Description: "Generate a task review summary for daily or weekly periods",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Review period: 'daily' or 'weekly' (default: daily)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolTaskReview,
			Builtin: true,
		})
	}
	if enabled("todoist_sync") && cfg.TaskManager.Todoist.Enabled {
		r.Register(&ToolDef{
			Name:        "todoist_sync",
			Description: "Sync tasks with Todoist (pull, push, or full bidirectional sync)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: 'pull', 'push', or 'sync' (default: sync)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolTodoistSync,
			Builtin: true,
		})
	}
	if enabled("notion_sync") && cfg.TaskManager.Notion.Enabled {
		r.Register(&ToolDef{
			Name:        "notion_sync",
			Description: "Sync tasks with a Notion database (pull, push, or full bidirectional sync)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: 'pull', 'push', or 'sync' (default: sync)"},
					"userId": {"type": "string", "description": "User ID (default: 'default')"}
				}
			}`),
			Handler: toolNotionSync,
			Builtin: true,
		})
	}

	// --- P23.4: Financial Tracking Tools ---
	if enabled("expense_add") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "expense_add",
			Description: "Record an expense using natural language or explicit fields (e.g. '午餐 350 元', 'coffee $5.50')",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Natural language expense (e.g. '午餐 350 元', 'coffee $5.50')"},
					"amount": {"type": "number", "description": "Expense amount (optional if using text)"},
					"currency": {"type": "string", "description": "Currency code (e.g. TWD, USD, JPY)"},
					"category": {"type": "string", "description": "Category (food, transport, shopping, etc.)"},
					"description": {"type": "string", "description": "Expense description"},
					"userId": {"type": "string", "description": "User ID (optional, defaults to 'default')"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags for the expense"}
				}
			}`),
			Handler: toolExpenseAdd,
			Builtin: true,
		})
	}
	if enabled("expense_report") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "expense_report",
			Description: "Generate an expense report for a period (today, week, month, year)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Report period: today, week, month, year (default: month)"},
					"category": {"type": "string", "description": "Filter by category (optional)"},
					"currency": {"type": "string", "description": "Report currency (optional)"},
					"userId": {"type": "string", "description": "User ID (optional)"}
				}
			}`),
			Handler: toolExpenseReport,
			Builtin: true,
		})
	}
	if enabled("expense_budget") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "expense_budget",
			Description: "Manage monthly budgets per category (set/list/check)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: set, list, check"},
					"category": {"type": "string", "description": "Budget category (required for set)"},
					"limit": {"type": "number", "description": "Monthly limit (required for set)"},
					"currency": {"type": "string", "description": "Currency (optional)"},
					"userId": {"type": "string", "description": "User ID (optional)"}
				},
				"required": ["action"]
			}`),
			Handler: toolExpenseBudget,
			Builtin: true,
		})
	}
	if enabled("price_watch") && cfg.Finance.Enabled {
		r.Register(&ToolDef{
			Name:        "price_watch",
			Description: "Monitor currency exchange rates with alerts (add/list/cancel)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: add, list, cancel"},
					"from": {"type": "string", "description": "From currency code (e.g. USD)"},
					"to": {"type": "string", "description": "To currency code (e.g. JPY)"},
					"condition": {"type": "string", "description": "Condition: lt (less than) or gt (greater than)"},
					"threshold": {"type": "number", "description": "Price threshold to trigger alert"},
					"id": {"type": "number", "description": "Watch ID (for cancel)"},
					"userId": {"type": "string", "description": "User ID (optional)"},
					"notifyChannel": {"type": "string", "description": "Notification channel (optional)"}
				},
				"required": ["action"]
			}`),
			Handler: toolPriceWatch,
			Builtin: true,
		})
	}

	// --- P24.2: Contact & Social Graph Tools ---
	if enabled("contact_add") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_add",
			Description: "Add or update a contact with cross-channel identifiers, birthday, notes",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Contact display name"},
					"channel": {"type": "string", "description": "Channel (discord, line, telegram, etc.)"},
					"channelId": {"type": "string", "description": "Channel-specific user ID"},
					"birthday": {"type": "string", "description": "Birthday (YYYY-MM-DD or MM-DD)"},
					"notes": {"type": "string", "description": "Notes about this contact"},
					"tags": {"type": "string", "description": "Comma-separated tags"}
				},
				"required": ["name"]
			}`),
			Handler: toolContactAdd,
			Builtin: true,
		})
	}
	if enabled("contact_search") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_search",
			Description: "Search contacts by name, tag, or channel",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (name, tag, or channel)"},
					"limit": {"type": "integer", "description": "Max results (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolContactSearch,
			Builtin: true,
		})
	}
	if enabled("contact_list") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_list",
			Description: "List all contacts, optionally filtered by tag",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"tag": {"type": "string", "description": "Filter by tag (optional)"},
					"limit": {"type": "integer", "description": "Max results (default 20)"}
				}
			}`),
			Handler: toolContactList,
			Builtin: true,
		})
	}
	if enabled("contact_upcoming") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_upcoming",
			Description: "Show upcoming contact birthdays and events in the next N days",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"days": {"type": "integer", "description": "Look-ahead days (default 30)"}
				}
			}`),
			Handler: toolContactUpcoming,
			Builtin: true,
		})
	}
	if enabled("contact_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "contact_log",
			Description: "Log an interaction with a contact (call, meeting, chat, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"contactId": {"type": "integer", "description": "Contact ID"},
					"type": {"type": "string", "description": "Interaction type (call, meeting, chat, gift, etc.)"},
					"notes": {"type": "string", "description": "Interaction notes"}
				},
				"required": ["contactId", "type"]
			}`),
			Handler: toolContactLog,
			Builtin: true,
		})
	}

	// --- P24.3: Life Insights Engine Tools ---
	if enabled("life_report") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "life_report",
			Description: "Generate a life report (daily, weekly, monthly) combining activity, spending, habits, and goals",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Report period: daily, weekly, monthly"},
					"date": {"type": "string", "description": "Target date (YYYY-MM-DD, default today)"}
				}
			}`),
			Handler: toolLifeReport,
			Builtin: true,
		})
	}
	if enabled("life_insights") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "life_insights",
			Description: "Get AI-driven life insights: anomaly detection, spending forecast, behavioral patterns",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"type": {"type": "string", "description": "Insight type: anomalies, forecast, patterns, all"},
					"days": {"type": "integer", "description": "Analysis window in days (default 30)"}
				}
			}`),
			Handler: toolLifeInsights,
			Builtin: true,
		})
	}

	// --- P24.4: Smart Scheduling Tools ---
	if enabled("schedule_view") {
		r.Register(&ToolDef{
			Name:        "schedule_view",
			Description: "View schedule for a date range from calendar events",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default today)"},
					"days": {"type": "integer", "description": "Number of days to show (default 1)"}
				}
			}`),
			Handler: toolScheduleView,
			Builtin: true,
		})
	}
	if enabled("schedule_suggest") {
		r.Register(&ToolDef{
			Name:        "schedule_suggest",
			Description: "Find available time slots for a new event",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"duration": {"type": "integer", "description": "Duration in minutes"},
					"date": {"type": "string", "description": "Target date (YYYY-MM-DD, default today)"},
					"days": {"type": "integer", "description": "Look-ahead days (default 7)"},
					"preferMorning": {"type": "boolean", "description": "Prefer morning slots"}
				},
				"required": ["duration"]
			}`),
			Handler: toolScheduleSuggest,
			Builtin: true,
		})
	}
	if enabled("schedule_plan") {
		r.Register(&ToolDef{
			Name:        "schedule_plan",
			Description: "Analyze schedule for overcommitment, suggest time blocks, and plan the day",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date to plan (YYYY-MM-DD, default today)"},
					"tasks": {"type": "string", "description": "Comma-separated tasks to fit into schedule"}
				}
			}`),
			Handler: toolSchedulePlan,
			Builtin: true,
		})
	}

	// --- P24.5: Habit & Wellness Tracking Tools ---
	if enabled("habit_create") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_create",
			Description: "Create a new habit to track (daily, weekly frequency)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Habit name"},
					"frequency": {"type": "string", "description": "Frequency: daily, weekly (default daily)"},
					"target": {"type": "integer", "description": "Target count per period (default 1)"},
					"category": {"type": "string", "description": "Category (health, productivity, etc.)"}
				},
				"required": ["name"]
			}`),
			Handler: toolHabitCreate,
			Builtin: true,
		})
	}
	if enabled("habit_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_log",
			Description: "Log a habit completion",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"habitId": {"type": "integer", "description": "Habit ID"},
					"name": {"type": "string", "description": "Habit name (alternative to ID)"},
					"count": {"type": "integer", "description": "Count (default 1)"},
					"notes": {"type": "string", "description": "Optional notes"}
				}
			}`),
			Handler: toolHabitLog,
			Builtin: true,
		})
	}
	if enabled("habit_status") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_status",
			Description: "Show current habit streaks and progress",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"habitId": {"type": "integer", "description": "Specific habit ID (optional, shows all if omitted)"}
				}
			}`),
			Handler: toolHabitStatus,
			Builtin: true,
		})
	}
	if enabled("habit_report") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "habit_report",
			Description: "Generate habit tracking report for a period",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"days": {"type": "integer", "description": "Report period in days (default 30)"},
					"category": {"type": "string", "description": "Filter by category (optional)"}
				}
			}`),
			Handler: toolHabitReport,
			Builtin: true,
		})
	}
	if enabled("health_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "health_log",
			Description: "Log health data (weight, blood pressure, sleep, steps, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"metric": {"type": "string", "description": "Metric name (weight, bp, sleep, steps, etc.)"},
					"value": {"type": "number", "description": "Metric value"},
					"unit": {"type": "string", "description": "Unit (kg, mmHg, hours, etc.)"},
					"notes": {"type": "string", "description": "Optional notes"}
				},
				"required": ["metric", "value"]
			}`),
			Handler: toolHealthLog,
			Builtin: true,
		})
	}
	if enabled("health_summary") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "health_summary",
			Description: "Get health data summary with trends",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"metric": {"type": "string", "description": "Specific metric (optional, shows all if omitted)"},
					"days": {"type": "integer", "description": "Period in days (default 30)"}
				}
			}`),
			Handler: toolHealthSummary,
			Builtin: true,
		})
	}

	// --- P24.6: Goal Planning & Autonomy Tools ---
	if enabled("goal_create") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_create",
			Description: "Create a new goal with milestones and deadline",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Goal title"},
					"description": {"type": "string", "description": "Goal description"},
					"deadline": {"type": "string", "description": "Deadline (YYYY-MM-DD, optional)"},
					"milestones": {"type": "string", "description": "Comma-separated milestone titles"},
					"category": {"type": "string", "description": "Category (career, health, finance, etc.)"}
				},
				"required": ["title"]
			}`),
			Handler: toolGoalCreate,
			Builtin: true,
		})
	}
	if enabled("goal_list") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_list",
			Description: "List goals, optionally filtered by status or category",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"status": {"type": "string", "description": "Filter: active, completed, abandoned (default active)"},
					"category": {"type": "string", "description": "Filter by category (optional)"}
				}
			}`),
			Handler: toolGoalList,
			Builtin: true,
		})
	}
	if enabled("goal_update") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_update",
			Description: "Update goal progress, status, or milestones",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"goalId": {"type": "integer", "description": "Goal ID"},
					"action": {"type": "string", "description": "Action: progress, complete, abandon, milestone_done"},
					"milestoneIndex": {"type": "integer", "description": "Milestone index (for milestone_done)"},
					"notes": {"type": "string", "description": "Progress notes"}
				},
				"required": ["goalId", "action"]
			}`),
			Handler: toolGoalUpdate,
			Builtin: true,
		})
	}
	if enabled("goal_review") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "goal_review",
			Description: "Generate a goal review: stale goals, upcoming deadlines, weekly progress",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"type": {"type": "string", "description": "Review type: weekly, stale, deadlines, all (default all)"}
				}
			}`),
			Handler: toolGoalReview,
			Builtin: true,
		})
	}

	// --- P24.7: Morning Briefing & Evening Wrap Tools ---
	if enabled("briefing_morning") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "briefing_morning",
			Description: "Generate a morning briefing: schedule, tasks, habits, goals, reminders, birthdays",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default today)"}
				}
			}`),
			Handler: toolBriefingMorning,
			Builtin: true,
		})
	}
	if enabled("briefing_evening") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "briefing_evening",
			Description: "Generate an evening wrap-up: day summary, habits completed, spending, tasks done, tomorrow preview",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default today)"}
				}
			}`),
			Handler: toolBriefingEvening,
			Builtin: true,
		})
	}

	// --- P29.2: Time Tracking ---
	if enabled("time_start") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_start",
			Description: "Start a time tracking timer for a project/activity",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"project": {"type": "string", "description": "Project name (default: general)"},
					"activity": {"type": "string", "description": "Activity description"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags for categorization"},
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				}
			}`),
			Handler: toolTimeStart,
			Builtin: true,
		})
	}
	if enabled("time_stop") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_stop",
			Description: "Stop the currently running time tracking timer",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				}
			}`),
			Handler: toolTimeStop,
			Builtin: true,
		})
	}
	if enabled("time_log") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_log",
			Description: "Log a manual time entry (already completed work)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"project": {"type": "string", "description": "Project name"},
					"activity": {"type": "string", "description": "Activity description"},
					"duration": {"type": "number", "description": "Duration in minutes"},
					"date": {"type": "string", "description": "Date (YYYY-MM-DD, default: today)"},
					"note": {"type": "string", "description": "Notes about the work"},
					"tags": {"type": "array", "items": {"type": "string"}, "description": "Tags"},
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				},
				"required": ["duration"]
			}`),
			Handler: toolTimeLog,
			Builtin: true,
		})
	}
	if enabled("time_report") && cfg.HistoryDB != "" {
		r.Register(&ToolDef{
			Name:        "time_report",
			Description: "Generate a time tracking report with hours by project, day, and top activities",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"period": {"type": "string", "description": "Report period: today, week, month, year (default: week)"},
					"project": {"type": "string", "description": "Filter by project (optional)"},
					"user_id": {"type": "string", "description": "User ID (default: default)"}
				}
			}`),
			Handler: toolTimeReport,
			Builtin: true,
		})
	}

	// --- P29.1: Quick Capture ---
	if enabled("quick_capture") {
		r.Register(&ToolDef{
			Name:        "quick_capture",
			Description: "Quick-capture any text: auto-classifies as task, expense, reminder, contact, note, or idea and routes accordingly",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Free-form text to capture"},
					"category": {"type": "string", "description": "Override category: task, expense, reminder, contact, note, idea (optional, auto-detected if omitted)"}
				},
				"required": ["text"]
			}`),
			Handler: toolQuickCapture,
			Builtin: true,
		})
	}

	// --- P29.0: Lifecycle Automation ---
	if enabled("lifecycle_sync") {
		r.Register(&ToolDef{
			Name:        "lifecycle_sync",
			Description: "Run cross-module lifecycle sync: birthday reminders, insight-driven actions, or both",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Sync action: birthdays, insights, or all (default: all)"}
				}
			}`),
			Handler: toolLifecycleSync,
			Builtin: true,
		})
	}
	if enabled("lifecycle_suggest") {
		r.Register(&ToolDef{
			Name:        "lifecycle_suggest",
			Description: "Suggest habits based on a goal's title and category",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"goal_title": {"type": "string", "description": "Goal title to analyze"},
					"goal_category": {"type": "string", "description": "Goal category (optional)"}
				},
				"required": ["goal_title"]
			}`),
			Handler: toolLifecycleSuggest,
			Builtin: true,
		})
	}

	// --- P23.1: User Profile Tools ---
	if enabled("user_profile_get") {
		r.Register(&ToolDef{
			Name:        "user_profile_get",
			Description: "Get a user's profile including preferences and recent mood",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID"},
					"channelKey": {"type": "string", "description": "Channel key (e.g., 'tg:12345') - resolves to user"}
				}
			}`),
			Handler: toolUserProfileGet,
			Builtin: true,
		})
	}
	if enabled("user_profile_set") {
		r.Register(&ToolDef{
			Name:        "user_profile_set",
			Description: "Update user profile or link a channel identity",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID"},
					"displayName": {"type": "string", "description": "Display name"},
					"language": {"type": "string", "description": "Preferred language"},
					"timezone": {"type": "string", "description": "Timezone"},
					"channelKey": {"type": "string", "description": "Link this channel to user"},
					"channelName": {"type": "string", "description": "Channel display name"}
				},
				"required": ["userId"]
			}`),
			Handler: toolUserProfileSet,
			Builtin: true,
		})
	}
	if enabled("mood_check") {
		r.Register(&ToolDef{
			Name:        "mood_check",
			Description: "Check a user's recent mood trend",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID"},
					"channelKey": {"type": "string", "description": "Channel key to resolve user"},
					"days": {"type": "number", "description": "Number of days to look back (default 7)"}
				}
			}`),
			Handler: toolMoodCheck,
			Builtin: true,
		})
	}

	// --- P23.6: Multi-User / Family Mode Tools ---
	if enabled("family_list_add") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "family_list_add",
			Description: "Add an item to a shared family list (e.g. shopping list)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Item text"},
					"listId": {"type": "string", "description": "List ID (optional, uses default shopping list)"},
					"quantity": {"type": "string", "description": "Quantity (optional)"},
					"addedBy": {"type": "string", "description": "User who added (optional)"}
				},
				"required": ["text"]
			}`),
			Handler: toolFamilyListAdd,
			Builtin: true,
		})
	}
	if enabled("family_list_view") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "family_list_view",
			Description: "View shared family lists and their items",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"listId": {"type": "string", "description": "List ID to view items for (optional, lists all lists if empty)"},
					"listType": {"type": "string", "description": "Filter by list type (optional)"}
				}
			}`),
			Handler: toolFamilyListView,
			Builtin: true,
		})
	}
	if enabled("user_switch") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "user_switch",
			Description: "Switch to a different user context (shows profile, permissions, rate limit)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"userId": {"type": "string", "description": "User ID to switch to"}
				},
				"required": ["userId"]
			}`),
			Handler: toolUserSwitch,
			Builtin: true,
		})
	}
	if enabled("family_manage") && cfg.Family.Enabled {
		r.Register(&ToolDef{
			Name:        "family_manage",
			Description: "Manage family users: add, remove, list, update, grant/revoke permissions",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: add, remove, list, update, grant, revoke"},
					"userId": {"type": "string", "description": "User ID"},
					"displayName": {"type": "string", "description": "Display name (for add/update)"},
					"role": {"type": "string", "description": "Role: admin, member, guest (for add/update)"},
					"permission": {"type": "string", "description": "Permission name (for grant/revoke)"},
					"rateLimit": {"type": "integer", "description": "Daily rate limit (for update)"},
					"budgetMonthly": {"type": "number", "description": "Monthly budget (for update)"}
				},
				"required": ["action"]
			}`),
			Handler: toolFamilyManage,
			Builtin: true,
		})
	}
}
