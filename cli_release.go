package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func cmdRelease(args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	bump := fs.String("bump", "", "version bump type: patch, minor, or major")
	notes := fs.String("notes", "", "release notes (auto-generated from git log if omitted)")
	dryRun := fs.Bool("dry-run", false, "print what would be done without executing")
	skipTests := fs.Bool("skip-tests", false, "skip running go test")
	fs.Parse(args)

	rel := &releaseRunner{
		bump:      *bump,
		notes:     *notes,
		dryRun:    *dryRun,
		skipTests: *skipTests,
	}
	rel.run()
}

type releaseRunner struct {
	bump      string
	notes     string
	dryRun    bool
	skipTests bool

	currentVersion string
	nextVersion    string
	completed      []string // steps completed so far
}

func (r *releaseRunner) run() {
	if r.dryRun {
		fmt.Println("[dry-run] No changes will be made.")
	}

	// Step 1: Pre-flight
	r.step("Pre-flight checks", r.preflight)

	// Step 2: Version bump
	r.step("Version bump", r.versionBump)

	// Step 3: Build & test
	r.step("Build & test", r.buildAndTest)

	// Step 4: Commit & push
	r.step("Commit & push", r.commitAndPush)

	// Step 5: Cross-compile
	r.step("Cross-compile", r.crossCompile)

	// Step 6: Tag & publish
	r.step("Tag & publish", r.tagAndPublish)

	// Step 7: Local install
	r.step("Local install", r.localInstall)

	// Step 8: Summary
	r.summary()
}

func (r *releaseRunner) step(name string, fn func()) {
	fmt.Printf("── %s ──\n", name)
	fn()
	r.completed = append(r.completed, name)
	fmt.Println()
}

func (r *releaseRunner) fatal(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, "\nError: "+msg+"\n", args...)
	if len(r.completed) > 0 {
		fmt.Fprintf(os.Stderr, "\nCompleted steps before failure:\n")
		for i, s := range r.completed {
			fmt.Fprintf(os.Stderr, "  %d. %s\n", i+1, s)
		}
	}
	os.Exit(1)
}

// --- Step 1: Pre-flight ---

