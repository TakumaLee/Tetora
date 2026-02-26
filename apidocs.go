package main

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

//go:embed apidocs.html
var apiDocsHTML string

// handleAPIDocs serves the embedded API documentation HTML page.
func handleAPIDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write([]byte(apiDocsHTML))
}

// handleAPISpec returns the OpenAPI 3.0 spec as JSON, dynamically built from config.
func handleAPISpec(cfg *Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		spec := buildOpenAPISpec(cfg)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		json.NewEncoder(w).Encode(spec)
	}
}

// buildOpenAPISpec constructs a full OpenAPI 3.0.3 specification for the Tetora API.
func buildOpenAPISpec(cfg *Config) map[string]any {
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Tetora API",
			"description": "AI Agent Orchestrator API — zero-dependency multi-agent dispatch, session management, workflow orchestration, and cost governance.",
			"version":     tetoraVersion,
		},
		"servers": []map[string]any{
			{"url": "http://" + cfg.ListenAddr, "description": "Local"},
		},
		"paths":      buildPaths(),
		"components": buildComponents(),
		"tags":       buildTags(),
	}

	// Add security scheme if API token is configured.
	if cfg.APIToken != "" {
		spec["security"] = []map[string]any{{"bearerAuth": []string{}}}
	}

	return spec
}

// buildTags returns the tag definitions for grouping endpoints.
func buildTags() []map[string]any {
	return []map[string]any{
		{"name": "Core", "description": "Task dispatch, cancellation, and status"},
		{"name": "Health", "description": "Health check and diagnostics"},
		{"name": "History", "description": "Execution history and cost statistics"},
		{"name": "Sessions", "description": "Conversational session management"},
		{"name": "Workflows", "description": "Workflow definition and execution"},
		{"name": "Knowledge", "description": "Knowledge base files and search"},
		{"name": "Infrastructure", "description": "Circuit breakers, queue, budget, and SLA"},
		{"name": "Cron", "description": "Scheduled job management"},
		{"name": "Agent", "description": "Agent messages, handoffs, and reflections"},
		{"name": "Agents", "description": "Agent configuration"},
		{"name": "Stats", "description": "Cost and performance statistics"},
		{"name": "Audit", "description": "Audit log and backup"},
	}
}

