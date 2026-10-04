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
	maxNativeBinaryBytes    = 256 << 20
)

type releaseArtifactExtra struct {
	Format string `json:"Format"` //nolint:tagliatelle // GoReleaser writes artifacts.json extra fields with Go field names.
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

type releaseInventory struct {
	archives      map[string]struct{}
	packages      map[string]struct{}
	downloadNames map[string]struct{}
	checksumCount int
}

func TestReleaseArtifacts(t *testing.T) {
	artifactDir := os.Getenv(artifactDirEnv)
	if artifactDir == "" {
		t.Skipf("%s is not set", artifactDirEnv)
	}

	manifest := loadArtifactManifest(t, artifactDir)
	validateArtifactInventory(t, manifest)
	validateChecksums(t, manifest)
	if runtime.GOOS == "linux" {
		validateLinuxPackages(t, manifest)
	}
	validateNativeArchive(t, manifest, runtime.GOOS, runtime.GOARCH)

	if publishedDir := os.Getenv(publishedArtifactDirEnv); publishedDir != "" {
		validatePublishedAssets(t, manifest, publishedDir)
	}
}

func loadArtifactManifest(t *testing.T, dir string) releaseManifest {
	t.Helper()
	manifest := releaseManifest{Dir: resolveArtifactDir(t, dir)}
	decodeJSONFile(t, filepath.Join(manifest.Dir, "artifacts.json"), &manifest.Artifacts)
	decodeJSONFile(t, filepath.Join(manifest.Dir, "metadata.json"), &manifest.Metadata)
	if manifest.Metadata.ProjectName != "aht" || manifest.Metadata.Version == "" || manifest.Metadata.Commit == "" || manifest.Metadata.Date == "" {
		t.Fatalf("metadata does not identify a complete aht build: %+v", manifest.Metadata)
	}
	return manifest
}

func resolveArtifactDir(t *testing.T, dir string) string {
	t.Helper()
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("resolve artifact directory: %v", err)
	}
	for candidateRoot := workingDir; ; candidateRoot = filepath.Dir(candidateRoot) {
		candidate := filepath.Join(candidateRoot, dir)
		if hasArtifactManifest(candidate) {
			return filepath.Clean(candidate)
		}
		if filepath.Dir(candidateRoot) == candidateRoot {
			break
		}
	}
	return filepath.Clean(filepath.Join(workingDir, dir))
}

func hasArtifactManifest(dir string) bool {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return false
	}
	_, statErr := root.Stat("artifacts.json")
	closeErr := root.Close()
	return statErr == nil && closeErr == nil
}

func decodeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(value); err != nil {
		t.Fatalf("decode %s: %v", filepath.Base(path), err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		t.Fatalf("decode %s: trailing JSON content", filepath.Base(path))
	}
}

func validateArtifactInventory(t *testing.T, manifest releaseManifest) {
	t.Helper()
	inventory := releaseInventory{
		archives:      make(map[string]struct{}),
		packages:      make(map[string]struct{}),
		downloadNames: make(map[string]struct{}),
	}
	for _, artifact := range manifest.Artifacts {
		if isDownloadableArtifact(artifact) {
			artifactDiskPath(t, manifest, artifact)
			inventory.add(t, artifact)
		}
	}

	requireExactSet(t, "archive targets", inventory.archives, setOf("darwin/amd64", "darwin/arm64", "linux/amd64", "linux/arm64"))
	requireExactSet(t, "package targets", inventory.packages, setOf("amd64/deb", "amd64/rpm", "arm64/deb", "arm64/rpm"))
	if inventory.checksumCount != 1 {
		t.Fatalf("found %d checksum artifacts, want 1", inventory.checksumCount)
	}
}

func (inventory *releaseInventory) add(t *testing.T, artifact releaseArtifact) {
	t.Helper()
	if _, exists := inventory.downloadNames[artifact.Name]; exists {
		t.Fatalf("duplicate downloadable artifact name %q", artifact.Name)
	}
	inventory.downloadNames[artifact.Name] = struct{}{}

	switch artifact.Type {
	case "Archive":
		if artifact.Extra.Format != "tar.gz" {
			t.Fatalf("archive %q uses format %q, want tar.gz", artifact.Name, artifact.Extra.Format)
		}
		addUniqueTarget(t, inventory.archives, artifact.GOOS+"/"+artifact.GOARCH, "archive")
	case "Linux Package":
		if artifact.GOOS != "linux" || (artifact.Extra.Format != "deb" && artifact.Extra.Format != "rpm") {
			t.Fatalf("package %q has unsupported target %s/%s format %q", artifact.Name, artifact.GOOS, artifact.GOARCH, artifact.Extra.Format)
		}
		addUniqueTarget(t, inventory.packages, artifact.GOARCH+"/"+artifact.Extra.Format, "package")
	case "Checksum":
		if artifact.Name != "checksums.txt" {
			t.Fatalf("checksum artifact is named %q, want checksums.txt", artifact.Name)
		}
		inventory.checksumCount++
	}
}

