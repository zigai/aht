// Command compatibility detects harness releases and runs CI installation tasks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

var markdownEscaper = strings.NewReplacer("|", "\\|", "\r", " ", "\n", " ")

type application struct {
	client *releaseClient
	getenv func(string) string
	stdout io.Writer
	stderr io.Writer
}

func main() {
	if err := runCommandLine(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runCommandLine() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	app := application{client: nil, getenv: os.Getenv, stdout: os.Stdout, stderr: os.Stderr}
	return app.run(ctx, os.Args[1:])
}

func (a application) run(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("%w: usage: compatibility probe|changes|detect|weekly|install|result|finish", errCompatibility)
	}
	switch args[0] {
	case "probe":
		return a.detectReleases(ctx, false)
	case "changes":
		return a.changes(ctx)
	case "detect":
		return a.detectReleases(ctx, true)
	case "weekly":
		return a.weekly()
	case "install":
		return a.install(ctx)
	case "result":
		return a.recordResult()
	case "finish":
		return a.finish(ctx)
	default:
		return fmt.Errorf("%w: unknown command %q", errCompatibility, args[0])
	}
}

func (a application) workDirectory() string {
	if value := a.getenv("AHT_COMPAT_WORK"); value != "" {
		return value
	}
	return filepath.Join(os.TempDir(), "aht-compatibility")
}

func (a application) releaseClient() *releaseClient {
	if a.client != nil {
		return a.client
	}
	return newReleaseClient(a.getenv("GH_TOKEN"))
}

func (a application) restore(ctx context.Context) (releaseState, error) {
	return a.releaseClient().restoreState(ctx, a.getenv("GITHUB_REPOSITORY"), a.getenv("AHT_DEFAULT_BRANCH"), a.getenv("GITHUB_RUN_ID"))
}

func (a application) detectReleases(ctx context.Context, persist bool) error {
	state := emptyState()
	if persist {
		var err error
		state, err = a.restore(ctx)
		if err != nil {
			return err
		}
	}
	selection := a.getenv("AHT_COMPAT_SELECTION")
	if selection == "" {
		selection = "all"
	}
	plan, err := detect(ctx, state, selection, a.getenv("AHT_COMPAT_FORCE") == "true", a.releaseClient().latest)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(a.stdout).Encode(plan); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	if !persist {
		if planHasErrors(plan) {
			return fmt.Errorf("%w: release sources failed; see observations", errCompatibility)
		}
		return nil
	}
	if err := a.writeJSON("plan.json", plan); err != nil {
		return err
	}
	if err := a.output("matrix", plan.Matrix); err != nil {
		return err
	}
	return a.output("changed", len(plan.Matrix.Include) > 0)
}

func planHasErrors(plan releasePlan) bool {
	for _, item := range plan.Observations {
		if item.Error != "" {
			return true
		}
	}
	return false
}

func (a application) weekly() error {
	entries := matrix{Include: []candidate{}}
	for _, spec := range catalog() {
		if spec.Source == "weekly" {
			entries.Include = append(entries.Include, candidate{Harness: spec.ID, Version: ""})
		}
	}
	return a.output("matrix", entries)
}

func (a application) recordResult() error {
	spec, err := findHarness(a.getenv("AHT_COMPAT_HARNESS"))
	if err != nil {
		return err
	}
	version := a.getenv("AHT_COMPAT_VERSION")
	if version == "" {
		version = "current"
	} else if _, err := parseVersion(version); err != nil {
		return err
	}
	result := hostResult{Harness: spec.ID, Version: version, Outcome: resultOutcome(a.getenv("AHT_INSTALL_OUTCOME"), a.getenv("AHT_TEST_OUTCOME"))}
	var mismatch error
	if result.Outcome == "success" && version != "current" {
		log, err := os.ReadFile(filepath.Join(a.workDirectory(), spec.ID+".log"))
		if err != nil {
			result.Outcome = "failure"
			writeErr := a.writeJSON(spec.ID+"-result.json", result)
			return errors.Join(fmt.Errorf("read lifecycle log: %w", err), writeErr)
		}
		if !checkedVersion(string(log), spec.ID, version) {
			result.Outcome = "failure"
			mismatch = fmt.Errorf("%w: lifecycle test did not observe requested %s version %s", errCompatibility, spec.ID, version)
		}
	}
	if err := a.writeJSON(spec.ID+"-result.json", result); err != nil {
		return err
	}
	return mismatch
}

func resultOutcome(install, test string) string {
	if install == "failure" || test == "failure" {
		return "failure"
	}
	if install == "success" && test == "success" {
		return "success"
	}
	return "incomplete"
}