// buildPaths constructs the paths section of the OpenAPI spec.
func buildPaths() map[string]any {
	paths := map[string]any{}

	// ---- Core ----

	paths["/dispatch"] = map[string]any{
		"post": opPost("Dispatch tasks", "Core",
			"Submit one or more tasks for concurrent execution. Each task is routed to the appropriate agent and provider.",
			reqBody(ref("TaskArray")),
			resp200(ref("DispatchResult")),
			resp400(), resp401(), resp409("dispatch already running"),
		),
	}

	paths["/dispatch/estimate"] = map[string]any{
		"post": opPost("Estimate dispatch cost", "Core",
			"Estimate the cost of running tasks without executing them.",
			reqBody(ref("TaskArray")),
			resp200(ref("EstimateResult")),
			resp400(), resp401(),
		),
	}

	paths["/dispatch/failed"] = map[string]any{
		"get": opGet("List failed tasks", "Core",
			"List recently failed tasks available for retry or reroute.",
			nil,
			resp200(schemaArray(ref("FailedTask"))),
			resp401(),
		),
	}

	paths["/dispatch/{taskId}"] = map[string]any{
		"post": opPost("Retry or reroute failed task", "Core",
			"Retry a failed task with original params or reroute to a different agent.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": prop("string", "Action to perform: retry or reroute"),
					"role":   prop("string", "Target agent for reroute (required if action=reroute)"),
				},
			}),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
				"taskId": prop("string", ""),
			}}),
			resp400(), resp401(), resp404(),
		),
	}

	paths["/cancel"] = map[string]any{
		"post": opPost("Cancel running dispatch", "Core",
			"Cancel all currently running tasks in the active dispatch.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "cancelling or nothing to cancel"),
			}}),
			resp401(),
		),
	}

	paths["/cancel/{taskId}"] = map[string]any{
		"post": opPost("Cancel single task", "Core",
			"Cancel a specific running task by ID.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/tasks/running"] = map[string]any{
		"get": opGet("List running tasks", "Core",
			"List all currently running tasks from dispatch and cron.",
			nil,
			resp200(schemaArray(ref("RunningTask"))),
			resp401(),
		),
	}

	// ---- Health ----

	paths["/healthz"] = map[string]any{
		"get": opGet("Health check", "Health",
			"Deep health check including DB, providers, disk, and uptime status.",
			nil,
			resp200(ref("HealthResult")),
		),
	}

	// ---- History ----

	paths["/history"] = map[string]any{
		"get": opGet("List execution history", "History",
			"Query execution history with filtering and pagination.",
			[]map[string]any{
				queryParam("job_id", "string", "Filter by job ID"),
				queryParam("status", "string", "Filter by status (success, error, timeout)"),
				queryParam("from", "string", "Start date (RFC3339)"),
				queryParam("to", "string", "End date (RFC3339)"),
				queryParam("limit", "integer", "Results per page (default 20)"),
				queryParam("page", "integer", "Page number (default 1)"),
				queryParam("offset", "integer", "Offset (overrides page)"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"runs":  schemaArray(ref("JobRun")),
					"total": prop("integer", "Total matching records"),
					"page":  prop("integer", "Current page"),
					"limit": prop("integer", "Results per page"),
				},
			}),
			resp401(),
		),
	}

	paths["/history/{id}"] = map[string]any{
		"get": opGet("Get history entry", "History",
			"Get a single execution history entry by ID.",
			[]map[string]any{pathParam("id", "integer", "History entry ID")},
			resp200(ref("JobRun")),
			resp401(), resp404(),
		),
	}

	paths["/stats/cost"] = map[string]any{
		"get": opGet("Cost statistics", "Stats",
			"Get cost statistics summary (today, week, month, total).",
			nil,
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/trend"] = map[string]any{
		"get": opGet("Cost trend", "Stats",
			"Get daily cost trend for the last N days.",
			[]map[string]any{queryParam("days", "integer", "Number of days (default 30)")},
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/metrics"] = map[string]any{
		"get": opGet("Performance metrics", "Stats",
			"Get performance metrics (success rate, latency, throughput) per agent.",
			[]map[string]any{queryParam("days", "integer", "Number of days (default 7)")},
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/routing"] = map[string]any{
		"get": opGet("Routing statistics", "Stats",
			"Get smart dispatch routing statistics by agent.",
			[]map[string]any{queryParam("days", "integer", "Number of days (default 7)")},
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	paths["/stats/sla"] = map[string]any{
		"get": opGet("SLA statistics", "Stats",
			"Get SLA metrics per agent (success rate, latency, cost).",
			[]map[string]any{
				queryParam("role", "string", "Filter by agent"),
				queryParam("days", "integer", "Window in days (default from SLA config)"),
			},
			resp200(schemaArray(ref("SLAMetrics"))),
			resp401(),
		),
	}

	// ---- Sessions ----

	paths["/sessions"] = map[string]any{
		"get": opGet("List sessions", "Sessions",
			"List conversational sessions with optional filtering.",
			[]map[string]any{
				queryParam("role", "string", "Filter by agent"),
				queryParam("status", "string", "Filter by status (active, archived)"),
				queryParam("source", "string", "Filter by source"),
				queryParam("limit", "integer", "Results per page (default 20)"),
				queryParam("offset", "integer", "Offset for pagination"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"sessions": schemaArray(ref("Session")),
					"total":    prop("integer", "Total matching sessions"),
				},
			}),
			resp401(),
		),
		"post": opPost("Create session", "Sessions",
			"Create a new conversational session.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"role":   prop("string", "Agent name"),
					"title":  prop("string", "Session title"),
					"source": prop("string", "Source identifier"),
				},
			}),
			resp200(ref("Session")),
			resp400(), resp401(),
		),
	}

	paths["/sessions/{id}"] = map[string]any{
		"get": opGet("Get session detail", "Sessions",
			"Get session metadata and message history.",
			[]map[string]any{pathParam("id", "string", "Session ID")},
			resp200(ref("SessionDetail")),
			resp401(), resp404(),
		),
		"delete": opDelete("Delete session", "Sessions",
			"Delete a session and its messages.",
			[]map[string]any{pathParam("id", "string", "Session ID")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/sessions/{id}/message"] = map[string]any{
		"post": opPost("Send message to session", "Sessions",
			"Send a user message to a session and get an assistant response.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content": prop("string", "Message content"),
					"model":   prop("string", "Override model for this message"),
				},
				"required": []string{"content"},
			}),
			resp200(ref("SessionMessage")),
			resp400(), resp401(), resp404(),
		),
	}

	paths["/sessions/{id}/compact"] = map[string]any{
		"post": opPost("Compact session", "Sessions",
			"Compact session history to reduce token usage.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status":          prop("string", ""),
				"removedMessages": prop("integer", "Number of messages removed"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/sessions/{id}/stream"] = map[string]any{
		"get": opGet("Session SSE stream", "Sessions",
			"Server-Sent Events stream for real-time session updates.",
			[]map[string]any{pathParam("id", "string", "Session ID")},
			resp200(map[string]any{"type": "string", "description": "SSE event stream"}),
			resp401(), resp404(),
		),
	}

	// ---- Workflows ----

	paths["/workflows"] = map[string]any{
		"get": opGet("List workflows", "Workflows",
			"List all saved workflow definitions.",
			nil,
			resp200(schemaArray(ref("Workflow"))),
			resp401(),
		),
		"post": opPost("Create workflow", "Workflows",
			"Create or update a workflow definition.",
			reqBody(ref("Workflow")),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
				"name":   prop("string", "Workflow name"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/workflows/{name}"] = map[string]any{
		"get": opGet("Get workflow", "Workflows",
			"Get a single workflow definition by name.",
			[]map[string]any{pathParam("name", "string", "Workflow name")},
			resp200(ref("Workflow")),
			resp401(), resp404(),
		),
		"delete": opDelete("Delete workflow", "Workflows",
			"Delete a workflow definition.",
			[]map[string]any{pathParam("name", "string", "Workflow name")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/workflows/{name}/validate"] = map[string]any{
		"post": opPost("Validate workflow", "Workflows",
			"Validate a workflow definition (DAG, step references, etc.) without executing.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"valid":  map[string]any{"type": "boolean"},
				"errors": schemaArray(prop("string", "")),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/workflows/{name}/run"] = map[string]any{
		"post": opPost("Run workflow", "Workflows",
			"Execute a workflow. Supports live, dry-run, and shadow modes.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"variables": map[string]any{"type": "object", "additionalProperties": prop("string", "")},
					"mode":      prop("string", "Execution mode: live (default), dry-run, shadow"),
				},
			}),
			resp200(ref("WorkflowRun")),
			resp400(), resp401(), resp404(),
		),
	}

	paths["/workflow-runs"] = map[string]any{
		"get": opGet("List workflow runs", "Workflows",
			"List workflow execution runs with optional filtering.",
			[]map[string]any{
				queryParam("workflow", "string", "Filter by workflow name"),
			},
			resp200(schemaArray(ref("WorkflowRun"))),
			resp401(),
		),
	}

	paths["/workflow-runs/{id}"] = map[string]any{
		"get": opGet("Get workflow run", "Workflows",
			"Get workflow run details including step results, handoffs, and agent messages.",
			[]map[string]any{pathParam("id", "string", "Workflow run ID")},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"run":      ref("WorkflowRun"),
					"handoffs": schemaArray(ref("Handoff")),
					"messages": schemaArray(ref("AgentMessage")),
				},
			}),
			resp401(), resp404(),
		),
	}

	// ---- Knowledge ----

	paths["/knowledge"] = map[string]any{
		"get": opGet("List knowledge files", "Knowledge",
			"List all files in the knowledge base directory.",
			nil,
			resp200(schemaArray(ref("KnowledgeFile"))),
			resp401(),
		),
	}

	paths["/knowledge/search"] = map[string]any{
		"get": opGet("Search knowledge base", "Knowledge",
			"TF-IDF search across knowledge base files.",
			[]map[string]any{
				queryParam("q", "string", "Search query"),
				queryParam("limit", "integer", "Max results (default 10)"),
			},
			resp200(schemaArray(ref("SearchResult"))),
			resp401(),
		),
	}

	// ---- Infrastructure ----

	paths["/circuits"] = map[string]any{
		"get": opGet("Circuit breaker status", "Infrastructure",
			"Get the current state of all provider circuit breakers.",
			nil,
			resp200(map[string]any{"type": "object", "description": "Map of provider name to circuit state (closed, open, half-open)"}),
			resp401(),
		),
	}

	paths["/circuits/{provider}/reset"] = map[string]any{
		"post": opPost("Reset circuit breaker", "Infrastructure",
			"Reset a provider's circuit breaker to closed state.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"provider": prop("string", "Provider name"),
				"state":    prop("string", "New state (closed)"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/queue"] = map[string]any{
		"get": opGet("List offline queue", "Infrastructure",
			"List items in the offline task queue.",
			[]map[string]any{
				queryParam("status", "string", "Filter by status (pending, processing, completed, expired, failed)"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items":   schemaArray(ref("QueueItem")),
					"count":   prop("integer", "Number of items returned"),
					"pending": prop("integer", "Total pending items"),
				},
			}),
			resp401(),
		),
	}

	paths["/queue/{id}"] = map[string]any{
		"get": opGet("Get queue item", "Infrastructure",
			"Get a single offline queue item by ID.",
			[]map[string]any{pathParam("id", "integer", "Queue item ID")},
			resp200(ref("QueueItem")),
			resp401(), resp404(),
		),
		"delete": opDelete("Delete queue item", "Infrastructure",
			"Remove an item from the offline queue.",
			[]map[string]any{pathParam("id", "integer", "Queue item ID")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/queue/{id}/retry"] = map[string]any{
		"post": opPost("Retry queue item", "Infrastructure",
			"Retry a pending or failed queue item.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "retrying"),
				"taskId": prop("string", "New task ID"),
			}}),
			resp401(), resp404(), resp409("item not in retryable state"),
		),
	}

	paths["/budget"] = map[string]any{
		"get": opGet("Budget status", "Infrastructure",
			"Get current budget utilization across global, agent, and workflow scopes.",
			nil,
			resp200(map[string]any{"type": "object", "description": "Budget status with daily/weekly/monthly usage and caps"}),
			resp401(),
		),
	}

	paths["/budget/pause"] = map[string]any{
		"post": opPost("Pause all paid execution", "Infrastructure",
			"Activate the kill switch to pause all paid LLM execution.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "paused"),
			}}),
			resp401(),
		),
	}

	paths["/budget/resume"] = map[string]any{
		"post": opPost("Resume paid execution", "Infrastructure",
			"Deactivate the kill switch and resume paid LLM execution.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "active"),
			}}),
			resp401(),
		),
	}

	// ---- Cron ----

	paths["/cron"] = map[string]any{
		"get": opGet("List cron jobs", "Cron",
			"List all configured cron jobs with their status and schedule.",
			nil,
			resp200(schemaArray(ref("CronJob"))),
			resp401(),
		),
	}

	paths["/cron/{id}/trigger"] = map[string]any{
		"post": opPost("Trigger cron job", "Cron",
			"Manually trigger a cron job to run immediately.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "triggered"),
			}}),
			resp401(), resp404(),
		),
	}

	// ---- Agent ----

	paths["/agent-messages"] = map[string]any{
		"get": opGet("List agent messages", "Agent",
			"List inter-agent communication messages.",
			[]map[string]any{
				queryParam("workflowRun", "string", "Filter by workflow run ID"),
				queryParam("role", "string", "Filter by agent"),
				queryParam("limit", "integer", "Max results (default 50)"),
			},
			resp200(schemaArray(ref("AgentMessage"))),
			resp401(),
		),
		"post": opPost("Send agent message", "Agent",
			"Send an inter-agent communication message.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"fromAgent":      prop("string", "Sender agent"),
					"toAgent":        prop("string", "Recipient agent"),
					"type":          prop("string", "Message type: handoff, request, response, note"),
					"content":       prop("string", "Message content"),
					"workflowRunId": prop("string", "Associated workflow run ID"),
					"refId":         prop("string", "Reference to another message ID"),
				},
				"required": []string{"fromAgent", "toAgent", "content"},
			}),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "sent"),
				"id":     prop("string", "Message ID"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/handoffs"] = map[string]any{
		"get": opGet("List handoffs", "Agent",
			"List agent handoffs with optional workflow run filter.",
			[]map[string]any{
				queryParam("workflowRun", "string", "Filter by workflow run ID"),
			},
			resp200(schemaArray(ref("Handoff"))),
			resp401(),
		),
	}

	// ---- Agents ----

	paths["/roles"] = map[string]any{
		"get": opGet("List agents", "Agents",
			"List all configured agents.",
			nil,
			resp200(map[string]any{"type": "object", "additionalProperties": ref("AgentConfig")}),
			resp401(),
		),
		"post": opPost("Create agent", "Agents",
			"Create or update an agent configuration.",
			reqBody(ref("AgentConfig")),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", ""),
				"name":   prop("string", "Agent name"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/roles/{name}"] = map[string]any{
		"get": opGet("Get agent", "Agents",
			"Get a single agent configuration by name.",
			[]map[string]any{pathParam("name", "string", "Agent name")},
			resp200(ref("AgentConfig")),
			resp401(), resp404(),
		),
		"put": map[string]any{
			"tags":        []string{"Agents"},
			"summary":     "Update agent",
			"description": "Update an existing agent configuration.",
			"parameters":  []map[string]any{pathParam("name", "string", "Agent name")},
			"requestBody": reqBody(ref("AgentConfig")),
			"responses": mergeResponses(
				resp200(map[string]any{"type": "object", "properties": map[string]any{
					"status": prop("string", ""),
					"name":   prop("string", "Agent name"),
				}}),
				resp400(), resp401(), resp404(),
			),
		},
		"delete": opDelete("Delete agent", "Agents",
			"Delete an agent.",
			[]map[string]any{pathParam("name", "string", "Agent name")},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"status": prop("string", "deleted"),
			}}),
			resp401(), resp404(),
		),
	}

	paths["/roles/archetypes"] = map[string]any{
		"get": opGet("List agent archetypes", "Agents",
			"List available agent archetype templates.",
			nil,
			resp200(map[string]any{"type": "object"}),
			resp401(),
		),
	}

	// ---- Route (Smart Dispatch) ----

	paths["/route"] = map[string]any{
		"post": opPost("Smart dispatch", "Core",
			"Send a natural language prompt for intelligent routing to the best agent.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": prop("string", "Natural language prompt"),
					"async":  map[string]any{"type": "boolean", "description": "Run asynchronously (returns request ID)"},
				},
				"required": []string{"prompt"},
			}),
			resp200(ref("SmartDispatchResult")),
			resp400(), resp401(),
		),
	}

	paths["/route/classify"] = map[string]any{
		"post": opPost("Classify prompt", "Core",
			"Classify a prompt to determine which agent would handle it, without executing.",
			reqBody(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": prop("string", "Natural language prompt"),
				},
				"required": []string{"prompt"},
			}),
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"role":       prop("string", "Matched agent"),
				"confidence": prop("string", "Confidence level"),
				"method":     prop("string", "Classification method (keyword, llm)"),
			}}),
			resp400(), resp401(),
		),
	}

	paths["/route/{id}"] = map[string]any{
		"get": opGet("Get async route result", "Core",
			"Poll the result of an asynchronous smart dispatch request.",
			[]map[string]any{pathParam("id", "string", "Request ID from async route")},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": prop("string", "running, done, or error"),
					"result": ref("SmartDispatchResult"),
					"error":  prop("string", "Error message if failed"),
				},
			}),
			resp401(), resp404(),
		),
	}

	// ---- Audit / Backup ----

	paths["/audit"] = map[string]any{
		"get": opGet("Audit log", "Audit",
			"Query the audit log with pagination.",
			[]map[string]any{
				queryParam("limit", "integer", "Results per page (default 50)"),
				queryParam("page", "integer", "Page number (default 1)"),
			},
			resp200(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"entries": schemaArray(ref("AuditEntry")),
					"total":   prop("integer", "Total entries"),
					"page":    prop("integer", "Current page"),
					"limit":   prop("integer", "Results per page"),
				},
			}),
			resp401(),
		),
	}

	// --- Retention & Data ---

	paths["/retention"] = map[string]any{
		"get": opGet("Get retention config & stats", "Data",
			"Returns the retention configuration (effective days per table) and current row counts.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"config":   map[string]any{"type": "object", "description": "Configured retention days"},
				"defaults": map[string]any{"type": "object", "description": "Effective retention days (config or fallback)"},
				"stats":    map[string]any{"type": "object", "description": "Row count per table"},
			}}),
			resp401(),
		),
	}

	paths["/retention/cleanup"] = map[string]any{
		"post": opPost("Run retention cleanup", "Data",
			"Triggers an immediate retention cleanup across all tables.",
			nil,
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"results": schemaArray(map[string]any{"type": "object", "properties": map[string]any{
					"table": prop("string", "Table name"), "deleted": prop("integer", "Rows deleted"),
					"error": prop("string", "Error message if any"),
				}}),
			}}),
			resp401(),
		),
	}

	paths["/data/export"] = map[string]any{
		"get": opGet("Export all data", "Data",
			"Exports all user data as JSON (GDPR right of access). Includes history, sessions, memory, audit log, and reflections.",
			nil,
			resp200(map[string]any{"type": "object", "description": "Full data export"}),
			resp401(),
		),
	}

	paths["/data/purge"] = map[string]any{
		"delete": opDelete("Purge data before date", "Data",
			"Permanently deletes all data before the specified date. Requires X-Confirm-Purge: true header.",
			[]map[string]any{{
				"name": "before", "in": "query", "required": true,
				"description": "Date cutoff (YYYY-MM-DD)",
				"schema":      map[string]any{"type": "string", "format": "date"},
			}},
			resp200(map[string]any{"type": "object", "properties": map[string]any{
				"results": schemaArray(map[string]any{"type": "object"}),
			}}),
			resp401(),
		),
	}

	paths["/backup"] = map[string]any{
		"get": opGet("Download backup", "Audit",
			"Download a tar.gz backup of the Tetora data directory.",
			nil,
			map[string]any{
				"200": map[string]any{
					"description": "Backup archive",
					"content": map[string]any{
						"application/gzip": map[string]any{
							"schema": map[string]any{"type": "string", "format": "binary"},
						},
					},
				},
			},
			resp401(),
		),
	}

	return paths
}

