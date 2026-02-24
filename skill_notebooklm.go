package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// toolNotebookLMImport imports URLs as sources into a NotebookLM notebook.
func toolNotebookLMImport(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		NotebookURL string   `json:"notebook_url"`
		URLs        []string `json:"urls"`
		BatchSize   int      `json:"batch_size"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.NotebookURL == "" {
		return "", fmt.Errorf("notebook_url is required")
	}
	if len(args.URLs) == 0 {
		return "", fmt.Errorf("urls is required")
	}
	if args.BatchSize <= 0 {
		args.BatchSize = 10
	}

	if globalBrowserRelay == nil || !globalBrowserRelay.Connected() {
		return "", fmt.Errorf("browser extension not connected")
	}

	// Navigate to the notebook.
	navParams, _ := json.Marshal(map[string]string{"url": args.NotebookURL})
	if _, err := globalBrowserRelay.SendCommand("navigate", navParams, 30*time.Second); err != nil {
		return "", fmt.Errorf("navigate to notebook: %w", err)
	}

	// Wait for page to be ready.
	time.Sleep(3 * time.Second)

	imported := 0
	var errors []string

	for i := 0; i < len(args.URLs); i += args.BatchSize {
		end := i + args.BatchSize
		if end > len(args.URLs) {
			end = len(args.URLs)
		}
		batch := args.URLs[i:end]

		for _, url := range batch {
			// Click "Add source" button.
			clickParams, _ := json.Marshal(map[string]string{
				"selector": `button[aria-label="Add source"], button.add-source-button, [data-action="add-source"]`,
			})
			if _, err := globalBrowserRelay.SendCommand("click", clickParams, 10*time.Second); err != nil {
				errors = append(errors, fmt.Sprintf("click add source: %v", err))
				continue
			}
			time.Sleep(1 * time.Second)

			// Click "Website" option.
			clickWebsite, _ := json.Marshal(map[string]string{
				"selector": `[data-value="website"], button:has-text("Website"), [aria-label="Website"]`,
			})
			if _, err := globalBrowserRelay.SendCommand("click", clickWebsite, 10*time.Second); err != nil {
				errors = append(errors, fmt.Sprintf("click website option: %v", err))
				continue
			}
			time.Sleep(1 * time.Second)

			// Type URL.
			typeParams, _ := json.Marshal(map[string]string{
				"selector": `input[type="url"], input[placeholder*="URL"], input[aria-label*="URL"]`,
				"text":     url,
			})
			if _, err := globalBrowserRelay.SendCommand("type", typeParams, 10*time.Second); err != nil {
				errors = append(errors, fmt.Sprintf("type URL %s: %v", url, err))
				continue
			}
			time.Sleep(500 * time.Millisecond)

			// Click submit/insert.
			submitParams, _ := json.Marshal(map[string]string{
				"selector": `button[type="submit"], button:has-text("Insert"), button:has-text("Add")`,
			})
			if _, err := globalBrowserRelay.SendCommand("click", submitParams, 10*time.Second); err != nil {
				errors = append(errors, fmt.Sprintf("submit URL %s: %v", url, err))
				continue
			}

			imported++
			time.Sleep(2 * time.Second) // Wait for processing.
		}

		logInfoCtx(ctx, "notebooklm import batch", "batch", i/args.BatchSize+1, "imported", imported)
	}

	result := map[string]any{
		"total":    len(args.URLs),
		"imported": imported,
	}
	if len(errors) > 0 {
		result["errors"] = errors
	}
	b, _ := json.Marshal(result)
	return string(b), nil
}

// toolNotebookLMListSources lists sources in the current NotebookLM notebook.
func toolNotebookLMListSources(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	if globalBrowserRelay == nil || !globalBrowserRelay.Connected() {
		return "", fmt.Errorf("browser extension not connected")
	}

	// Execute JS to extract source list from the page.
	js := `
		(function() {
			var sources = [];
			var items = document.querySelectorAll('.source-item, [data-source-id], .source-list-item');
			items.forEach(function(el) {
				sources.push({
					name: el.textContent.trim().substring(0, 200),
					id: el.getAttribute('data-source-id') || ''
				});
			});
			if (sources.length === 0) {
				var panels = document.querySelectorAll('[role="listitem"]');
				panels.forEach(function(el) {
					sources.push({ name: el.textContent.trim().substring(0, 200) });
				});
			}
			return JSON.stringify(sources);
		})()
	`
	evalParams, _ := json.Marshal(map[string]string{"code": js})
	result, err := globalBrowserRelay.SendCommand("eval", evalParams, 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("list sources: %w", err)
	}
	return result, nil
}

// toolNotebookLMQuery asks a question in the current NotebookLM notebook.
func toolNotebookLMQuery(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		Question string `json:"question"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.Question == "" {
		return "", fmt.Errorf("question is required")
	}

	if globalBrowserRelay == nil || !globalBrowserRelay.Connected() {
		return "", fmt.Errorf("browser extension not connected")
	}

	// Type question into the chat input.
	typeParams, _ := json.Marshal(map[string]string{
		"selector": `textarea, input[aria-label*="question"], input[aria-label*="Ask"], [contenteditable="true"]`,
		"text":     args.Question,
	})
	if _, err := globalBrowserRelay.SendCommand("type", typeParams, 10*time.Second); err != nil {
		return "", fmt.Errorf("type question: %w", err)
	}

	// Press Enter / click send.
	submitParams, _ := json.Marshal(map[string]string{
		"selector": `button[aria-label="Send"], button[type="submit"], button.send-button`,
	})
	if _, err := globalBrowserRelay.SendCommand("click", submitParams, 10*time.Second); err != nil {
		return "", fmt.Errorf("submit question: %w", err)
	}

	// Wait for response to appear.
	time.Sleep(10 * time.Second)

	// Get the last response.
	js := `
		(function() {
			var responses = document.querySelectorAll('.response-text, .chat-message:last-child, [data-message-type="response"]:last-child');
			if (responses.length > 0) {
				return responses[responses.length - 1].textContent.trim();
			}
			var chat = document.querySelectorAll('[role="log"] > *, .chat-container > *');
			if (chat.length > 0) {
				return chat[chat.length - 1].textContent.trim();
			}
			return "No response found";
		})()
	`
	evalParams, _ := json.Marshal(map[string]string{"code": js})
	result, err := globalBrowserRelay.SendCommand("eval", evalParams, 15*time.Second)
	if err != nil {
		return "", fmt.Errorf("get response: %w", err)
	}
	return result, nil
}

