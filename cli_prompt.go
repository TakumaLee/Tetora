package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
)

func cmdPrompt(args []string) {
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
	cfg := loadConfig(findConfigPath())
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
	cfg := loadConfig(findConfigPath())
	content, err := readPrompt(cfg, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(content)
}

func promptAdd(name string) {
	cfg := loadConfig(findConfigPath())

	// Check if already exists.
	if _, err := readPrompt(cfg, name); err == nil {
		fmt.Fprintf(os.Stderr, "Prompt %q already exists. Use 'tetora prompt edit %s' to modify.\n", name, name)
		os.Exit(1)
	}

	// Check if stdin has data (piped input).
	stat, _ := os.Stdin.Stat()
	var content string
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		// Reading from pipe.
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading stdin: %v\n", err)
			os.Exit(1)
		}
		content = string(data)
	} else {
		// Interactive: try $EDITOR, fallback to simple input.
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
	cfg := loadConfig(findConfigPath())

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
	cfg := loadConfig(findConfigPath())
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