func (a application) finish(ctx context.Context) error {
	var plan releasePlan
	if err := readJSON(filepath.Join(a.workDirectory(), "plan.json"), &plan); err != nil {
		return err
	}
	results, err := readResults(a.workDirectory(), plan)
	if err != nil {
		return err
	}
	// Restore again so rerunning an old workflow preserves more recent checks.
	state, err := a.restore(ctx)
	if err != nil {
		return err
	}
	runURL := ""
	if runID := a.getenv("GITHUB_RUN_ID"); runID != "" {
		server := a.getenv("GITHUB_SERVER_URL")
		if server == "" {
			server = "https://github.com"
		}
		if repo := a.getenv("GITHUB_REPOSITORY"); repo != "" {
			runURL = server + "/" + repo + "/actions/runs/" + runID
		}
	}
	next, incomplete, err := mergeResults(state, plan.Matrix.Include, results, runURL)
	if err != nil {
		return err
	}
	if err := a.writeJSON("state.json", next); err != nil {
		return err
	}
	if err := a.summary(releaseSummary(plan, next, incomplete)); err != nil {
		return err
	}
	failed := planHasErrors(plan) || len(incomplete) > 0 || selectedFailure(plan, results)
	return a.output("failed", failed)
}

func readResults(directory string, plan releasePlan) ([]hostResult, error) {
	results := make([]hostResult, 0, len(plan.Matrix.Include))
	for _, selected := range plan.Matrix.Include {
		var result hostResult
		path := filepath.Join(directory, selected.Harness+"-result.json")
		if err := readJSON(path, &result); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

func selectedFailure(plan releasePlan, results []hostResult) bool {
	for _, selected := range plan.Matrix.Include {
		for _, result := range results {
			if result.Harness == selected.Harness && result.Version == selected.Version && result.Outcome == "failure" {
				return true
			}
		}
	}
	return false
}

func releaseSummary(plan releasePlan, state releaseState, incomplete []string) string {
	var report strings.Builder
	fmt.Fprintf(&report, "## Release compatibility\n\n%d harness(es) selected; major/minor changes only.\n\n", len(plan.Matrix.Include))
	report.WriteString("| Harness | Latest observed | Last checked | Result |\n| --- | --- | --- | --- |\n")
	for _, spec := range catalog() {
		if spec.Source == "weekly" {
			continue
		}
		latest := latestObservation(plan, spec.ID)
		version, outcome := "none", "not checked"
		if previous := state.Harnesses[spec.ID]; previous.Source == spec.sourceKey() {
			version, outcome = previous.Version, previous.Outcome
		}
		fmt.Fprintf(&report, "| %s | %s | %s | %s |\n", spec.ID, markdownCell(latest), version, outcome)
	}
	if len(incomplete) > 0 {
		report.WriteString("\nIncomplete (will retry): " + strings.Join(incomplete, ", ") + ".\n")
	}
	return report.String()
}

func latestObservation(plan releasePlan, id string) string {
	for _, item := range plan.Observations {
		if item.Harness != id {
			continue
		}
		if item.Error != "" {
			return item.Error
		}
		return item.Version
	}
	return "not queried"
}

func markdownCell(value string) string {
	return markdownEscaper.Replace(value)
}

func (a application) writeJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	workDir := a.workDirectory()
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}
	targetPath := filepath.Join(workDir, name)
	tempFile, err := os.CreateTemp(workDir, name+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file for %s: %w", name, err)
	}
	tempName := tempFile.Name()
	defer func() { _ = os.Remove(tempName) }()
	if _, err := tempFile.Write(append(data, '\n')); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Chmod(tempName, 0o600); err != nil {
		return fmt.Errorf("chmod %s: %w", name, err)
	}
	if err := os.Rename(tempName, targetPath); err != nil {
		return fmt.Errorf("replace %s: %w", name, err)
	}
	return nil
}

func readJSON(path string, destination any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func (a application) output(name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode output %s: %w", name, err)
	}
	text := name + "=" + string(data) + "\n"
	if path := a.getenv("GITHUB_OUTPUT"); path != "" {
		return appendText(path, text)
	}
	if _, err := io.WriteString(a.stdout, text); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func (a application) summary(text string) error {
	if path := a.getenv("GITHUB_STEP_SUMMARY"); path != "" {
		return appendText(path, text)
	}
	if _, err := io.WriteString(a.stdout, text); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}

func appendText(path, text string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	_, writeErr := file.WriteString(text)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}
