package cli

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

func CmdRelease(args []string) {
	fs := flag.NewFlagSet("release", flag.ExitOnError)
	bump := fs.String("bump", "", "version bump type: patch, minor, or major")
	notes := fs.String("notes", "", "release notes (auto-generated from git log if omitted)")
	dryRun := fs.Bool("dry-run", false, "print what would be done without executing")
	skipTests := fs.Bool("skip-tests", false, "skip running go test")
	fs.Parse(args) //nolint:errcheck

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
	completed      []string
}

func (r *releaseRunner) run() {
	if r.dryRun {
		fmt.Println("[dry-run] No changes will be made.")
	}

	r.step("Pre-flight checks", r.preflight)
	r.step("Version bump", r.versionBump)
	r.step("Build & test", r.buildAndTest)
	r.step("Commit & push", r.commitAndPush)
	r.step("Cross-compile", r.crossCompile)
	r.step("Tag & publish", r.tagAndPublish)
	r.step("Local install", r.localInstall)
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

func (r *releaseRunner) preflight() {
	if err := releaseExecSilent("git", "rev-parse", "--git-dir"); err != nil {
		r.fatal("not a git repository")
	}
	fmt.Println("  git repo: ok")

	branch := releaseExecOutput("git", "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "main" {
		r.fatal("must be on main branch (current: %s)", branch)
	}
	fmt.Println("  branch: main")

	if _, err := exec.LookPath("gh"); err != nil {
		r.fatal("gh CLI not found — install from https://cli.github.com")
	}
	fmt.Println("  gh cli: ok")

	status := releaseExecOutput("git", "status", "--porcelain")
	if status != "" {
		lines := strings.Split(strings.TrimSpace(status), "\n")
		for _, line := range lines {
			if len(line) < 2 {
				continue
			}
			if strings.HasPrefix(line, "??") {
				fmt.Printf("  warning: untracked file: %s\n", strings.TrimSpace(line[3:]))
			}
		}
		fmt.Println("  working tree: has changes (will be included in commit)")
	} else {
		fmt.Println("  working tree: clean")
	}

	if r.bump != "" && r.bump != "patch" && r.bump != "minor" && r.bump != "major" {
		r.fatal("invalid --bump value %q (must be patch, minor, or major)", r.bump)
	}

	r.currentVersion = readMakefileVersion()
	fmt.Printf("  current version: %s\n", r.currentVersion)
}

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

	updateMakefileVersion(r.nextVersion)
	fmt.Println("  updated: Makefile")

	updateInstallShVersion(r.nextVersion)
	fmt.Println("  updated: install.sh")
}

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
	if err := releaseExecPassthrough("go", "build", "-ldflags", ldflags, "."); err != nil {
		r.fatal("build failed: %v", err)
	}
	fmt.Println("  build: ok")

	if r.skipTests {
		fmt.Println("  tests: skipped (--skip-tests)")
	} else {
		fmt.Println("  testing...")
		if err := releaseExecPassthrough("go", "test", "./..."); err != nil {
			r.fatal("tests failed: %v", err)
		}
		fmt.Println("  tests: ok")
	}
}

