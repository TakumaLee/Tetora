package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

// PromptInfo represents a prompt template file.
type PromptInfo struct {
	Name    string `json:"name"`
	Preview string `json:"preview,omitempty"`
	Content string `json:"content,omitempty"`
}

func CmdPrompt(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora prompt <list|show|add|edit|remove> [name]")
		return
	}
	switch args[0] {
	case "list", "ls":
		promptList()
	case "show":
		if len(args) < 2 {
			fmt.Println("Usage: tetora prompt show <name>")
			return
		}
		promptShow(args[1])
	case "add":
		if len(args) < 2 {
			fmt.Println("Usage: tetora prompt add <name>")
			return
		}
		promptAdd(args[1])
	case "edit":
		if len(args) < 2 {
			fmt.Println("Usage: tetora prompt edit <name>")
			return
		}
		promptEdit(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Println("Usage: tetora prompt remove <name>")
			return
		}
		promptRemove(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown action: %s\n", args[0])
	}
}

func promptList() {
	cfg := LoadCLIConfig(FindConfigPath())
	prompts, err := listPrompts(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if len(prompts) == 0 {
		fmt.Println("No prompts found.")
		fmt.Printf("Add one with: tetora prompt add <name>\n")
		fmt.Printf("Directory: %s\n", promptsDir(cfg))
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tPREVIEW\n")
	for _, p := range prompts {
		fmt.Fprintf(w, "%s\t%s\n", p.Name, p.Preview)
	}
	w.Flush()
	fmt.Printf("\n%d prompts in %s\n", len(prompts), promptsDir(cfg))
}

func promptShow(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	content, err := readPrompt(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(content)
}

func promptAdd(name string) {
	cfg := LoadCLIConfig(FindConfigPath())

	if _, err := readPrompt(cfg, name); err == nil {
		fmt.Fprintf(os.Stderr, "Prompt %q already exists. Use 'tetora prompt edit %s' to modify.\n", name, name)
		os.Exit(1)
	}

	stat, _ := os.Stdin.Stat()
	var content string
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		content = string(data)
	} else {
		editor := os.Getenv("EDITOR")
		if editor != "" {
			content = editWithEditor(editor, "")
		} else {
			fmt.Println("Enter prompt content (end with Ctrl+D):")
			fmt.Println("---")
			scanner := bufio.NewScanner(os.Stdin)
			var lines []string
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
			}
			content = strings.Join(lines, "\n") + "\n"
		}
	}

	if strings.TrimSpace(content) == "" {
		fmt.Println("Empty content, aborting.")
		return
	}

	if err := writePrompt(cfg, name, content); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Prompt %q saved.\n", name)
}

func promptEdit(name string) {
	cfg := LoadCLIConfig(FindConfigPath())

	existing, err := readPrompt(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}

	content := editWithEditor(editor, existing)
	if content == existing {
		fmt.Println("No changes made.")
		return
	}

	if err := writePrompt(cfg, name, content); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Prompt %q updated.\n", name)
}

func promptRemove(name string) {
	cfg := LoadCLIConfig(FindConfigPath())
	if err := deletePrompt(cfg, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Prompt %q removed.\n", name)
}

// editWithEditor opens content in $EDITOR and returns the result.
func editWithEditor(editor, initial string) string {
	tmpFile, err := os.CreateTemp("", "tetora-prompt-*.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating temp file: %v\n", err)
		os.Exit(1)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if initial != "" {
		tmpFile.WriteString(initial)
	}
	tmpFile.Close()

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Editor exited with error: %v\n", err)
		os.Exit(1)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading edited file: %v\n", err)
		os.Exit(1)
	}
	return string(data)
}

// --- Prompt FS operations (replicated from root prompt.go) ---

func promptsDir(cfg *CLIConfig) string {
	dir := filepath.Join(cfg.BaseDir, "prompts")
	os.MkdirAll(dir, 0o755)
	return dir
}

func listPrompts(cfg *CLIConfig) ([]PromptInfo, error) {
	dir := promptsDir(cfg)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var prompts []PromptInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		preview := ""
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err == nil {
			preview = string(data)
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			preview = strings.ReplaceAll(preview, "\n", " ")
		}
		prompts = append(prompts, PromptInfo{Name: name, Preview: preview})
	}

	sort.Slice(prompts, func(i, j int) bool {
		return prompts[i].Name < prompts[j].Name
	})
	return prompts, nil
}

func readPrompt(cfg *CLIConfig, name string) (string, error) {
	path := filepath.Join(promptsDir(cfg), name+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("prompt %q not found", name)
	}
	return string(data), nil
}

func writePrompt(cfg *CLIConfig, name, content string) error {
	if name == "" {
		return fmt.Errorf("prompt name is required")
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return fmt.Errorf("invalid character %q in prompt name (use a-z, 0-9, -, _)", string(r))
		}
	}
	path := filepath.Join(promptsDir(cfg), name+".md")
	return os.WriteFile(path, []byte(content), 0o644)
}

func deletePrompt(cfg *CLIConfig, name string) error {
	path := filepath.Join(promptsDir(cfg), name+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("prompt %q not found", name)
	}
	return os.Remove(path)
}
