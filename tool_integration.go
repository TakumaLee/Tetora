package main

import (
	"encoding/json"
)

// registerIntegrationTools registers integration tools (Gmail, Calendar, Spotify,
// YouTube, Home Assistant, Drive, Dropbox, browser relay, iMessage, Twitter,
// NotebookLM, media, files).
// Note: handler functions are defined in their own files (gmail.go, gcalendar.go, etc.).
func registerIntegrationTools(r *ToolRegistry, cfg *Config, enabled func(string) bool) {
	// --- P19.1: Gmail Integration ---
	if enabled("email_list") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_list",
			Description: "List recent emails from Gmail with optional search query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Gmail search query (e.g. 'from:alice subject:meeting')"},
					"maxResults": {"type": "number", "description": "Maximum number of results (default 20)"}
				}
			}`),
			Handler: toolEmailList,
			Builtin: true,
		})
	}

	if enabled("email_read") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_read",
			Description: "Read a specific email message by ID, returning full headers and body",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message_id": {"type": "string", "description": "Gmail message ID"}
				},
				"required": ["message_id"]
			}`),
			Handler: toolEmailRead,
			Builtin: true,
		})
	}

	if enabled("email_send") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_send",
			Description: "Send an email via Gmail",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"to": {"type": "string", "description": "Recipient email address"},
					"subject": {"type": "string", "description": "Email subject"},
					"body": {"type": "string", "description": "Email body (plain text)"},
					"cc": {"type": "array", "items": {"type": "string"}, "description": "CC recipients"},
					"bcc": {"type": "array", "items": {"type": "string"}, "description": "BCC recipients"}
				},
				"required": ["to", "subject", "body"]
			}`),
			Handler:     toolEmailSend,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("email_draft") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_draft",
			Description: "Create an email draft in Gmail",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"to": {"type": "string", "description": "Recipient email address"},
					"subject": {"type": "string", "description": "Email subject"},
					"body": {"type": "string", "description": "Email body (plain text)"}
				},
				"required": ["to", "subject"]
			}`),
			Handler: toolEmailDraft,
			Builtin: true,
		})
	}

	if enabled("email_search") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_search",
			Description: "Search emails using advanced Gmail search syntax (from:, to:, subject:, has:attachment, after:, before:, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Gmail search query using advanced syntax"},
					"maxResults": {"type": "number", "description": "Maximum number of results (default 20)"}
				},
				"required": ["query"]
			}`),
			Handler: toolEmailSearch,
			Builtin: true,
		})
	}

	if enabled("email_label") && cfg.Gmail.Enabled {
		r.Register(&ToolDef{
			Name:        "email_label",
			Description: "Add or remove labels on a Gmail message (e.g. INBOX, UNREAD, STARRED, IMPORTANT, SPAM, TRASH)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"message_id": {"type": "string", "description": "Gmail message ID"},
					"add_labels": {"type": "array", "items": {"type": "string"}, "description": "Label IDs to add"},
					"remove_labels": {"type": "array", "items": {"type": "string"}, "description": "Label IDs to remove"}
				},
				"required": ["message_id"]
			}`),
			Handler: toolEmailLabel,
			Builtin: true,
		})
	}

	// --- P19.2: Google Calendar Integration ---
	if enabled("calendar_list") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_list",
			Description: "List upcoming Google Calendar events within a time range",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"timeMin": {"type": "string", "description": "Start of time range (RFC3339, default: now)"},
					"timeMax": {"type": "string", "description": "End of time range (RFC3339, default: 7 days from now)"},
					"maxResults": {"type": "number", "description": "Maximum number of events to return (default 10)"},
					"days": {"type": "number", "description": "Convenience: list events for next N days (default 7)"}
				}
			}`),
			Handler: toolCalendarList,
			Builtin: true,
		})
	}

	if enabled("calendar_create") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_create",
			Description: "Create a Google Calendar event. Accepts structured input or natural language (Japanese/English/Chinese)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"summary": {"type": "string", "description": "Event title"},
					"description": {"type": "string", "description": "Event description"},
					"location": {"type": "string", "description": "Event location"},
					"start": {"type": "string", "description": "Start time (RFC3339 or date YYYY-MM-DD)"},
					"end": {"type": "string", "description": "End time (RFC3339 or date; default: 1 hour after start)"},
					"timeZone": {"type": "string", "description": "Time zone (e.g. Asia/Tokyo)"},
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Attendee email addresses"},
					"allDay": {"type": "boolean", "description": "Create as all-day event"},
					"text": {"type": "string", "description": "Natural language schedule (e.g. '明日2時の会議', 'meeting tomorrow at 2pm')"}
				}
			}`),
			Handler: toolCalendarCreate,
			Builtin: true,
		})
	}

	if enabled("calendar_update") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_update",
			Description: "Update an existing Google Calendar event",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"eventId": {"type": "string", "description": "Event ID to update"},
					"summary": {"type": "string", "description": "New event title"},
					"description": {"type": "string", "description": "New description"},
					"location": {"type": "string", "description": "New location"},
					"start": {"type": "string", "description": "New start time (RFC3339)"},
					"end": {"type": "string", "description": "New end time (RFC3339)"},
					"timeZone": {"type": "string", "description": "Time zone"},
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Updated attendee emails"},
					"allDay": {"type": "boolean", "description": "All-day event flag"}
				},
				"required": ["eventId"]
			}`),
			Handler: toolCalendarUpdate,
			Builtin: true,
		})
	}

	if enabled("calendar_delete") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_delete",
			Description: "Delete a Google Calendar event by ID",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"eventId": {"type": "string", "description": "Event ID to delete"}
				},
				"required": ["eventId"]
			}`),
			Handler:     toolCalendarDelete,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("calendar_search") && cfg.Calendar.Enabled {
		r.Register(&ToolDef{
			Name:        "calendar_search",
			Description: "Search Google Calendar events by text query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (full-text)"},
					"timeMin": {"type": "string", "description": "Start of time range (RFC3339, default: 30 days ago)"},
					"timeMax": {"type": "string", "description": "End of time range (RFC3339, default: 90 days from now)"}
				},
				"required": ["query"]
			}`),
			Handler: toolCalendarSearch,
			Builtin: true,
		})
	}

	// --- P20.3: Twitter/X Integration ---
	if enabled("tweet_post") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_post",
			Description: "Post a tweet on Twitter/X. Optionally reply to an existing tweet.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Tweet text content"},
					"reply_to": {"type": "string", "description": "Tweet ID to reply to (optional)"}
				},
				"required": ["text"]
			}`),
			Handler:     toolTweetPost,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("tweet_read_timeline") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_read_timeline",
			Description: "Read the authenticated user's home timeline (reverse chronological)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"max_results": {"type": "number", "description": "Maximum number of tweets to return (default 10, max 100)"}
				}
			}`),
			Handler: toolTweetTimeline,
			Builtin: true,
		})
	}

	if enabled("tweet_search") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_search",
			Description: "Search recent tweets on Twitter/X matching a query",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query (supports Twitter search operators)"},
					"max_results": {"type": "number", "description": "Maximum number of tweets to return (default 10, max 100)"}
				},
				"required": ["query"]
			}`),
			Handler: toolTweetSearch,
			Builtin: true,
		})
	}

	if enabled("tweet_reply") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_reply",
			Description: "Reply to a specific tweet on Twitter/X",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"tweet_id": {"type": "string", "description": "ID of the tweet to reply to"},
					"text": {"type": "string", "description": "Reply text content"}
				},
				"required": ["tweet_id", "text"]
			}`),
			Handler:     toolTweetReply,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("tweet_dm") && cfg.Twitter.Enabled {
		r.Register(&ToolDef{
			Name:        "tweet_dm",
			Description: "Send a direct message to a Twitter/X user",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"recipient_id": {"type": "string", "description": "Twitter user ID of the recipient"},
					"text": {"type": "string", "description": "Message text"}
				},
				"required": ["recipient_id", "text"]
			}`),
			Handler:     toolTweetDM,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	// --- P21.6: Browser Extension Relay ---
	if enabled("browser_navigate") && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "browser_navigate",
			Description: "Navigate the browser to a URL via Chrome extension relay",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to navigate to"}
				},
				"required": ["url"]
			}`),
			Handler: toolBrowserRelay("navigate"),
			Builtin: true,
		})
	}
	if enabled("browser_content") && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "browser_content",
			Description: "Get the text content of the current browser page via Chrome extension relay",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolBrowserRelay("content"),
			Builtin: true,
		})
	}
	if enabled("browser_click") && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "browser_click",
			Description: "Click an element on the current page by CSS selector via Chrome extension relay",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"selector": {"type": "string", "description": "CSS selector of element to click"}
				},
				"required": ["selector"]
			}`),
			Handler: toolBrowserRelay("click"),
			Builtin: true,
		})
	}
	if enabled("browser_type") && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "browser_type",
			Description: "Type text into an input element via Chrome extension relay",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"selector": {"type": "string", "description": "CSS selector of input element"},
					"text": {"type": "string", "description": "Text to type"}
				},
				"required": ["selector", "text"]
			}`),
			Handler: toolBrowserRelay("type"),
			Builtin: true,
		})
	}
	if enabled("browser_screenshot") && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "browser_screenshot",
			Description: "Take a screenshot of the current browser tab via Chrome extension relay",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolBrowserRelay("screenshot"),
			Builtin: true,
		})
	}
	if enabled("browser_eval") && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "browser_eval",
			Description: "Execute JavaScript in the current browser tab via Chrome extension relay",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"code": {"type": "string", "description": "JavaScript code to execute"}
				},
				"required": ["code"]
			}`),
			Handler:     toolBrowserRelay("eval"),
			Builtin:     true,
			RequireAuth: true,
		})
	}

	// --- P21.7: NotebookLM Skill ---
	if enabled("notebooklm_import") && cfg.NotebookLM.Enabled && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "notebooklm_import",
			Description: "Import URLs as sources into a NotebookLM notebook via browser relay. Requires the Chrome extension to be connected and NotebookLM open.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"notebook_url": {"type": "string", "description": "URL of the NotebookLM notebook"},
					"urls": {"type": "array", "items": {"type": "string"}, "description": "URLs to import as sources"},
					"batch_size": {"type": "number", "description": "Number of URLs per batch (default 10)"}
				},
				"required": ["notebook_url", "urls"]
			}`),
			Handler:     toolNotebookLMImport,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("notebooklm_list_sources") && cfg.NotebookLM.Enabled && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "notebooklm_list_sources",
			Description: "List all sources in the current NotebookLM notebook via browser relay",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolNotebookLMListSources,
			Builtin: true,
		})
	}

	if enabled("notebooklm_query") && cfg.NotebookLM.Enabled && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "notebooklm_query",
			Description: "Ask a question in the current NotebookLM notebook and get the response",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"question": {"type": "string", "description": "Question to ask about the notebook sources"}
				},
				"required": ["question"]
			}`),
			Handler: toolNotebookLMQuery,
			Builtin: true,
		})
	}

	if enabled("notebooklm_delete_source") && cfg.NotebookLM.Enabled && cfg.BrowserRelay.Enabled {
		r.Register(&ToolDef{
			Name:        "notebooklm_delete_source",
			Description: "Delete a source from the current NotebookLM notebook",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"source_name": {"type": "string", "description": "Name/text of the source to delete"},
					"source_id": {"type": "string", "description": "Source ID (from notebooklm_list_sources)"}
				}
			}`),
			Handler:     toolNotebookLMDeleteSource,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	// --- P20.1: Home Assistant ---
	if enabled("ha_list_entities") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_list_entities",
			Description: "List Home Assistant entities, optionally filtered by domain (light, switch, sensor, climate, etc.)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"domain": {"type": "string", "description": "Entity domain filter (e.g. 'light', 'switch', 'sensor'). Optional — omit to list all."}
				}
			}`),
			Handler: toolHAListEntities,
			Builtin: true,
		})
	}

	if enabled("ha_get_state") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_get_state",
			Description: "Get the current state and attributes of a single Home Assistant entity",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"entity_id": {"type": "string", "description": "Entity ID (e.g. 'light.living_room', 'sensor.temperature')"}
				},
				"required": ["entity_id"]
			}`),
			Handler: toolHAGetState,
			Builtin: true,
		})
	}

	if enabled("ha_call_service") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_call_service",
			Description: "Call a Home Assistant service (e.g. turn on a light, set thermostat temperature, lock a door)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"domain": {"type": "string", "description": "Service domain (e.g. 'light', 'switch', 'climate')"},
					"service": {"type": "string", "description": "Service name (e.g. 'turn_on', 'turn_off', 'set_temperature')"},
					"data": {"type": "object", "description": "Service data (e.g. {\"entity_id\": \"light.living_room\", \"brightness\": 128})"}
				},
				"required": ["domain", "service"]
			}`),
			Handler:     toolHACallService,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("ha_set_state") && cfg.HomeAssistant.Enabled {
		r.Register(&ToolDef{
			Name:        "ha_set_state",
			Description: "Directly set the state of a Home Assistant entity (for virtual/custom sensors)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"entity_id": {"type": "string", "description": "Entity ID to update"},
					"state": {"type": "string", "description": "New state value"},
					"attributes": {"type": "object", "description": "Optional attributes to set"}
				},
				"required": ["entity_id", "state"]
			}`),
			Handler:     toolHASetState,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	// --- P20.2: iMessage via BlueBubbles ---
	if enabled("imessage_send") && cfg.IMessage.Enabled {
		r.Register(&ToolDef{
			Name:        "imessage_send",
			Description: "Send an iMessage to a specific chat via BlueBubbles",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"chat_guid": {"type": "string", "description": "Chat GUID (e.g. 'iMessage;-;+1234567890')"},
					"text": {"type": "string", "description": "Message text to send"}
				},
				"required": ["chat_guid", "text"]
			}`),
			Handler: toolIMessageSend,
			Builtin: true,
		})
	}

	if enabled("imessage_search") && cfg.IMessage.Enabled {
		r.Register(&ToolDef{
			Name:        "imessage_search",
			Description: "Search iMessage messages via BlueBubbles",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"limit": {"type": "number", "description": "Maximum results (default 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolIMessageSearch,
			Builtin: true,
		})
	}

	if enabled("imessage_read") && cfg.IMessage.Enabled {
		r.Register(&ToolDef{
			Name:        "imessage_read",
			Description: "Read recent iMessage messages from a chat via BlueBubbles",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"chat_guid": {"type": "string", "description": "Chat GUID to read messages from"},
					"limit": {"type": "number", "description": "Number of recent messages to retrieve (default 20)"}
				},
				"required": ["chat_guid"]
			}`),
			Handler: toolIMessageRead,
			Builtin: true,
		})
	}

	// --- P20.4: Device Actions ---
	registerDeviceTools(r, cfg)

	// --- P23.5: Media Control Tools ---
	if enabled("spotify_play") && cfg.Spotify.Enabled {
		r.Register(&ToolDef{
			Name:        "spotify_play",
			Description: "Spotify playback control: play, pause, next, prev, volume, shuffle, repeat",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: play, pause, next, prev, volume, shuffle, repeat"},
					"uri": {"type": "string", "description": "Spotify URI to play (optional)"},
					"deviceId": {"type": "string", "description": "Target device ID (optional)"},
					"volume": {"type": "integer", "description": "Volume level 0-100 (for volume action)"}
				},
				"required": ["action"]
			}`),
			Handler: toolSpotifyPlay,
			Builtin: true,
		})
	}
	if enabled("spotify_search") && cfg.Spotify.Enabled {
		r.Register(&ToolDef{
			Name:        "spotify_search",
			Description: "Search Spotify for tracks, albums, artists, or playlists",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"type": {"type": "string", "description": "Type: track, album, artist, playlist (default: track)"},
					"limit": {"type": "integer", "description": "Max results (default: 5)"}
				},
				"required": ["query"]
			}`),
			Handler: toolSpotifySearch,
			Builtin: true,
		})
	}
	if enabled("spotify_now_playing") && cfg.Spotify.Enabled {
		r.Register(&ToolDef{
			Name:        "spotify_now_playing",
			Description: "Get currently playing track on Spotify",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolSpotifyNowPlaying,
			Builtin: true,
		})
	}
	if enabled("spotify_devices") && cfg.Spotify.Enabled {
		r.Register(&ToolDef{
			Name:        "spotify_devices",
			Description: "List available Spotify Connect devices",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolSpotifyDevices,
			Builtin: true,
		})
	}
	if enabled("spotify_recommend") && cfg.Spotify.Enabled {
		r.Register(&ToolDef{
			Name:        "spotify_recommend",
			Description: "Get track recommendations based on seed tracks or current playback",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"trackIds": {"type": "array", "items": {"type": "string"}, "description": "Seed track IDs (optional, uses current track if empty)"},
					"limit": {"type": "integer", "description": "Number of recommendations (default: 5)"}
				}
			}`),
			Handler: toolSpotifyRecommend,
			Builtin: true,
		})
	}
	if enabled("youtube_summary") {
		r.Register(&ToolDef{
			Name:        "youtube_summary",
			Description: "Extract subtitles and video info from a YouTube URL (requires yt-dlp)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "YouTube video URL"},
					"maxWords": {"type": "integer", "description": "Max words in transcript summary (default: 500)"},
					"lang": {"type": "string", "description": "Subtitle language (default: en)"}
				},
				"required": ["url"]
			}`),
			Handler: toolYouTubeSummary,
			Builtin: true,
		})
	}
	if enabled("podcast_list") && cfg.Podcast.Enabled {
		r.Register(&ToolDef{
			Name:        "podcast_list",
			Description: "Manage podcast subscriptions: subscribe, unsubscribe, list, latest, played",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: subscribe, unsubscribe, list, latest, played"},
					"url": {"type": "string", "description": "RSS feed URL (for subscribe/unsubscribe)"},
					"feedUrl": {"type": "string", "description": "Feed URL (for latest/played)"},
					"episodeGuid": {"type": "string", "description": "Episode GUID (for played)"},
					"limit": {"type": "integer", "description": "Max episodes (default: 10)"}
				},
				"required": ["action"]
			}`),
			Handler: toolPodcastList,
			Builtin: true,
		})
	}

	// --- P23.3: File & Document Processing Tools ---
	if enabled("pdf_read") && cfg.FileManager.Enabled {
		r.Register(&ToolDef{
			Name:        "pdf_read",
			Description: "Extract text from a PDF file using pdftotext",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to the PDF file"},
					"pages": {"type": "string", "description": "Page range (e.g. '1-5', optional)"}
				},
				"required": ["path"]
			}`),
			Handler: toolPdfRead,
			Builtin: true,
		})
	}
	if enabled("doc_summarize") && cfg.FileManager.Enabled {
		r.Register(&ToolDef{
			Name:        "doc_summarize",
			Description: "Summarize a document file (reads content and returns summary prompt)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to the document file"},
					"maxWords": {"type": "integer", "description": "Maximum words in summary (default: 200)"}
				},
				"required": ["path"]
			}`),
			Handler: toolDocSummarize,
			Builtin: true,
		})
	}
	if enabled("file_store") && cfg.FileManager.Enabled {
		r.Register(&ToolDef{
			Name:        "file_store",
			Description: "Store a file in the managed file system with content hash dedup",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"filename": {"type": "string", "description": "File name"},
					"category": {"type": "string", "description": "Category (general, document, image, etc.)"},
					"content": {"type": "string", "description": "File content (text) or base64 data"},
					"base64": {"type": "boolean", "description": "If true, content is base64-encoded binary"},
					"source": {"type": "string", "description": "Source identifier (optional)"}
				},
				"required": ["filename"]
			}`),
			Handler: toolFileStore,
			Builtin: true,
		})
	}
	if enabled("file_list") && cfg.FileManager.Enabled {
		r.Register(&ToolDef{
			Name:        "file_list",
			Description: "List managed files with optional category filter",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"category": {"type": "string", "description": "Filter by category (optional)"},
					"limit": {"type": "integer", "description": "Max results (default: 50)"}
				}
			}`),
			Handler: toolFileList,
			Builtin: true,
		})
	}
	if enabled("file_duplicates") && cfg.FileManager.Enabled {
		r.Register(&ToolDef{
			Name:        "file_duplicates",
			Description: "Find duplicate files by content hash",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolFileDuplicates,
			Builtin: true,
		})
	}
	if enabled("file_organize") && cfg.FileManager.Enabled {
		r.Register(&ToolDef{
			Name:        "file_organize",
			Description: "Organize a file into categorized storage (moves to category/YYYY-MM/ path)",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "File ID to organize"},
					"category": {"type": "string", "description": "Target category"}
				},
				"required": ["id", "category"]
			}`),
			Handler: toolFileOrganize,
			Builtin: true,
		})
	}
	if enabled("drive_search") {
		r.Register(&ToolDef{
			Name:        "drive_search",
			Description: "Search for files in Google Drive",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"},
					"maxResults": {"type": "integer", "description": "Max results (default: 10)"}
				},
				"required": ["query"]
			}`),
			Handler: toolDriveSearch,
			Builtin: true,
		})
	}
	if enabled("drive_upload") {
		r.Register(&ToolDef{
			Name:        "drive_upload",
			Description: "Upload a file to Google Drive",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "File name"},
					"content": {"type": "string", "description": "File content"},
					"mimeType": {"type": "string", "description": "MIME type (optional)"},
					"folderId": {"type": "string", "description": "Parent folder ID (optional)"}
				},
				"required": ["name", "content"]
			}`),
			Handler: toolDriveUpload,
			Builtin: true,
		})
	}
	if enabled("drive_download") {
		r.Register(&ToolDef{
			Name:        "drive_download",
			Description: "Download a file from Google Drive by ID",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"fileId": {"type": "string", "description": "Google Drive file ID"},
					"saveAs": {"type": "string", "description": "Save locally with this name (optional)"}
				},
				"required": ["fileId"]
			}`),
			Handler: toolDriveDownload,
			Builtin: true,
		})
	}
	if enabled("dropbox_op") {
		r.Register(&ToolDef{
			Name:        "dropbox_op",
			Description: "Dropbox operations: search, upload, download, list",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"action": {"type": "string", "description": "Action: search, upload, download, list"},
					"query": {"type": "string", "description": "Search query (for search)"},
					"path": {"type": "string", "description": "File path (for upload/download/list)"},
					"content": {"type": "string", "description": "File content (for upload)"},
					"saveAs": {"type": "string", "description": "Save locally with this name (for download, optional)"}
				},
				"required": ["action"]
			}`),
			Handler: toolDropboxOp,
			Builtin: true,
		})
	}

	// --- P18.2: OAuth 2.0 Framework ---
	if enabled("oauth_status") {
		r.Register(&ToolDef{
			Name:        "oauth_status",
			Description: "List connected OAuth services and their status (scopes, expiry). No secrets are returned.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {}
			}`),
			Handler: toolOAuthStatus,
			Builtin: true,
		})
	}

	if enabled("oauth_request") {
		r.Register(&ToolDef{
			Name:        "oauth_request",
			Description: "Make an authenticated HTTP request using a connected OAuth service. The token is auto-refreshed if needed.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"service": {"type": "string", "description": "OAuth service name (e.g. google, github)"},
					"method": {"type": "string", "description": "HTTP method (default: GET)"},
					"url": {"type": "string", "description": "Request URL"},
					"body": {"type": "string", "description": "Request body (optional)"}
				},
				"required": ["service", "url"]
			}`),
			Handler:     toolOAuthRequest,
			Builtin:     true,
			RequireAuth: true,
		})
	}

	if enabled("oauth_authorize") {
		r.Register(&ToolDef{
			Name:        "oauth_authorize",
			Description: "Get the authorization URL for an OAuth service so the user can connect it",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"service": {"type": "string", "description": "OAuth service name (e.g. google, github)"}
				},
				"required": ["service"]
			}`),
			Handler: toolOAuthAuthorize,
			Builtin: true,
		})
	}

	// --- P19.3: Smart Reminders ---
	if enabled("reminder_set") {
		r.Register(&ToolDef{
			Name:        "reminder_set",
			Description: "Set a new reminder with natural language time. Supports Japanese (5分後, 明日3時), English (in 5 min, tomorrow 3pm), Chinese (5分鐘後, 明天下午3點), and absolute times (2025-01-15 14:00, 15:30).",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"text": {"type": "string", "description": "Reminder text / what to remind about"},
					"time": {"type": "string", "description": "When to fire (natural language: '5分後', 'in 1 hour', 'tomorrow 3pm', '明天下午3點')"},
					"recurring": {"type": "string", "description": "Cron expression for recurring reminders (e.g. '0 9 * * *' for daily 9am). Optional."},
					"channel": {"type": "string", "description": "Source channel (telegram, slack, api, etc.)"},
					"user_id": {"type": "string", "description": "User ID for the reminder owner"}
				},
				"required": ["text", "time"]
			}`),
			Handler: toolReminderSet,
			Builtin: true,
		})
	}

	if enabled("reminder_list") {
		r.Register(&ToolDef{
			Name:        "reminder_list",
			Description: "List active (pending) reminders. Optionally filter by user_id.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"user_id": {"type": "string", "description": "Filter by user ID (optional)"}
				}
			}`),
			Handler: toolReminderList,
			Builtin: true,
		})
	}

	if enabled("reminder_cancel") {
		r.Register(&ToolDef{
			Name:        "reminder_cancel",
			Description: "Cancel an active reminder by ID.",
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"id": {"type": "string", "description": "Reminder ID to cancel"},
					"user_id": {"type": "string", "description": "User ID for ownership verification (optional)"}
				},
				"required": ["id"]
			}`),
			Handler: toolReminderCancel,
			Builtin: true,
		})
	}
}