// buildComponents constructs the components/schemas section.
func buildComponents() map[string]any {
	schemas := map[string]any{}

	schemas["Task"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":             prop("string", "Task ID (auto-generated if empty)"),
			"name":           prop("string", "Human-readable task name"),
			"prompt":         prop("string", "Task prompt for the agent"),
			"workdir":        prop("string", "Working directory"),
			"model":          prop("string", "LLM model to use"),
			"provider":       prop("string", "Provider name override"),
			"docker":         map[string]any{"type": "boolean", "description": "Run in Docker sandbox"},
			"timeout":        prop("string", "Timeout duration (e.g. 5m, 1h)"),
			"budget":         prop("number", "Max cost in USD"),
			"permissionMode": prop("string", "Permission mode for the agent"),
			"mcp":            prop("string", "MCP config name"),
			"addDirs":        schemaArray(prop("string", "")),
			"systemPrompt":   prop("string", "System prompt override"),
			"sessionId":      prop("string", "Session ID to continue"),
			"role":           prop("string", "Agent name"),
			"source":         prop("string", "Request source identifier"),
		},
		"required": []string{"prompt"},
	}

	schemas["TaskArray"] = schemaArray(ref("Task"))

	schemas["TaskResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         prop("string", "Task ID"),
			"name":       prop("string", "Task name"),
			"status":     prop("string", "Execution status: success, error, timeout, cancelled"),
			"exitCode":   prop("integer", "Process exit code"),
			"output":     prop("string", "Agent output text"),
			"error":      prop("string", "Error message if failed"),
			"durationMs": prop("integer", "Execution duration in milliseconds"),
			"costUsd":    prop("number", "Actual cost in USD"),
			"model":      prop("string", "Model used"),
			"sessionId":  prop("string", "Session ID"),
			"outputFile": prop("string", "Output file path (if any)"),
			"tokensIn":   prop("integer", "Input tokens consumed"),
			"tokensOut":  prop("integer", "Output tokens generated"),
			"providerMs": prop("integer", "Provider-side latency in ms"),
			"traceId":    prop("string", "Trace ID for request correlation"),
			"provider":   prop("string", "Provider used"),
		},
	}

	schemas["DispatchResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"startedAt":    prop("string", "Start time (RFC3339)"),
			"finishedAt":   prop("string", "Finish time (RFC3339)"),
			"durationMs":   prop("integer", "Total duration in milliseconds"),
			"totalCostUsd": prop("number", "Total cost in USD"),
			"tasks":        schemaArray(ref("TaskResult")),
			"summary":      prop("string", "Execution summary"),
		},
	}

	schemas["EstimateResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tasks":                 schemaArray(ref("CostEstimate")),
			"totalEstimatedCostUsd": prop("number", "Total estimated cost"),
			"classifyCostUsd":       prop("number", "Cost of LLM classification (if smart dispatch)"),
		},
	}

	schemas["CostEstimate"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":               prop("string", "Task name"),
			"provider":           prop("string", "Resolved provider"),
			"model":              prop("string", "Resolved model"),
			"estimatedCostUsd":   prop("number", "Estimated cost in USD"),
			"estimatedTokensIn":  prop("integer", "Estimated input tokens"),
			"estimatedTokensOut": prop("integer", "Estimated output tokens"),
			"breakdown":          prop("string", "Cost breakdown description"),
		},
	}

	schemas["Session"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":             prop("string", "Session ID"),
			"role":           prop("string", "Agent name"),
			"source":         prop("string", "Source channel"),
			"status":         prop("string", "Status: active, archived"),
			"title":          prop("string", "Session title"),
			"channelKey":     prop("string", "Channel session key"),
			"totalCost":      prop("number", "Total cost for session"),
			"totalTokensIn":  prop("integer", "Total input tokens"),
			"totalTokensOut": prop("integer", "Total output tokens"),
			"messageCount":   prop("integer", "Number of messages"),
			"createdAt":      prop("string", "Created timestamp (RFC3339)"),
			"updatedAt":      prop("string", "Updated timestamp (RFC3339)"),
		},
	}

	schemas["SessionMessage"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":        prop("integer", "Message ID"),
			"sessionId": prop("string", "Session ID"),
			"role":      prop("string", "Message role: user, assistant, system"),
			"content":   prop("string", "Message content"),
			"costUsd":   prop("number", "Cost for this message"),
			"tokensIn":  prop("integer", "Input tokens"),
			"tokensOut": prop("integer", "Output tokens"),
			"model":     prop("string", "Model used"),
			"taskId":    prop("string", "Associated task ID"),
			"createdAt": prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["SessionDetail"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session":  ref("Session"),
			"messages": schemaArray(ref("SessionMessage")),
		},
	}

	schemas["Workflow"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":        prop("string", "Workflow name (unique identifier)"),
			"description": prop("string", "Human-readable description"),
			"steps":       schemaArray(ref("WorkflowStep")),
			"variables":   map[string]any{"type": "object", "additionalProperties": prop("string", ""), "description": "Input variables with default values"},
			"timeout":     prop("string", "Overall workflow timeout (e.g. 30m)"),
			"onSuccess":   prop("string", "Notification template on success"),
			"onFailure":   prop("string", "Notification template on failure"),
		},
		"required": []string{"name", "steps"},
	}

	schemas["WorkflowStep"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":             prop("string", "Step ID (unique within workflow)"),
			"type":           prop("string", "Step type: dispatch, skill, condition, parallel"),
			"role":           prop("string", "Agent name for dispatch steps"),
			"prompt":         prop("string", "Prompt for dispatch steps (supports {{variable}} substitution)"),
			"skill":          prop("string", "Skill name for skill steps"),
			"skillArgs":      schemaArray(prop("string", "")),
			"dependsOn":      schemaArray(prop("string", "Step IDs that must complete first")),
			"model":          prop("string", "Model override"),
			"provider":       prop("string", "Provider override"),
			"timeout":        prop("string", "Per-step timeout"),
			"budget":         prop("number", "Per-step budget cap"),
			"permissionMode": prop("string", "Permission mode override"),
			"if":             prop("string", "Condition expression"),
			"then":           prop("string", "Step ID on condition true"),
			"else":           prop("string", "Step ID on condition false"),
			"handoffFrom":    prop("string", "Source step ID whose output becomes context"),
			"parallel":       schemaArray(ref("WorkflowStep")),
			"retryMax":       prop("integer", "Max retries on failure"),
			"retryDelay":     prop("string", "Delay between retries"),
			"onError":        prop("string", "Error handling: stop (default), skip, retry"),
		},
		"required": []string{"id"},
	}

	schemas["WorkflowRun"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":           prop("string", "Run ID"),
			"workflowName": prop("string", "Workflow name"),
			"status":       prop("string", "Status: running, success, error, cancelled, timeout"),
			"startedAt":    prop("string", "Start time (RFC3339)"),
			"finishedAt":   prop("string", "Finish time (RFC3339)"),
			"durationMs":   prop("integer", "Duration in milliseconds"),
			"totalCostUsd": prop("number", "Total cost in USD"),
			"variables":    map[string]any{"type": "object", "additionalProperties": prop("string", "")},
			"stepResults":  map[string]any{"type": "object", "additionalProperties": ref("StepRunResult")},
			"error":        prop("string", "Error message if failed"),
		},
	}

	schemas["StepRunResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"stepId":     prop("string", "Step ID"),
			"status":     prop("string", "Status: pending, running, success, error, skipped, timeout"),
			"output":     prop("string", "Step output"),
			"error":      prop("string", "Error message"),
			"startedAt":  prop("string", "Start time"),
			"finishedAt": prop("string", "Finish time"),
			"durationMs": prop("integer", "Duration in ms"),
			"costUsd":    prop("number", "Cost in USD"),
			"taskId":     prop("string", "Task ID"),
			"sessionId":  prop("string", "Session ID"),
			"retries":    prop("integer", "Number of retries"),
		},
	}

	schemas["KnowledgeFile"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":    prop("string", "Filename"),
			"size":    prop("integer", "File size in bytes"),
			"modTime": prop("string", "Last modification time (RFC3339)"),
		},
	}

	schemas["SearchResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filename":  prop("string", "Source filename"),
			"snippet":   prop("string", "Matched text snippet"),
			"score":     prop("number", "Relevance score"),
			"lineStart": prop("integer", "Starting line number of snippet"),
		},
	}

	schemas["Handoff"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":            prop("string", "Handoff ID"),
			"workflowRunId": prop("string", "Workflow run ID"),
			"fromAgent":      prop("string", "Source agent name"),
			"toAgent":        prop("string", "Target agent name"),
			"fromStepId":    prop("string", "Source step ID"),
			"toStepId":      prop("string", "Target step ID"),
			"fromSessionId": prop("string", "Source session ID"),
			"toSessionId":   prop("string", "Target session ID"),
			"context":       prop("string", "Output from source agent"),
			"instruction":   prop("string", "Instructions for target agent"),
			"status":        prop("string", "Status: pending, active, completed, error"),
			"createdAt":     prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["AgentMessage"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":            prop("string", "Message ID"),
			"workflowRunId": prop("string", "Workflow run ID"),
			"fromAgent":      prop("string", "Sender agent"),
			"toAgent":        prop("string", "Recipient agent"),
			"type":          prop("string", "Message type: handoff, request, response, note"),
			"content":       prop("string", "Message content"),
			"refId":         prop("string", "Reference to another message"),
			"createdAt":     prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["QueueItem"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         prop("integer", "Queue item ID"),
			"taskJson":   prop("string", "Serialized task JSON"),
			"role":       prop("string", "Target agent"),
			"source":     prop("string", "Source identifier"),
			"priority":   prop("integer", "Priority (higher = sooner)"),
			"status":     prop("string", "Status: pending, processing, completed, expired, failed"),
			"retryCount": prop("integer", "Number of retries"),
			"createdAt":  prop("string", "Created timestamp (RFC3339)"),
			"updatedAt":  prop("string", "Updated timestamp (RFC3339)"),
			"error":      prop("string", "Error message"),
		},
	}

	schemas["SLAMetrics"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role":         prop("string", "Agent name"),
			"total":        prop("integer", "Total executions"),
			"success":      prop("integer", "Successful executions"),
			"fail":         prop("integer", "Failed executions"),
			"successRate":  prop("number", "Success rate (0.0-1.0)"),
			"avgLatencyMs": prop("integer", "Average latency in ms"),
			"p95LatencyMs": prop("integer", "P95 latency in ms"),
			"totalCost":    prop("number", "Total cost in USD"),
			"avgCost":      prop("number", "Average cost per execution"),
		},
	}

	schemas["HealthResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status":    prop("string", "Overall status: ok, degraded, error"),
			"version":   prop("string", "Tetora version"),
			"uptime":    prop("string", "Server uptime"),
			"uptimeSec": prop("integer", "Server uptime in seconds"),
			"db":        map[string]any{"type": "object", "description": "Database health details"},
			"providers": map[string]any{"type": "object", "description": "Provider availability"},
			"disk":      map[string]any{"type": "object", "description": "Disk usage information"},
			"cron":      map[string]any{"type": "object", "description": "Cron engine status"},
		},
	}

	schemas["CronJob"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       prop("string", "Job ID"),
			"name":     prop("string", "Job name"),
			"schedule": prop("string", "Cron schedule expression"),
			"role":     prop("string", "Agent name"),
			"enabled":  map[string]any{"type": "boolean", "description": "Whether job is active"},
			"running":  map[string]any{"type": "boolean", "description": "Whether job is currently running"},
			"lastRun":  prop("string", "Last run timestamp"),
			"nextRun":  prop("string", "Next scheduled run"),
		},
	}

	schemas["AgentConfig"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name":              prop("string", "Agent name"),
			"soulFile":          prop("string", "System prompt file path"),
			"model":             prop("string", "Default model for this agent"),
			"description":       prop("string", "Agent description"),
			"keywords":          schemaArray(prop("string", "Routing keywords")),
			"permissionMode":    prop("string", "Permission mode"),
			"allowedDirs":       schemaArray(prop("string", "Allowed directories")),
			"provider":          prop("string", "Preferred provider"),
			"docker":            map[string]any{"type": "boolean", "description": "Docker sandbox override"},
			"fallbackProviders": schemaArray(prop("string", "Failover chain")),
		},
	}

	schemas["SmartDispatchResult"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"role":       prop("string", "Selected agent name"),
			"method":     prop("string", "Classification method (keyword, llm)"),
			"confidence": prop("string", "Classification confidence"),
			"taskResult": ref("TaskResult"),
		},
	}

	schemas["FailedTask"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       prop("string", "Task ID"),
			"name":     prop("string", "Task name"),
			"role":     prop("string", "Original agent"),
			"error":    prop("string", "Failure error message"),
			"failedAt": prop("string", "Failure timestamp (RFC3339)"),
		},
	}

	schemas["RunningTask"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       prop("string", "Task ID"),
			"name":     prop("string", "Task name"),
			"source":   prop("string", "Source (dispatch, cron)"),
			"model":    prop("string", "Model in use"),
			"timeout":  prop("string", "Timeout setting"),
			"elapsed":  prop("string", "Elapsed time"),
			"prompt":   prop("string", "Prompt (truncated)"),
			"pid":      prop("integer", "Process ID"),
			"pidAlive": map[string]any{"type": "boolean", "description": "Whether process is alive"},
		},
	}

	schemas["JobRun"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         prop("integer", "Record ID"),
			"taskId":     prop("string", "Task ID"),
			"jobName":    prop("string", "Job/task name"),
			"source":     prop("string", "Source identifier"),
			"role":       prop("string", "Agent name"),
			"status":     prop("string", "Execution status"),
			"model":      prop("string", "Model used"),
			"provider":   prop("string", "Provider used"),
			"costUsd":    prop("number", "Cost in USD"),
			"durationMs": prop("integer", "Duration in ms"),
			"tokensIn":   prop("integer", "Input tokens"),
			"tokensOut":  prop("integer", "Output tokens"),
			"startedAt":  prop("string", "Start time (RFC3339)"),
			"finishedAt": prop("string", "Finish time (RFC3339)"),
			"outputFile": prop("string", "Output file path"),
			"error":      prop("string", "Error message"),
		},
	}

	schemas["AuditEntry"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":        prop("integer", "Entry ID"),
			"event":     prop("string", "Event type"),
			"source":    prop("string", "Source"),
			"detail":    prop("string", "Event detail"),
			"ip":        prop("string", "Client IP"),
			"createdAt": prop("string", "Timestamp (RFC3339)"),
		},
	}

	schemas["Error"] = map[string]any{
		"type": "object",
		"properties": map[string]any{
			"error": prop("string", "Error message"),
		},
		"required": []string{"error"},
	}

	components := map[string]any{
		"schemas": schemas,
	}

	// Security scheme (always defined, applied conditionally in spec root).
	components["securitySchemes"] = map[string]any{
		"bearerAuth": map[string]any{
			"type":         "http",
			"scheme":       "bearer",
			"bearerFormat": "token",
			"description":  "API token configured in config.json (apiToken field)",
		},
	}

	return components
}