func (r *releaseRunner) preflight() {
	// Must be in a git repo.
	if err := execSilent("git", "rev-parse", "--git-dir"); err != nil {
		r.fatal("not a git repository")
	}
	fmt.Println("  git repo: ok")

	// Must be on main branch.
	branch := execOutput("git", "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main" {
		r.fatal("must be on main branch (current: %s)", branch)
	}
	fmt.Println("  branch: main")

	// gh CLI must be available.
	if _, err := exec.LookPath("gh"); err != nil {
		r.fatal("gh CLI not found — install from https://cli.github.com")
	}
	fmt.Println("  gh cli: ok")

	// Working tree check: allow clean or staged-only changes.
	status := execOutput("git", "status", "--porcelain")
	if status != "" {
		// Check for untracked or unstaged changes that aren't the files we'll modify.
		lines := strings.Split(strings.TrimSpace(status), "\n")
		for _, line := range lines {
			if len(line) < 2 {
				continue
			}
			// XY format: X=index, Y=worktree. Allow anything in index.
			// Warn about untracked (??) files but don't block.
			if strings.HasPrefix(line, "??") {
				fmt.Printf("  warning: untracked file: %s\n", strings.TrimSpace(line[3:]))
			}
		}
		fmt.Println("  working tree: has changes (will be included in commit)")
	} else {
		fmt.Println("  working tree: clean")
	}

	// Validate --bump flag if provided.
	if r.bump != "" && r.bump != "patch" && r.bump != "minor" && r.bump != "major" {
		r.fatal("invalid --bump value %q (must be patch, minor, or major)", r.bump)
	}

	// Read current version from Makefile.
	r.currentVersion = readMakefileVersion()
	fmt.Printf("  current version: %s\n", r.currentVersion)
}

// --- Step 2: Version bump ---

func (r *releaseRunner) versionBump() {
	if r.bump == "" {
		r.nextVersion = r.currentVersion
		fmt.Printf("  no --bump flag, using current version: %s\n", r.nextVersion)
		return
	}

	r.nextVersion = bumpVersion(r.currentVersion, r.bump)
	fmt.Printf("  version: %s → %s\n", r.currentVersion, r.nextVersion)

	if r.dryRun {
		fmt.Println("  [dry-run] would update Makefile and install.sh")
		return
	}

	// Update Makefile line 1.
	updateMakefileVersion(r.nextVersion)
	fmt.Println("  updated: Makefile")

	// Update install.sh line 5.
	updateInstallShVersion(r.nextVersion)
	fmt.Println("  updated: install.sh")
}

// --- Step 3: Build & test ---

func (r *releaseRunner) buildAndTest() {
	ldflags := fmt.Sprintf("-s -w -X main.tetoraVersion=%s", r.nextVersion)

	if r.dryRun {
		fmt.Printf("  [dry-run] would run: go build -ldflags %q .\n", ldflags)
		if !r.skipTests {
			fmt.Println("  [dry-run] would run: go test ./...")
		}
		return
	}

	fmt.Println("  building...")
	if err := execPassthrough("go", "build", "-ldflags", ldflags, "."); err != nil {
		r.fatal("build failed: %v", err)
	}
	fmt.Println("  build: ok")

	if r.skipTests {
		fmt.Println("  tests: skipped (--skip-tests)")
	} else {
		fmt.Println("  testing...")
		if err := execPassthrough("go", "test", "./..."); err != nil {
			r.fatal("tests failed: %v", err)
		}
		fmt.Println("  tests: ok")
	}
}

// --- Step 4: Commit & push ---

func (r *releaseRunner) commitAndPush() {
	tag := "v" + r.nextVersion

	// Build commit message.
	commitMsg := tag
	if r.notes != "" {
		commitMsg = tag + ": " + r.notes
	} else {
		// Auto-generate from git log.
		autoNotes := r.autoGenerateNotes()
		if autoNotes != "" {
			commitMsg = tag + ": " + autoNotes
		}
	}

	if r.dryRun {
		fmt.Printf("  [dry-run] would commit: %q\n", commitMsg)
		fmt.Println("  [dry-run] would push to origin main")
		return
	}

	// Stage all tracked changes + Makefile/install.sh.
	if err := execPassthrough("git", "add", "Makefile", "install.sh"); err != nil {
		r.fatal("git add failed: %v", err)
	}
	// Also stage any other tracked modified files.
	if err := execSilent("git", "add", "-u"); err != nil {
		r.fatal("git add -u failed: %v", err)
	}

	// Check if there's anything to commit.
	if err := execSilent("git", "diff", "--cached", "--quiet"); err == nil {
		fmt.Println("  nothing to commit, skipping")
	} else {
		if err := execPassthrough("git", "commit", "-m", commitMsg); err != nil {
			r.fatal("git commit failed: %v", err)
		}
		fmt.Printf("  committed: %s\n", commitMsg)
	}

	fmt.Println("  pushing to origin main...")
	if err := execPassthrough("git", "push", "origin", "main"); err != nil {
		r.fatal("git push failed: %v", err)
	}
	fmt.Println("  pushed: ok")
}

// --- Step 5: Cross-compile ---

func (r *releaseRunner) crossCompile() {
	if r.dryRun {
		fmt.Println("  [dry-run] would run: make release")
		return
	}

	fmt.Println("  running make release...")
	if err := execPassthrough("make", "release"); err != nil {
		r.fatal("make release failed: %v", err)
	}

	// Count binaries.
	entries, _ := os.ReadDir("dist")
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	fmt.Printf("  built %d binaries in dist/\n", count)
}

// --- Step 6: Tag & publish ---

func (r *releaseRunner) tagAndPublish() {
	tag := "v" + r.nextVersion

	if r.dryRun {
		fmt.Printf("  [dry-run] would create tag: %s\n", tag)
		fmt.Printf("  [dry-run] would push tag to origin\n")
		fmt.Printf("  [dry-run] would create GitHub release with dist/* binaries\n")
		return
	}

	// Check tag doesn't already exist.
	if err := execSilent("git", "rev-parse", tag); err == nil {
		r.fatal("tag %s already exists — delete it first or use a different version", tag)
	}

	// Create tag.
	if err := execPassthrough("git", "tag", tag); err != nil {
		r.fatal("git tag failed: %v", err)
	}
	fmt.Printf("  created tag: %s\n", tag)

	// Push tag.
	if err := execPassthrough("git", "push", "origin", tag); err != nil {
		r.fatal("git push tag failed: %v", err)
	}
	fmt.Println("  pushed tag: ok")

	// Build release notes.
	releaseNotes := r.notes
	if releaseNotes == "" {
		releaseNotes = r.autoGenerateNotes()
		if releaseNotes == "" {
			releaseNotes = fmt.Sprintf("Release %s", tag)
		}
	}

	// Collect dist files.
	distFiles, err := filepath.Glob("dist/*")
	if err != nil || len(distFiles) == 0 {
		r.fatal("no files found in dist/")
	}

	// gh release create.
	ghArgs := []string{"release", "create", tag}
	ghArgs = append(ghArgs, distFiles...)
	ghArgs = append(ghArgs, "--repo", "TakumaLee/Tetora", "--title", tag, "--notes", releaseNotes)
	if err := execPassthrough("gh", ghArgs...); err != nil {
		r.fatal("gh release create failed: %v", err)
	}
	fmt.Println("  GitHub release: created")
}

// --- Step 7: Local install ---

func (r *releaseRunner) localInstall() {
	home, err := os.UserHomeDir()
	if err != nil {
		r.fatal("cannot determine home directory: %v", err)
	}
	installDir := filepath.Join(home, ".tetora", "bin")
	destPath := filepath.Join(installDir, "tetora")

	if r.dryRun {
		fmt.Printf("  [dry-run] would copy tetora binary to %s\n", destPath)
		return
	}

	os.MkdirAll(installDir, 0o755)

	// Copy the freshly built binary.
	src, err := os.ReadFile("tetora")
	if err != nil {
		r.fatal("cannot read built binary: %v", err)
	}
	if err := os.WriteFile(destPath, src, 0o755); err != nil {
		r.fatal("cannot install binary to %s: %v", destPath, err)
	}
	fmt.Printf("  installed: %s\n", destPath)
}

// --- Step 8: Summary ---

func (r *releaseRunner) summary() {
	tag := "v" + r.nextVersion
	releaseURL := fmt.Sprintf("https://github.com/TakumaLee/Tetora/releases/tag/%s", tag)

	entries, _ := os.ReadDir("dist")
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}

	if r.dryRun {
		fmt.Println("── Summary (dry-run) ──")
		fmt.Printf("  Would release %s with %d binaries\n", tag, count)
		fmt.Printf("  Release URL: %s\n", releaseURL)
	} else {
		fmt.Println("── Done ──")
		fmt.Printf("  Released %s — %d binaries uploaded\n", tag, count)
		fmt.Printf("  %s\n", releaseURL)
	}
}

