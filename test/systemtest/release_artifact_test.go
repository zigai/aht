//go:build integration

package systemtest

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	artifactDirEnv          = "AHT_ARTIFACT_DIR"
	publishedArtifactDirEnv = "AHT_PUBLISHED_ARTIFACT_DIR"
)

type releaseArtifactExtra struct {
	Format string `json:"Format"`
}

type releaseArtifact struct {
	Name   string               `json:"name"`
	Path   string               `json:"path"`
	GOOS   string               `json:"goos"`
	GOARCH string               `json:"goarch"`
	Type   string               `json:"type"`
	Extra  releaseArtifactExtra `json:"extra"`
}

type releaseMetadata struct {
	ProjectName string `json:"project_name"`
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Date        string `json:"date"`
}

type releaseManifest struct {
	Dir       string
	Artifacts []releaseArtifact
	Metadata  releaseMetadata
}

type releaseVersion struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
}

type releaseSession struct {
	SchemaVersion int    `json:"schema_version"`
	SessionID     string `json:"session_id"`
	Harness       string `json:"harness"`
}

func TestReleaseArtifacts(t *testing.T) {
	artifactDir := os.Getenv(artifactDirEnv)
	if artifactDir == "" {
		t.Skipf("%s is not set", artifactDirEnv)
	}

	manifest, err := loadArtifactManifest(artifactDir)
	if err != nil {
		t.Fatalf("load artifact manifest: %v", err)
	}
	if err := validateArtifactInventory(manifest); err != nil {
		t.Fatalf("validate artifact inventory: %v", err)
	}
	if err := validateChecksums(manifest); err != nil {
		t.Fatalf("validate checksums: %v", err)
	}
	if runtime.GOOS == "linux" {
		if err := validateLinuxPackages(manifest); err != nil {
			t.Fatalf("validate Linux packages: %v", err)
		}
	}
	if err := validateNativeArchive(manifest, runtime.GOOS, runtime.GOARCH); err != nil {
		t.Fatalf("validate native archive: %v", err)
	}

	publishedDir := os.Getenv(publishedArtifactDirEnv)
	if publishedDir == "" {
		return
	}
	if err := validatePublishedAssets(manifest, publishedDir); err != nil {
		t.Fatalf("validate published assets: %v", err)
	}
}

func loadArtifactManifest(dir string) (releaseManifest, error) {
	artifactDir, err := resolveArtifactDir(dir)
	if err != nil {
		return releaseManifest{}, err
	}

	manifest := releaseManifest{Dir: artifactDir}
	if err := decodeJSONFile(filepath.Join(manifest.Dir, "artifacts.json"), &manifest.Artifacts); err != nil {
		return releaseManifest{}, fmt.Errorf("decode artifacts.json: %w", err)
	}
	if err := decodeJSONFile(filepath.Join(manifest.Dir, "metadata.json"), &manifest.Metadata); err != nil {
		return releaseManifest{}, fmt.Errorf("decode metadata.json: %w", err)
	}
	if manifest.Metadata.ProjectName != "aht" || manifest.Metadata.Version == "" || manifest.Metadata.Commit == "" || manifest.Metadata.Date == "" {
		return releaseManifest{}, fmt.Errorf("metadata does not identify a complete aht build")
	}
	return manifest, nil
}

func resolveArtifactDir(dir string) (string, error) {
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir), nil
	}
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve artifact directory: %w", err)
	}
	for candidateRoot := workingDir; ; candidateRoot = filepath.Dir(candidateRoot) {
		candidate := filepath.Join(candidateRoot, dir)
		if _, err := os.Stat(filepath.Join(candidate, "artifacts.json")); err == nil {
			return filepath.Clean(candidate), nil
		}
		parent := filepath.Dir(candidateRoot)
		if parent == candidateRoot {
			break
		}
	}
	return filepath.Clean(filepath.Join(workingDir, dir)), nil
}

func decodeJSONFile(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(path), err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s: trailing JSON content", filepath.Base(path))
	}
	return nil
}