// --- OpenAPI builder helpers ---

// prop creates a simple property schema.
func prop(typeName, description string) map[string]any {
	p := map[string]any{"type": typeName}
	if description != "" {
		p["description"] = description
	}
	return p
}

// ref creates a $ref to a component schema.
func ref(name string) map[string]any {
	return map[string]any{"$ref": "#/components/schemas/" + name}
}

// schemaArray creates an array schema wrapping an item schema.
func schemaArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

// queryParam creates a query parameter definition.
func queryParam(name, typeName, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "query",
		"required":    false,
		"description": description,
		"schema":      map[string]any{"type": typeName},
	}
}

// pathParam creates a path parameter definition.
func pathParam(name, typeName, description string) map[string]any {
	return map[string]any{
		"name":        name,
		"in":          "path",
		"required":    true,
		"description": description,
		"schema":      map[string]any{"type": typeName},
	}
}

// reqBody creates a requestBody definition with JSON content type.
// Pass nil for endpoints with no body.
func reqBody(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	return map[string]any{
		"required": true,
		"content": map[string]any{
			"application/json": map[string]any{
				"schema": schema,
			},
		},
	}
}

// resp200 creates a 200 response with a JSON schema.
func resp200(schema map[string]any) map[string]any {
	return map[string]any{
		"200": map[string]any{
			"description": "Success",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": schema,
				},
			},
		},
	}
}