// --- Helpers ---

func readMakefileVersion() string {
	f, err := os.Open("Makefile")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read Makefile: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	if scanner.Scan() {
		line := scanner.Text()
		// Expected: "VERSION  := X.Y.Z"
		parts := strings.SplitN(line, ":=", 2)
		if len(parts) == 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	fmt.Fprintf(os.Stderr, "Error: cannot parse version from Makefile line 1\n")
	os.Exit(1)
	return ""
}

func bumpVersion(current, kind string) string {
	parts := strings.Split(current, ".")
	if len(parts) != 3 {
		fmt.Fprintf(os.Stderr, "Error: invalid version format %q (expected X.Y.Z)\n", current)
		os.Exit(1)
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	patch, _ := strconv.Atoi(parts[2])

	switch kind {
	case "patch":
		patch++
	case "minor":
		minor++
		patch = 0
	case "major":
		major++
		minor = 0
		patch = 0
	}
	return fmt.Sprintf("%d.%d.%d", major, minor, patch)
}

func updateMakefileVersion(version string) {
	data, err := os.ReadFile("Makefile")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read Makefile: %v\n", err)
		os.Exit(1)
	}
	lines := strings.SplitN(string(data), "\n", 2)
	lines[0] = "VERSION  := " + version
	if err := os.WriteFile("Makefile", []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot write Makefile: %v\n", err)
		os.Exit(1)
	}
}

func updateInstallShVersion(version string) {
	data, err := os.ReadFile("install.sh")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot read install.sh: %v\n", err)
		os.Exit(1)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.Contains(line, "TETORA_VERSION:-") {
			// Replace: local VERSION="${TETORA_VERSION:-X.Y.Z}"
			lines[i] = fmt.Sprintf(`    local VERSION="${TETORA_VERSION:-%s}"`, version)
			break
		}
	}
	if err := os.WriteFile("install.sh", []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot write install.sh: %v\n", err)
		os.Exit(1)
	}
}

func (r *releaseRunner) autoGenerateNotes() string {
	// Get last tag.
	lastTag := execOutput("git", "describe", "--tags", "--abbrev=0")
	var rangeSpec string
	if lastTag != "" {
		rangeSpec = lastTag + "..HEAD"
	} else {
		rangeSpec = "HEAD~10..HEAD"
	}
	log := execOutput("git", "log", rangeSpec, "--oneline")
	if log == "" {
		return ""
	}
	// Truncate to first 10 lines if too long.
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) > 10 {
		lines = lines[:10]
		lines = append(lines, fmt.Sprintf("... and %d more commits", len(strings.Split(log, "\n"))-10))
	}
	return strings.Join(lines, "\n")
}

// execSilent runs a command and returns error if non-zero exit.
func execSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

// execOutput runs a command and returns trimmed stdout.
func execOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// execPassthrough runs a command with stdout/stderr connected to the terminal.
func execPassthrough(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