func validateArtifactInventory(manifest releaseManifest) error {
	expectedArchives := map[string]struct{}{
		"darwin/amd64": {},
		"darwin/arm64": {},
		"linux/amd64":  {},
		"linux/arm64":  {},
	}
	expectedPackages := map[string]struct{}{
		"amd64/deb": {},
		"amd64/rpm": {},
		"arm64/deb": {},
		"arm64/rpm": {},
	}
	archives := make(map[string]struct{})
	packages := make(map[string]struct{})
	downloadNames := make(map[string]struct{})
	checksumCount := 0

	for _, artifact := range manifest.Artifacts {
		if artifact.Type != "Archive" && artifact.Type != "Linux Package" && artifact.Type != "Checksum" {
			continue
		}
		if _, err := artifactDiskPath(manifest, artifact); err != nil {
			return err
		}
		if _, exists := downloadNames[artifact.Name]; exists {
			return fmt.Errorf("duplicate downloadable artifact name %q", artifact.Name)
		}
		downloadNames[artifact.Name] = struct{}{}

		switch artifact.Type {
		case "Archive":
			if artifact.Extra.Format != "tar.gz" {
				return fmt.Errorf("archive %q uses format %q, want tar.gz", artifact.Name, artifact.Extra.Format)
			}
			if err := addUniqueTarget(archives, artifact.GOOS+"/"+artifact.GOARCH, "archive"); err != nil {
				return err
			}
		case "Linux Package":
			if artifact.GOOS != "linux" || (artifact.Extra.Format != "deb" && artifact.Extra.Format != "rpm") {
				return fmt.Errorf("package %q has unsupported target %s/%s format %q", artifact.Name, artifact.GOOS, artifact.GOARCH, artifact.Extra.Format)
			}
			if err := addUniqueTarget(packages, artifact.GOARCH+"/"+artifact.Extra.Format, "package"); err != nil {
				return err
			}
		case "Checksum":
			if artifact.Name != "checksums.txt" {
				return fmt.Errorf("checksum artifact is named %q, want checksums.txt", artifact.Name)
			}
			checksumCount++
		}
	}

	if err := requireExactSet("archive targets", archives, expectedArchives); err != nil {
		return err
	}
	if err := requireExactSet("package targets", packages, expectedPackages); err != nil {
		return err
	}
	if checksumCount != 1 {
		return fmt.Errorf("found %d checksum artifacts, want 1", checksumCount)
	}
	return nil
}

func artifactDiskPath(manifest releaseManifest, artifact releaseArtifact) (string, error) {
	if artifact.Name == "" || artifact.Name != filepath.Base(artifact.Name) {
		return "", fmt.Errorf("artifact name %q is not a basename", artifact.Name)
	}
	path := artifact.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(manifest.Dir), path)
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) != manifest.Dir || filepath.Base(path) != artifact.Name {
		return "", fmt.Errorf("artifact %q is not a top-level file in %q", artifact.Name, manifest.Dir)
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat artifact %q: %w", artifact.Name, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("artifact %q is not a regular file", artifact.Name)
	}
	return path, nil
}

func addUniqueTarget(targets map[string]struct{}, key string, kind string) error {
	if _, exists := targets[key]; exists {
		return fmt.Errorf("duplicate %s target %q", kind, key)
	}
	targets[key] = struct{}{}
	return nil
}

func requireExactSet(kind string, actual map[string]struct{}, expected map[string]struct{}) error {
	missing := make([]string, 0)
	extra := make([]string, 0)
	for value := range expected {
		if _, exists := actual[value]; !exists {
			missing = append(missing, value)
		}
	}
	for value := range actual {
		if _, exists := expected[value]; !exists {
			extra = append(extra, value)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) != 0 || len(extra) != 0 {
		return fmt.Errorf("%s mismatch: missing=%v extra=%v", kind, missing, extra)
	}
	return nil
}

func validateChecksums(manifest releaseManifest) error {
	expected := make(map[string]releaseArtifact)
	var checksumArtifact releaseArtifact
	for _, artifact := range manifest.Artifacts {
		switch artifact.Type {
		case "Archive", "Linux Package":
			expected[artifact.Name] = artifact
		case "Checksum":
			checksumArtifact = artifact
		}
	}

	checksumPath, err := artifactDiskPath(manifest, checksumArtifact)
	if err != nil {
		return err
	}
	checksums, err := parseChecksums(checksumPath)
	if err != nil {
		return err
	}
	expectedNames := make(map[string]struct{}, len(expected))
	actualNames := make(map[string]struct{}, len(checksums))
	for name := range expected {
		expectedNames[name] = struct{}{}
	}
	for name := range checksums {
		actualNames[name] = struct{}{}
	}
	if err := requireExactSet("checksum inventory", actualNames, expectedNames); err != nil {
		return err
	}

	for name, artifact := range expected {
		path, err := artifactDiskPath(manifest, artifact)
		if err != nil {
			return err
		}
		digest, err := fileSHA256(path)
		if err != nil {
			return fmt.Errorf("hash %q: %w", name, err)
		}
		if hex.EncodeToString(digest) != checksums[name] {
			return fmt.Errorf("SHA-256 mismatch for %q", name)
		}
	}
	return nil
}

func parseChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open checksums.txt: %w", err)
	}
	defer file.Close()

	checksums := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			return nil, fmt.Errorf("invalid checksums.txt line %q", scanner.Text())
		}
		name := fields[1]
		if name != filepath.Base(name) || filepath.IsAbs(name) {
			return nil, fmt.Errorf("checksum name %q is not a basename", name)
		}
		if _, exists := checksums[name]; exists {
			return nil, fmt.Errorf("duplicate checksum entry for %q", name)
		}
		digest := strings.ToLower(fields[0])
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("invalid SHA-256 digest for %q", name)
		}
		checksums[name] = digest
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read checksums.txt: %w", err)
	}
	return checksums, nil
}

