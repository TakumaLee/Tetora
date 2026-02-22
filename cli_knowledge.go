package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func cmdKnowledge(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: tetora knowledge <list|add|remove|search|path> [options]")
		fmt.Println()
		fmt.Println("Commands:")
		fmt.Println("  list              List files in knowledge base")
		fmt.Println("  add <file>        Copy file to knowledge base")
		fmt.Println("  remove <name>     Remove file from knowledge base")
		fmt.Println("  search <query>    Search knowledge base (TF-IDF)")
		fmt.Println("  path              Show knowledge base directory path")
		return
	}
	switch args[0] {
	case "list", "ls":
		knowledgeList()
	case "add":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora knowledge add <file>")
			os.Exit(1)
		}
		knowledgeAdd(args[1])
	case "remove", "rm":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora knowledge remove <name>")
			os.Exit(1)
		}
		knowledgeRemove(args[1])
	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: tetora knowledge search <query>")
			os.Exit(1)
		}
		knowledgeSearch(strings.Join(args[1:], " "))
	case "path":
		knowledgePath()
	default:
		fmt.Fprintf(os.Stderr, "Unknown knowledge action: %s\n", args[0])
		os.Exit(1)
	}
}

func knowledgeList() {
	cfg := loadConfig(findConfigPath())
	dir := knowledgeDir(cfg)

	files, err := listKnowledgeFiles(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(files) == 0 {
		fmt.Println("No files in knowledge base.")
		fmt.Printf("Add files with: tetora knowledge add <file>\n")
		fmt.Printf("Directory: %s\n", dir)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE\tMODIFIED")
	for _, f := range files {
		fmt.Fprintf(w, "%s\t%s\t%s\n", f.Name, formatSize(f.Size), f.ModTime)
	}
	w.Flush()
	fmt.Printf("\n%d files in %s\n", len(files), dir)
}

func knowledgeAdd(filePath string) {
	cfg := loadConfig(findConfigPath())
	dir := knowledgeDir(cfg)

	if err := addKnowledgeFile(dir, filePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Added %q to knowledge base.\n", filePath)
}

func knowledgeRemove(name string) {
	cfg := loadConfig(findConfigPath())
	dir := knowledgeDir(cfg)

	if err := removeKnowledgeFile(dir, name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed %q from knowledge base.\n", name)
}

func knowledgeSearch(query string) {
	cfg := loadConfig(findConfigPath())
	dir := knowledgeDir(cfg)

	idx, err := buildKnowledgeIndex(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error building index: %v\n", err)
		os.Exit(1)
	}

	results := idx.search(query, 10)
	if len(results) == 0 {
		fmt.Printf("No results for %q in knowledge base.\n", query)
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "RANK\tFILE\tSCORE\tSNIPPET")
	for i, r := range results {
		// Replace newlines in snippet for table display.
		snippet := strings.ReplaceAll(r.Snippet, "\n", " ")
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		fmt.Fprintf(w, "%d\t%s\t%.4f\t%s\n", i+1, r.Filename, r.Score, snippet)
	}
	w.Flush()
	fmt.Printf("\n%d results for %q\n", len(results), query)
}

func knowledgePath() {
	cfg := loadConfig(findConfigPath())
	fmt.Println(knowledgeDir(cfg))
}

