package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// ReviewInfo mirrors config.ReviewConfig for CLI use.
type ReviewInfo struct {
	Queues       map[string][]string `json:"queues,omitempty"`
	DefaultAgent string              `json:"defaultAgent,omitempty"`
	MaxDiffLines int                 `json:"maxDiffLines,omitempty"`
	Model        string              `json:"model,omitempty"`
}

type reviewResponse struct {
	Status     string  `json:"status"`
	URL        string  `json:"url"`
	Agent      string  `json:"agent"`
	Model      string  `json:"model"`
	Output     string  `json:"output,omitempty"`
	Error      string  `json:"error,omitempty"`
	ExitCode   int     `json:"exitCode,omitempty"`
	DurationMs int64   `json:"durationMs"`
	CostUSD    float64 `json:"costUsd"`
}

// CmdReview handles `tetora review <url|shorthand>` and `tetora review --queue <name>`.
func CmdReview(args []string) {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printReviewUsage()
		if len(args) == 0 {
			os.Exit(1)
		}
		return
	}

	agent := ""
	model := ""
	queue := ""
	var targets []string

	i := 0
	for i < len(args) {
		switch args[i] {
		case "--agent", "-a":
			if i+1 < len(args) {
				agent = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--model", "-m":
			if i+1 < len(args) {
				model = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--queue", "-q":
			if i+1 < len(args) {
				queue = args[i+1]
				i += 2
			} else {
				i++
			}
		case "--help", "-h":
			printReviewUsage()
			return
		default:
			targets = append(targets, args[i])
			i++
		}
	}

	cfg := LoadCLIConfig(FindConfigPath())
	api := cfg.NewAPIClient()
	api.Client.Timeout = 0

	if queue != "" {
		urls, ok := cfg.Review.Queues[queue]
		if !ok || len(urls) == 0 {
			fmt.Fprintf(os.Stderr, "error: queue %q not configured in review.queues\n", queue)
			os.Exit(1)
		}
		targets = append(targets, urls...)
	}

	if len(targets) == 0 {
		fmt.Fprintln(os.Stderr, "error: no URL or queue provided")
		printReviewUsage()
		os.Exit(1)
	}

	anyFail := false
	for _, t := range targets {
		normalized, err := normalizeReviewTarget(t)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			anyFail = true
			continue
		}
		if !runReviewOne(api, cfg.ListenAddr, normalized, agent, model) {
			anyFail = true
		}
	}
	if anyFail {
		os.Exit(1)
	}
}

func runReviewOne(api *APIClient, listenAddr, url, agent, model string) bool {
	payload := map[string]any{"pr_url": url}
	if agent != "" {
		payload["agent"] = agent
	}
	if model != "" {
		payload["model"] = model
	}

	fmt.Fprintf(os.Stderr, "reviewing %s (via %s)...\n", url, listenAddr)
	start := time.Now()

	resp, err := api.PostJSON("/review", payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: cannot reach daemon at %s: %v\n", listenAddr, err)
		fmt.Fprintln(os.Stderr, "is the daemon running? try: tetora serve")
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == 409 {
		fmt.Fprintln(os.Stderr, "error: dispatch already running — retry after the current task finishes")
		return false
	}
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: review failed (HTTP %d): %s\n", resp.StatusCode, string(body))
		return false
	}

	var rr reviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		fmt.Fprintf(os.Stderr, "error: parse response: %v\n", err)
		return false
	}

	elapsed := time.Since(start)
	icon := "OK"
	if rr.Status != "ok" {
		icon = rr.Status
	}
	fmt.Fprintf(os.Stderr, "\n[%s] review %s ($%.2f, %s, %s)\n",
		icon, rr.URL, rr.CostUSD, rr.Model, elapsed.Round(time.Second))
	if rr.Output != "" {
		fmt.Println(rr.Output)
	}
	if rr.Error != "" {
		fmt.Fprintf(os.Stderr, "error: %s\n", rr.Error)
		return false
	}
	return true
}

// normalizeReviewTarget converts shorthand to a full URL.
// Accepts: full URL, owner/repo#NUM (github only).
func normalizeReviewTarget(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("empty target")
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s, nil
	}
	if idx := strings.Index(s, "#"); idx > 0 {
		repo := s[:idx]
		num := s[idx+1:]
		if strings.Count(repo, "/") == 1 && num != "" {
			return fmt.Sprintf("https://github.com/%s/pull/%s", repo, num), nil
		}
	}
	return "", fmt.Errorf("unrecognized target %q (use full URL or owner/repo#NUM)", s)
}

func printReviewUsage() {
	fmt.Fprint(os.Stderr, `tetora review — Dispatch a PR/MR review directly to the review agent (bypasses ruri triage)

Usage:
  tetora review <url> [options]
  tetora review <owner>/<repo>#<num> [options]
  tetora review --queue <name> [options]

Options:
  --agent, -a       Override review agent (default: config.review.defaultAgent or "kokuyou")
  --model, -m       Override model (default: config.review.model or "haiku")
  --queue, -q       Review all URLs in config.review.queues.<name>

Examples:
  tetora review https://github.com/TakumaLee/tetora/pull/99
  tetora review TakumaLee/tetora#99
  tetora review --queue github
  tetora review https://gitlab.com/acme/app/-/merge_requests/42 --model sonnet
`)
}