func fileSHA256(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

func validateNativeArchive(manifest releaseManifest, goos string, goarch string) error {
	artifact, err := findArtifact(manifest, "Archive", goos, goarch, "tar.gz")
	if err != nil {
		return fmt.Errorf("missing native archive: %w", err)
	}
	archivePath, err := artifactDiskPath(manifest, artifact)
	if err != nil {
		return err
	}

	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open native archive: %w", err)
	}
	defer archiveFile.Close()
	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return fmt.Errorf("open native archive gzip stream: %w", err)
	}
	defer gzipReader.Close()

	extractDir, err := os.MkdirTemp("", "aht-release-")
	if err != nil {
		return fmt.Errorf("create extraction directory: %w", err)
	}
	defer os.RemoveAll(extractDir)
	binaryPath := filepath.Join(extractDir, "aht")
	required := map[string]bool{"aht": false, "LICENSE": false, "README.md": false}
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read native archive: %w", err)
		}
		found, requiredMember := required[header.Name]
		if !requiredMember {
			continue
		}
		if found {
			return fmt.Errorf("native archive contains duplicate %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return fmt.Errorf("native archive member %q is not a regular file", header.Name)
		}
		required[header.Name] = true
		if header.Name != "aht" {
			continue
		}
		if header.FileInfo().Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("native archive binary is not executable")
		}
		binaryFile, err := os.OpenFile(binaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
		if err != nil {
			return fmt.Errorf("create extracted binary: %w", err)
		}
		_, copyErr := io.Copy(binaryFile, tarReader)
		closeErr := binaryFile.Close()
		if copyErr != nil {
			return fmt.Errorf("extract native binary: %w", copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close extracted binary: %w", closeErr)
		}
	}
	missing := make([]string, 0)
	for name, found := range required {
		if !found {
			missing = append(missing, name)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return fmt.Errorf("native archive is missing public files: %v", missing)
	}
	return verifyReleaseBinaryBehavior(binaryPath, manifest.Metadata)
}

func verifyReleaseBinaryBehavior(binary string, metadata releaseMetadata) error {
	isolatedRoot, err := os.MkdirTemp("", "aht-release-state-")
	if err != nil {
		return fmt.Errorf("create isolated state directory: %w", err)
	}
	defer os.RemoveAll(isolatedRoot)
	workingDir, err := os.MkdirTemp("", "aht-release-work-")
	if err != nil {
		return fmt.Errorf("create working directory: %w", err)
	}
	defer os.RemoveAll(workingDir)

	home := filepath.Join(isolatedRoot, "home")
	configHome := filepath.Join(isolatedRoot, "config")
	stateDir := filepath.Join(isolatedRoot, "state")
	for _, dir := range []string{home, configHome, stateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create isolated directory %q: %w", dir, err)
		}
	}
	environment := isolatedEnvironment(home, configHome, stateDir)

	versionOutput, err := runReleaseCommand(binary, workingDir, environment, "--json", "--version")
	if err != nil {
		return err
	}
	var version releaseVersion
	if err := json.Unmarshal(versionOutput, &version); err != nil {
		return fmt.Errorf("decode version output %q: %w", versionOutput, err)
	}
	if version.Version != metadata.Version {
		return fmt.Errorf("version mismatch: binary=%q metadata=%q", version.Version, metadata.Version)
	}
	if version.Commit == "" || !strings.HasPrefix(metadata.Commit, version.Commit) {
		return fmt.Errorf("commit mismatch: binary=%q metadata=%q", version.Commit, metadata.Commit)
	}
	if !releaseDatesEqual(version.Built, metadata.Date) {
		return fmt.Errorf("build date mismatch: binary=%q metadata=%q", version.Built, metadata.Date)
	}

	storePath := filepath.Join(isolatedRoot, "state.json")
	reportOutput, err := runReleaseCommand(binary, workingDir, environment, "--store", storePath, "--json", "report", "codex", "--session-id", "release-verification", "--event", "start", "--no-tmux")
	if err != nil {
		return err
	}
	var reported releaseSession
	if err := json.Unmarshal(reportOutput, &reported); err != nil {
		return fmt.Errorf("decode report output %q: %w", reportOutput, err)
	}
	if reported.SchemaVersion != 2 || reported.SessionID != "release-verification" || reported.Harness != "codex" {
		return fmt.Errorf("report output does not contain the schema-v2 Codex session: %q", reportOutput)
	}

	listOutput, err := runReleaseCommand(binary, workingDir, environment, "--store", storePath, "--json", "list")
	if err != nil {
		return err
	}
	var sessions []releaseSession
	if err := json.Unmarshal(listOutput, &sessions); err != nil {
		return fmt.Errorf("decode list output %q: %w", listOutput, err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != reported.SessionID || sessions[0].Harness != reported.Harness {
		return fmt.Errorf("list output does not contain exactly the reported session: %q", listOutput)
	}
	return nil
}

func isolatedEnvironment(home string, configHome string, stateDir string) []string {
	return systemTestEnvironment(home, configHome, stateDir)
}

func releaseDatesEqual(binaryDate string, metadataDate string) bool {
	binaryTime, err := time.Parse(time.RFC3339Nano, binaryDate)
	if err != nil {
		return false
	}
	metadataTime, err := time.Parse(time.RFC3339Nano, metadataDate)
	if err != nil {
		return false
	}
	return binaryTime.Equal(metadataTime.Truncate(time.Second))
}

func runReleaseCommand(binary string, dir string, environment []string, args ...string) ([]byte, error) {
	command := exec.Command(binary, args...)
	command.Dir = dir
	command.Env = environment
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("run %q %q: %w; stdout=%q stderr=%q", binary, args, err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		return nil, fmt.Errorf("run %q %q wrote stderr=%q", binary, args, stderr.String())
	}
	return stdout.Bytes(), nil
}

func validateLinuxPackages(manifest releaseManifest) error {
	dpkgDeb, err := exec.LookPath("dpkg-deb")
	if err != nil {
		return fmt.Errorf("dpkg-deb is required to inspect release .deb packages; install dpkg: %w", err)
	}
	rpm, err := exec.LookPath("rpm")
	if err != nil {
		return fmt.Errorf("rpm is required to inspect release .rpm packages; install rpm: %w", err)
	}
	rpmDB, err := os.MkdirTemp("", "aht-rpm-db-")
	if err != nil {
		return fmt.Errorf("create temporary RPM database: %w", err)
	}
	defer os.RemoveAll(rpmDB)

	for _, artifact := range manifest.Artifacts {
		if artifact.Type != "Linux Package" {
			continue
		}
		path, err := artifactDiskPath(manifest, artifact)
		if err != nil {
			return err
		}
		if artifact.Extra.Format == "deb" {
			if err := validateDebPackage(dpkgDeb, path, artifact.GOARCH); err != nil {
				return fmt.Errorf("validate %q: %w", artifact.Name, err)
			}
			continue
		}
		if err := validateRPMPackage(rpm, rpmDB, path, artifact.GOARCH); err != nil {
			return fmt.Errorf("validate %q: %w", artifact.Name, err)
		}
	}
	return nil
}

func validateDebPackage(dpkgDeb string, path string, goarch string) error {
	metadata, err := runInspectionCommand(dpkgDeb, "--show", "--showformat", "${Package}\n${Architecture}\n", path)
	if err != nil {
		return err
	}
	fields := strings.Fields(metadata)
	if len(fields) != 2 || fields[0] != "aht" || fields[1] != goarch {
		return fmt.Errorf("package metadata = %q, want aht %s", metadata, goarch)
	}
	contents, err := runInspectionCommand(dpkgDeb, "--contents", path)
	if err != nil {
		return err
	}
	if !packageContainsPath(contents, "usr/bin/aht") {
		return fmt.Errorf("package does not contain /usr/bin/aht")
	}
	return nil
}

func validateRPMPackage(rpm string, rpmDB string, path string, goarch string) error {
	metadata, err := runInspectionCommand(rpm, "--dbpath", rpmDB, "-qp", "--queryformat", "%{NAME}\n%{ARCH}\n", path)
	if err != nil {
		return err
	}
	fields := strings.Fields(metadata)
	expectedArchitecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if len(fields) != 2 || fields[0] != "aht" || fields[1] != expectedArchitecture {
		return fmt.Errorf("RPM metadata = %q, want aht %s", metadata, expectedArchitecture)
	}
	contents, err := runInspectionCommand(rpm, "--dbpath", rpmDB, "-qlp", path)
	if err != nil {
		return err
	}
	if !packageContainsPath(contents, "usr/bin/aht") {
		return fmt.Errorf("package does not contain /usr/bin/aht")
	}
	return nil
}

func packageContainsPath(output string, wanted string) bool {
	for line := range strings.Lines(output) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		path := strings.TrimPrefix(fields[len(fields)-1], "./")
		path = strings.TrimPrefix(path, "/")
		if path == wanted {
			return true
		}
	}
	return false
}

func runInspectionCommand(name string, args ...string) (string, error) {
	command := exec.Command(name, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("run %q %q: %w; output=%q", name, args, err, output)
	}
	return string(output), nil
}

func validatePublishedAssets(manifest releaseManifest, publishedDir string) error {
	if !filepath.IsAbs(publishedDir) {
		publishedDir = filepath.Join(filepath.Dir(manifest.Dir), publishedDir)
	}
	expected := make(map[string]releaseArtifact)
	for _, artifact := range manifest.Artifacts {
		if artifact.Type == "Archive" || artifact.Type == "Linux Package" || artifact.Type == "Checksum" {
			expected[artifact.Name] = artifact
		}
	}

	entries, err := os.ReadDir(publishedDir)
	if err != nil {
		return fmt.Errorf("read published artifact directory: %w", err)
	}
	actualNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return fmt.Errorf("published asset %q is not a regular file", entry.Name())
		}
		actualNames[entry.Name()] = struct{}{}
	}
	expectedNames := make(map[string]struct{}, len(expected))
	for name := range expected {
		expectedNames[name] = struct{}{}
	}
	if err := requireExactSet("published assets", actualNames, expectedNames); err != nil {
		return err
	}

	for name, artifact := range expected {
		distPath, err := artifactDiskPath(manifest, artifact)
		if err != nil {
			return err
		}
		equal, err := filesEqual(distPath, filepath.Join(publishedDir, name))
		if err != nil {
			return fmt.Errorf("compare published asset %q: %w", name, err)
		}
		if !equal {
			return fmt.Errorf("published asset %q differs from tested dist copy", name)
		}
	}
	return nil
}