func (r *releaseRunner) commitAndPush() {
	tag := "v" + r.nextVersion
	commitMsg := tag
	if r.notes != "" {
		commitMsg = tag + ": " + r.notes
	} else {
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

	if err := releaseExecPassthrough("git", "add", "Makefile", "install.sh"); err != nil {
		r.fatal("git add failed: %v", err)
	}
	if err := releaseExecSilent("git", "add", "-u"); err != nil {
		r.fatal("git add -u failed: %v", err)
	}

	if err := releaseExecSilent("git", "diff", "--cached", "--quiet"); err == nil {
		fmt.Println("  nothing to commit, skipping")
	} else {
		if err := releaseExecPassthrough("git", "commit", "-m", commitMsg); err != nil {
			r.fatal("git commit failed: %v", err)
		}
		fmt.Printf("  committed: %s\n", commitMsg)
	}

	fmt.Println("  pushing to origin main...")
	if err := releaseExecPassthrough("git", "push", "origin", "main"); err != nil {
		r.fatal("git push failed: %v", err)
	}
	fmt.Println("  pushed: ok")
}

func (r *releaseRunner) crossCompile() {
	if r.dryRun {
		fmt.Println("  [dry-run] would run: make release")
		return
	}

	fmt.Println("  running make release...")
	if err := releaseExecPassthrough("make", "release"); err != nil {
		r.fatal("make release failed: %v", err)
	}

	entries, _ := os.ReadDir("dist")
	count := 0
	for _, e := range entries {
		if !e.IsDir() {
			count++
		}
	}
	fmt.Printf("  built %d binaries in dist/\n", count)
}

func (r *releaseRunner) tagAndPublish() {
	tag := "v" + r.nextVersion

	if r.dryRun {
		fmt.Printf("  [dry-run] would create tag: %s\n", tag)
		fmt.Printf("  [dry-run] would push tag to origin\n")
		fmt.Printf("  [dry-run] would create GitHub release with dist/* binaries\n")
		return
	}

	if err := releaseExecSilent("git", "rev-parse", tag); err == nil {
		r.fatal("tag %s already exists — delete it first or use a different version", tag)
	}

	if err := releaseExecPassthrough("git", "tag", tag); err != nil {
		r.fatal("git tag failed: %v", err)
	}
	fmt.Printf("  created tag: %s\n", tag)

	if err := releaseExecPassthrough("git", "push", "origin", tag); err != nil {
		r.fatal("git push tag failed: %v", err)
	}
	fmt.Println("  pushed tag: ok")

	releaseNotes := r.notes
	if releaseNotes == "" {
		releaseNotes = r.autoGenerateNotes()
		if releaseNotes == "" {
			releaseNotes = fmt.Sprintf("Release %s", tag)
		}
	}

	distFiles, err := filepath.Glob("dist/*")
	if err != nil || len(distFiles) == 0 {
		r.fatal("no files found in dist/")
	}

	ghArgs := []string{"release", "create", tag}
	ghArgs = append(ghArgs, distFiles...)
	ghArgs = append(ghArgs, "--repo", "TakumaLee/Tetora", "--title", tag, "--notes", releaseNotes)
	if err := releaseExecPassthrough("gh", ghArgs...); err != nil {
		r.fatal("gh release create failed: %v", err)
	}
	fmt.Println("  GitHub release: created")
}

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

	src, err := os.ReadFile("tetora")
	if err != nil {
		r.fatal("cannot read built binary: %v", err)
	}
	if err := os.WriteFile(destPath, src, 0o755); err != nil {
		r.fatal("cannot install binary to %s: %v", destPath, err)
	}
	fmt.Printf("  installed: %s\n", destPath)
}

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

func (r *releaseRunner) autoGenerateNotes() string {
	lastTag := releaseExecOutput("git", "describe", "--tags", "--abbrev=0")
	var rangeSpec string
	if lastTag != "" {
		rangeSpec = lastTag + "..HEAD"
	} else {
		rangeSpec = "HEAD~10..HEAD"
	}
	log := releaseExecOutput("git", "log", rangeSpec, "--oneline")
	if log == "" {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(log), "\n")
	if len(lines) > 10 {
		lines = lines[:10]
		lines = append(lines, fmt.Sprintf("... and %d more commits", len(strings.Split(log, "\n"))-10))
	}
	return strings.Join(lines, "\n")
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
			lines[i] = fmt.Sprintf(`    local VERSION="${TETORA_VERSION:-%s}"`, version)
			break
		}
	}
	if err := os.WriteFile("install.sh", []byte(strings.Join(lines, "\n")), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot write install.sh: %v\n", err)
		os.Exit(1)
	}
}

func releaseExecSilent(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run()
}

func releaseExecOutput(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func releaseExecPassthrough(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
