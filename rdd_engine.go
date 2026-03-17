package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tetora/internal/log"
)

const defaultStateTemplate = `# Project State (RDD)

## Objective
%s

## Constraints
- Security: Never expose sensitive credentials
- Quality: All code must be linted and tested
- Architecture: Follow existing project patterns

## Decisions
- (List architectural and technical decisions here)

## Current Status
- [ ] PENDING: Initial project setup

## Next Steps
1. Review requirements and gather context
2. Draft implementation plan
3. Execute plan
`

// EnsureStateFile checks if the STATE.md file exists in the workdir.
// If it does not exist, it creates a boilerplate STATE.md using the provided objective.
func EnsureStateFile(workdir string, fileName string, objective string) error {
	log.Info("EnsureStateFile called", "workdir", workdir, "fileName", fileName)
	if workdir == "" {
		return nil // Cannot ensure state file without a working directory
	}
	
	if fileName == "" {
		fileName = "STATE.md"
	}
	
	statePath := filepath.Join(workdir, fileName)
	
	// Check if file already exists
	if _, err := os.Stat(statePath); err == nil {
		return nil // File exists, nothing to do
	}
	
	// Make sure the directory exists
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		return fmt.Errorf("create workdir for state file: %w", err)
	}
	
	// If objective is empty, provide a generic one
	if strings.TrimSpace(objective) == "" {
		objective = "(Define the main objective here)"
	}
	
	content := fmt.Sprintf(defaultStateTemplate, strings.TrimSpace(objective))
	
	if err := os.WriteFile(statePath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write state file: %w", err)
	}
	
	return nil
}

// ReadState reads the contents of the STATE.md file from the workdir.
// Returns an empty string if the file doesn't exist.
func ReadState(workdir string, fileName string) (string, error) {
	if workdir == "" {
		return "", nil
	}
	
	if fileName == "" {
		fileName = "STATE.md"
	}
	
	statePath := filepath.Join(workdir, fileName)
	
	content, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // Return empty if not found
		}
		return "", fmt.Errorf("read state file: %w", err)
	}
	
	return string(content), nil
}

// ReadRequirements reads the REQUIREMENTS.md file if it exists.
func ReadRequirements(workdir string) (string, error) {
	if workdir == "" {
		return "", nil
	}
	
	reqPath := filepath.Join(workdir, "REQUIREMENTS.md")
	content, err := os.ReadFile(reqPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil // Optional file
		}
		return "", fmt.Errorf("read requirements file: %w", err)
	}
	
	return string(content), nil
}

// BuildResumeContext constructs the prompt payload for the `/rdd resume` command.
func BuildResumeContext(workdir string, stateFileName string) (string, error) {
	stateContent, err := ReadState(workdir, stateFileName)
	if err != nil {
		return "", err
	}
	if stateContent == "" {
		return "", fmt.Errorf("no state file found at %s", filepath.Join(workdir, stateFileName))
	}
	
	reqContent, err := ReadRequirements(workdir)
	if err != nil {
		log.Warn("failed to read requirements for resume", "error", err)
	}
	
	var sb strings.Builder
	sb.WriteString("RESUME TASK\n\n")
	sb.WriteString("Please resume execution based on the current project state.\n\n")
	
	sb.WriteString("## Project State (" + stateFileName + ")\n")
	sb.WriteString(stateContent)
	sb.WriteString("\n\n")
	
	if reqContent != "" {
		sb.WriteString("## Project Requirements (REQUIREMENTS.md)\n")
		sb.WriteString(reqContent)
		sb.WriteString("\n\n")
	}
	
	sb.WriteString("Analyze the `Current Status` and `Next Steps` sections, and continue with the highest priority pending item. Ensure you update the state file when completing tasks.")
	
	return sb.String(), nil
}