// resp400 creates a 400 Bad Request response.
func resp400() map[string]any {
	return map[string]any{
		"400": map[string]any{
			"description": "Bad Request",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// resp401 creates a 401 Unauthorized response.
func resp401() map[string]any {
	return map[string]any{
		"401": map[string]any{
			"description": "Unauthorized",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// resp404 creates a 404 Not Found response.
func resp404() map[string]any {
	return map[string]any{
		"404": map[string]any{
			"description": "Not Found",
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// resp409 creates a 409 Conflict response.
func resp409(detail string) map[string]any {
	desc := "Conflict"
	if detail != "" {
		desc = "Conflict: " + detail
	}
	return map[string]any{
		"409": map[string]any{
			"description": desc,
			"content": map[string]any{
				"application/json": map[string]any{
					"schema": ref("Error"),
				},
			},
		},
	}
}

// mergeResponses combines multiple response maps into one.
func mergeResponses(maps ...map[string]any) map[string]any {
	merged := map[string]any{}
	for _, m := range maps {
		for k, v := range m {
			merged[k] = v
		}
	}
	return merged
}

// opGet creates a GET operation definition.
func opGet(summary, tag, description string, params []map[string]any, responses ...map[string]any) map[string]any {
	op := map[string]any{
		"tags":        []string{tag},
		"summary":     summary,
		"description": description,
		"responses":   mergeResponses(responses...),
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	return op
}

// opPost creates a POST operation definition.
func opPost(summary, tag, description string, body map[string]any, responses ...map[string]any) map[string]any {
	op := map[string]any{
		"tags":        []string{tag},
		"summary":     summary,
		"description": description,
		"responses":   mergeResponses(responses...),
	}
	if body != nil {
		op["requestBody"] = body
	}
	return op
}

// opDelete creates a DELETE operation definition.
func opDelete(summary, tag, description string, params []map[string]any, responses ...map[string]any) map[string]any {
	op := map[string]any{
		"tags":        []string{tag},
		"summary":     summary,
		"description": description,
		"responses":   mergeResponses(responses...),
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	return op
}