func isDownloadableArtifact(artifact releaseArtifact) bool {
	return artifact.Type == "Archive" || artifact.Type == "Linux Package" || artifact.Type == "Checksum"
}

func artifactDiskPath(t *testing.T, manifest releaseManifest, artifact releaseArtifact) string {
	t.Helper()
	if artifact.Name == "" || artifact.Name != filepath.Base(artifact.Name) {
		t.Fatalf("artifact name %q is not a basename", artifact.Name)
	}
	path := artifact.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Dir(manifest.Dir), path)
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) != manifest.Dir || filepath.Base(path) != artifact.Name {
		t.Fatalf("artifact %q is not a top-level file in %q", artifact.Name, manifest.Dir)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat artifact %q: %v", artifact.Name, err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("artifact %q is not a regular file", artifact.Name)
	}
	return path
}

func addUniqueTarget(t *testing.T, targets map[string]struct{}, key string, kind string) {
	t.Helper()
	if _, exists := targets[key]; exists {
		t.Fatalf("duplicate %s target %q", kind, key)
	}
	targets[key] = struct{}{}
}

func setOf(values ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func requireExactSet(t *testing.T, kind string, actual map[string]struct{}, expected map[string]struct{}) {
	t.Helper()
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
		t.Fatalf("%s mismatch: missing=%v extra=%v", kind, missing, extra)
	}
}

func validateChecksums(t *testing.T, manifest releaseManifest) {
	t.Helper()
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

	checksums := parseChecksums(t, artifactDiskPath(t, manifest, checksumArtifact))
	expectedNames := make(map[string]struct{}, len(expected))
	actualNames := make(map[string]struct{}, len(checksums))
	for name := range expected {
		expectedNames[name] = struct{}{}
	}
	for name := range checksums {
		actualNames[name] = struct{}{}
	}
	requireExactSet(t, "checksum inventory", actualNames, expectedNames)

	for name, artifact := range expected {
		if hex.EncodeToString(fileSHA256(t, manifest.Dir, filepath.Base(artifactDiskPath(t, manifest, artifact)))) != checksums[name] {
			t.Fatalf("SHA-256 mismatch for %q", name)
		}
	}
}

func parseChecksums(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}

	checksums := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			t.Fatalf("invalid checksums.txt line %q", scanner.Text())
		}
		name := fields[1]
		if name != filepath.Base(name) || filepath.IsAbs(name) {
			t.Fatalf("checksum name %q is not a basename", name)
		}
		if _, exists := checksums[name]; exists {
			t.Fatalf("duplicate checksum entry for %q", name)
		}
		digest := strings.ToLower(fields[0])
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			t.Fatalf("invalid SHA-256 digest for %q", name)
		}
		checksums[name] = digest
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read checksums.txt: %v", err)
	}
	return checksums
}

func fileSHA256(t *testing.T, dir string, name string) []byte {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("open %q: %v", dir, err)
	}
	defer closeOrFail(t, root)
	file, err := root.Open(name)
	if err != nil {
		t.Fatalf("open %q in %q: %v", name, dir, err)
	}
	defer closeOrFail(t, file)

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatalf("hash %q: %v", name, err)
	}
	return hash.Sum(nil)
}

func closeOrFail(t *testing.T, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func validateNativeArchive(t *testing.T, manifest releaseManifest, goos string, goarch string) {
	t.Helper()
	artifact := findArtifact(t, manifest, "Archive", goos, goarch, "tar.gz")
	binaryPath := filepath.Join(t.TempDir(), "aht")
	extractNativeArchive(t, artifactDiskPath(t, manifest, artifact), binaryPath)
	verifyReleaseVersion(t, binaryPath, manifest.Metadata)
	verifyReleaseTracking(t, binaryPath)
}

func extractNativeArchive(t *testing.T, archivePath string, binaryPath string) {
	t.Helper()
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("open native archive: %v", err)
	}
	defer closeOrFail(t, archiveFile)
	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		t.Fatalf("open native archive gzip stream: %v", err)
	}
	defer closeOrFail(t, gzipReader)

	required := map[string]bool{"aht": false, "LICENSE": false, "README.md": false}
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read native archive: %v", err)
		}
		found, requiredMember := required[header.Name]
		if !requiredMember {
			continue
		}
		if found {
			t.Fatalf("native archive contains duplicate %q", header.Name)
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("native archive member %q is not a regular file", header.Name)
		}
		required[header.Name] = true
		if header.Name == "aht" {
			extractNativeBinary(t, header, tarReader, binaryPath)
		}
	}

	if missing := missingMembers(required); len(missing) != 0 {
		t.Fatalf("native archive is missing public files: %v", missing)
	}
}