// toolNotebookLMDeleteSource deletes a source from the current NotebookLM notebook.
func toolNotebookLMDeleteSource(ctx context.Context, cfg *Config, input json.RawMessage) (string, error) {
	var args struct {
		SourceName string `json:"source_name"`
		SourceID   string `json:"source_id"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if args.SourceName == "" && args.SourceID == "" {
		return "", fmt.Errorf("source_name or source_id is required")
	}

	if globalBrowserRelay == nil || !globalBrowserRelay.Connected() {
		return "", fmt.Errorf("browser extension not connected")
	}

	// Click on the source to select it.
	if args.SourceID != "" {
		selector := fmt.Sprintf(`[data-source-id="%s"]`, args.SourceID)
		clickParams, _ := json.Marshal(map[string]string{"selector": selector})
		if _, err := globalBrowserRelay.SendCommand("click", clickParams, 10*time.Second); err != nil {
			return "", fmt.Errorf("click source: %w", err)
		}
	} else {
		// Use source name to find the element via JS eval.
		js := fmt.Sprintf(`
			(function() {
				var items = document.querySelectorAll('.source-item, [data-source-id], .source-list-item, [role="listitem"]');
				for (var i = 0; i < items.length; i++) {
					if (items[i].textContent.includes(%q)) {
						items[i].click();
						return "found";
					}
				}
				return "not found";
			})()
		`, args.SourceName)
		evalParams, _ := json.Marshal(map[string]string{"code": js})
		result, err := globalBrowserRelay.SendCommand("eval", evalParams, 10*time.Second)
		if err != nil {
			return "", fmt.Errorf("find source: %w", err)
		}
		if result == `"not found"` || result == "not found" {
			return "", fmt.Errorf("source not found: %s", args.SourceName)
		}
	}
	time.Sleep(1 * time.Second)

	// Click delete/remove button.
	deleteParams, _ := json.Marshal(map[string]string{
		"selector": `button[aria-label="Delete"], button[aria-label="Remove"], button:has-text("Delete"), .delete-source-button`,
	})
	if _, err := globalBrowserRelay.SendCommand("click", deleteParams, 10*time.Second); err != nil {
		return "", fmt.Errorf("click delete: %w", err)
	}
	time.Sleep(1 * time.Second)

	// Confirm deletion if there's a confirmation dialog.
	confirmParams, _ := json.Marshal(map[string]string{
		"selector": `button:has-text("Confirm"), button:has-text("Yes"), button[aria-label="Confirm"]`,
	})
	globalBrowserRelay.SendCommand("click", confirmParams, 5*time.Second) // Best effort.

	name := args.SourceName
	if name == "" {
		name = args.SourceID
	}
	return fmt.Sprintf("deleted source: %s", name), nil
}