func filesEqual(leftPath string, rightPath string) (bool, error) {
	left, err := os.Open(leftPath)
	if err != nil {
		return false, err
	}
	defer left.Close()
	right, err := os.Open(rightPath)
	if err != nil {
		return false, err
	}
	defer right.Close()

	leftBuffer := make([]byte, 32*1024)
	rightBuffer := make([]byte, len(leftBuffer))
	for {
		leftCount, leftErr := io.ReadFull(left, leftBuffer)
		rightCount, rightErr := io.ReadFull(right, rightBuffer)
		if leftCount != rightCount || !bytes.Equal(leftBuffer[:leftCount], rightBuffer[:rightCount]) {
			return false, nil
		}
		if errors.Is(leftErr, io.EOF) && errors.Is(rightErr, io.EOF) {
			return true, nil
		}
		if errors.Is(leftErr, io.ErrUnexpectedEOF) && errors.Is(rightErr, io.ErrUnexpectedEOF) {
			return true, nil
		}
		if leftErr != nil && !errors.Is(leftErr, io.ErrUnexpectedEOF) {
			return false, leftErr
		}
		if rightErr != nil && !errors.Is(rightErr, io.ErrUnexpectedEOF) {
			return false, rightErr
		}
	}
}

func findArtifact(manifest releaseManifest, artifactType string, goos string, goarch string, format string) (releaseArtifact, error) {
	matches := make([]releaseArtifact, 0, 1)
	for _, artifact := range manifest.Artifacts {
		if artifact.Type != artifactType || artifact.GOOS != goos || artifact.GOARCH != goarch || artifact.Extra.Format != format {
			continue
		}
		matches = append(matches, artifact)
	}
	if len(matches) != 1 {
		return releaseArtifact{}, fmt.Errorf("found %d %s artifacts for %s/%s format %q, want 1", len(matches), artifactType, goos, goarch, format)
	}
	return matches[0], nil
}