func missingMembers(found map[string]bool) []string {
	missing := make([]string, 0)
	for name, present := range found {
		if !present {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func extractNativeBinary(t *testing.T, header *tar.Header, member io.Reader, binaryPath string) {
	t.Helper()
	if header.FileInfo().Mode().Perm()&0o111 == 0 {
		t.Fatal("native archive binary is not executable")
	}
	binaryFile, err := os.OpenFile(binaryPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatalf("create extracted binary: %v", err)
	}
	written, copyErr := io.CopyN(binaryFile, member, maxNativeBinaryBytes+1)
	closeErr := binaryFile.Close()
	if written > maxNativeBinaryBytes {
		t.Fatalf("native binary exceeds %d bytes", maxNativeBinaryBytes)
	}
	if copyErr != nil && !errors.Is(copyErr, io.EOF) {
		t.Fatalf("extract native binary: %v", copyErr)
	}
	if closeErr != nil {
		t.Fatalf("close extracted binary: %v", closeErr)
	}
}

func isolatedReleaseEnvironment(t *testing.T) []string {
	t.Helper()
	isolatedRoot := t.TempDir()
	home := filepath.Join(isolatedRoot, "home")
	configHome := filepath.Join(isolatedRoot, "config")
	stateDir := filepath.Join(isolatedRoot, "state")
	for _, dir := range []string{home, configHome, stateDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("create isolated directory %q: %v", dir, err)
		}
	}
	return systemTestEnvironment(home, configHome, stateDir)
}

func verifyReleaseVersion(t *testing.T, binary string, metadata releaseMetadata) {
	t.Helper()
	versionOutput := runReleaseCommand(t, binary, isolatedReleaseEnvironment(t), "--json", "--version")
	var version releaseVersion
	if err := json.Unmarshal(versionOutput, &version); err != nil {
		t.Fatalf("decode version output %q: %v", versionOutput, err)
	}
	if version.Version != metadata.Version {
		t.Fatalf("version mismatch: binary=%q metadata=%q", version.Version, metadata.Version)
	}
	if version.Commit == "" || !strings.HasPrefix(metadata.Commit, version.Commit) {
		t.Fatalf("commit mismatch: binary=%q metadata=%q", version.Commit, metadata.Commit)
	}
	if !releaseDatesEqual(version.Built, metadata.Date) {
		t.Fatalf("build date mismatch: binary=%q metadata=%q", version.Built, metadata.Date)
	}
}

func verifyReleaseTracking(t *testing.T, binary string) {
	t.Helper()
	environment := isolatedReleaseEnvironment(t)
	storePath := filepath.Join(t.TempDir(), "state.json")
	reportOutput := runReleaseCommand(t, binary, environment, "--store", storePath, "--json", "report", "codex", "--session-id", "release-verification", "--event", "start", "--no-tmux")
	var reported releaseSession
	if err := json.Unmarshal(reportOutput, &reported); err != nil {
		t.Fatalf("decode report output %q: %v", reportOutput, err)
	}
	if reported.SchemaVersion != 3 || reported.SessionID != "release-verification" || reported.Harness != "codex" {
		t.Fatalf("report output does not contain the schema-v3 Codex session: %q", reportOutput)
	}

	listOutput := runReleaseCommand(t, binary, environment, "--store", storePath, "--json", "list")
	var sessions []releaseSession
	if err := json.Unmarshal(listOutput, &sessions); err != nil {
		t.Fatalf("decode list output %q: %v", listOutput, err)
	}
	if len(sessions) != 1 || sessions[0].SessionID != reported.SessionID || sessions[0].Harness != reported.Harness {
		t.Fatalf("list output does not contain exactly the reported session: %q", listOutput)
	}
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

func runReleaseCommand(t *testing.T, binary string, environment []string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(t.Context(), binary, args...)
	command.Dir = t.TempDir()
	command.Env = environment
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run %q %q: %v; stdout=%q stderr=%q", binary, args, err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("run %q %q wrote stderr=%q", binary, args, stderr.String())
	}
	return stdout.Bytes()
}

func validateLinuxPackages(t *testing.T, manifest releaseManifest) {
	t.Helper()
	dpkgDeb, err := exec.LookPath("dpkg-deb")
	if err != nil {
		t.Fatalf("dpkg-deb is required to inspect release .deb packages; install dpkg: %v", err)
	}
	rpm, err := exec.LookPath("rpm")
	if err != nil {
		t.Fatalf("rpm is required to inspect release .rpm packages; install rpm: %v", err)
	}
	rpmDB := t.TempDir()

	for _, artifact := range manifest.Artifacts {
		if artifact.Type != "Linux Package" {
			continue
		}
		path := artifactDiskPath(t, manifest, artifact)
		if artifact.Extra.Format == "deb" {
			validateDebPackage(t, dpkgDeb, path, artifact.GOARCH)
			continue
		}
		validateRPMPackage(t, rpm, rpmDB, path, artifact.GOARCH)
	}
}

func validateDebPackage(t *testing.T, dpkgDeb string, path string, goarch string) {
	t.Helper()
	metadata := runInspectionCommand(t, dpkgDeb, "--show", "--showformat", "${Package}\n${Architecture}\n", path)
	fields := strings.Fields(metadata)
	if len(fields) != 2 || fields[0] != "aht" || fields[1] != goarch {
		t.Fatalf("%s: package metadata = %q, want aht %s", filepath.Base(path), metadata, goarch)
	}
	if !packageContainsPath(runInspectionCommand(t, dpkgDeb, "--contents", path), "usr/bin/aht") {
		t.Fatalf("%s: package does not contain /usr/bin/aht", filepath.Base(path))
	}
}

func validateRPMPackage(t *testing.T, rpm string, rpmDB string, path string, goarch string) {
	t.Helper()
	metadata := runInspectionCommand(t, rpm, "--dbpath", rpmDB, "-qp", "--queryformat", "%{NAME}\n%{ARCH}\n", path)
	fields := strings.Fields(metadata)
	expectedArchitecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	if len(fields) != 2 || fields[0] != "aht" || fields[1] != expectedArchitecture {
		t.Fatalf("%s: RPM metadata = %q, want aht %s", filepath.Base(path), metadata, expectedArchitecture)
	}
	if !packageContainsPath(runInspectionCommand(t, rpm, "--dbpath", rpmDB, "-qlp", path), "usr/bin/aht") {
		t.Fatalf("%s: package does not contain /usr/bin/aht", filepath.Base(path))
	}
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

func runInspectionCommand(t *testing.T, name string, args ...string) string {
	t.Helper()
	output, err := exec.CommandContext(t.Context(), name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("run %q %q: %v; output=%q", name, args, err, output)
	}
	return string(output)
}

func validatePublishedAssets(t *testing.T, manifest releaseManifest, publishedDir string) {
	t.Helper()
	if !filepath.IsAbs(publishedDir) {
		publishedDir = filepath.Join(filepath.Dir(manifest.Dir), publishedDir)
	}
	expected := make(map[string]releaseArtifact)
	for _, artifact := range manifest.Artifacts {
		if isDownloadableArtifact(artifact) {
			expected[artifact.Name] = artifact
		}
	}

	entries, err := os.ReadDir(publishedDir)
	if err != nil {
		t.Fatalf("read published artifact directory: %v", err)
	}
	actualNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			t.Fatalf("published asset %q is not a regular file", entry.Name())
		}
		actualNames[entry.Name()] = struct{}{}
	}
	expectedNames := make(map[string]struct{}, len(expected))
	for name := range expected {
		expectedNames[name] = struct{}{}
	}
	requireExactSet(t, "published assets", actualNames, expectedNames)

	for name, artifact := range expected {
		distDigest := fileSHA256(t, manifest.Dir, filepath.Base(artifactDiskPath(t, manifest, artifact)))
		if !bytes.Equal(distDigest, fileSHA256(t, publishedDir, name)) {
			t.Fatalf("published asset %q differs from tested dist copy", name)
		}
	}
}

func findArtifact(t *testing.T, manifest releaseManifest, artifactType string, goos string, goarch string, format string) releaseArtifact {
	t.Helper()
	matches := make([]releaseArtifact, 0, 1)
	for _, artifact := range manifest.Artifacts {
		if artifact.Type == artifactType && artifact.GOOS == goos && artifact.GOARCH == goarch && artifact.Extra.Format == format {
			matches = append(matches, artifact)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("found %d %s artifacts for %s/%s format %q, want 1", len(matches), artifactType, goos, goarch, format)
	}
	return matches[0]
}
