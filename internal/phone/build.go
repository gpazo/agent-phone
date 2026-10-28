package phone

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type AppBuild struct {
	Path     string `json:"path"`
	BundleID string `json:"bundleId"`
}
type BuildResult struct {
	Project       string     `json:"project"`
	Scheme        string     `json:"scheme"`
	Configuration string     `json:"configuration"`
	Revision      string     `json:"revision,omitempty"`
	Log           string     `json:"log"`
	Apps          []AppBuild `json:"apps"`
	ElapsedMS     int64      `json:"elapsedMs"`
}

func buildKey(a Params) string {
	b, _ := json.Marshal(struct {
		Project, Scheme, Configuration, DerivedData string
		Settings                                    map[string]string
	}{a.Project, a.Scheme, a.Configuration, a.DerivedData, a.BuildSettings})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

func canonicalPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	ancestor := absolute
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			resolved, sensitive, err := canonicalExistingPath(resolved)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				component := missing[i]
				if !sensitive {
					for _, r := range component {
						if r > 127 {
							return "", fmt.Errorf("create non-ASCII output directory before use on a case-insensitive volume: %q", absolute)
						}
					}
					component = strings.ToLower(component)
				}
				resolved = filepath.Join(resolved, component)
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, statErr := os.Lstat(ancestor); statErr == nil {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", err
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
}
func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator)) || a == string(filepath.Separator) || b == string(filepath.Separator)
}
func (s *Service) normalizeBuild(a Params) (Params, error) {
	if a.Project == "" || a.Scheme == "" {
		return a, fmt.Errorf("project and scheme are required")
	}
	project, err := canonicalPath(a.Project)
	if err != nil {
		return a, err
	}
	info, err := os.Stat(project)
	if err != nil {
		return a, err
	}
	if !info.IsDir() || (filepath.Ext(project) != ".xcodeproj" && filepath.Ext(project) != ".xcworkspace") {
		return a, fmt.Errorf("project must be an .xcodeproj or .xcworkspace directory")
	}
	a.Project = project
	if a.Configuration == "" {
		a.Configuration = "Debug"
	}
	if a.Configuration == "." || a.Configuration == ".." || strings.ContainsAny(a.Configuration, "/\\") {
		return a, fmt.Errorf("configuration must be a single directory name")
	}
	for key := range a.BuildSettings {
		switch key {
		case "SYMROOT", "OBJROOT", "DSTROOT", "BUILD_DIR", "BUILD_ROOT", "CONFIGURATION_BUILD_DIR", "CONFIGURATION_TEMP_DIR", "PROJECT_TEMP_DIR", "TARGET_TEMP_DIR", "DERIVED_FILE_DIR", "DERIVED_FILES_DIR", "DERIVED_SOURCES_DIR", "SHARED_PRECOMPS_DIR", "TARGET_BUILD_DIR", "BUILT_PRODUCTS_DIR", "INSTALL_PATH", "DEPLOYMENT_LOCATION", "PROJECT_TEMP_ROOT", "TEMP_DIR", "TEMP_ROOT", "MODULE_CACHE_DIR", "CLANG_MODULE_CACHE_PATH", "SDK_STAT_CACHE_DIR", "INDEX_DATA_STORE_DIR", "DWARF_DSYM_FOLDER_PATH", "WRAPPER_NAME", "FULL_PRODUCT_NAME", "CONTENTS_FOLDER_PATH", "EXECUTABLE_FOLDER_PATH", "UNLOCALIZED_RESOURCES_FOLDER_PATH":
			return a, fmt.Errorf("unsupported output-redirecting build setting %q", key)
		}
		if key == "" || strings.ContainsAny(key, "= \t\n-") || strings.HasPrefix(key, "CODE_SIGN") || key == "SDKROOT" || key == "ARCHS" {
			return a, fmt.Errorf("unsupported build setting %q", key)
		}
	}
	if len(a.BuildSettings) == 0 {
		a.BuildSettings = nil
	}
	if a.DerivedData == "" {
		a.DerivedData = filepath.Join(s.data, "builds", buildKey(a))
	} else if !filepath.IsAbs(a.DerivedData) {
		return a, fmt.Errorf("derivedData must be absolute")
	}
	a.DerivedData, err = canonicalPath(a.DerivedData)
	if err != nil {
		return a, err
	}
	if info, err := os.Stat(a.DerivedData); err == nil && !info.IsDir() {
		return a, fmt.Errorf("derivedData must be a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return a, err
	}
	artifacts, err := canonicalPath(filepath.Join(s.data, "build-artifacts"))
	if err != nil {
		return a, err
	}
	if pathsOverlap(a.DerivedData, artifacts) {
		return a, fmt.Errorf("derivedData overlaps immutable build artifacts")
	}
	return a, nil
}
func validateBuildClaims(specs []Params) error {
	for i, a := range specs {
		for _, b := range specs[:i] {
			if buildKey(a) != buildKey(b) && pathsOverlap(a.DerivedData, b.DerivedData) {
				return fmt.Errorf("different build specs claim overlapping derivedData directories %q and %q", a.DerivedData, b.DerivedData)
			}
		}
	}
	return nil
}

func acquireBuildLock(ctx context.Context) (*os.File, error) {
	lock, err := os.OpenFile(filepath.Join("/tmp", fmt.Sprintf("agent-phone-xcodebuild-%d.lock", os.Getuid())), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	for {
		if err = ctx.Err(); err != nil {
			lock.Close()
			return nil, err
		}
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			lock.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			lock.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (s *Service) build(ctx context.Context, a Params) (any, error) {
	a, err := s.normalizeBuild(a)
	if err != nil {
		return nil, err
	}
	lock, err := acquireBuildLock(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Close()
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	started := time.Now()
	artifactDir := filepath.Join(s.data, "build-artifacts", randomID())
	if err = mkdir(artifactDir); err != nil {
		return nil, err
	}
	logPath := filepath.Join(artifactDir, "build.log")
	result := BuildResult{Project: a.Project, Scheme: a.Scheme, Configuration: a.Configuration, Log: logPath, Apps: []AppBuild{}}
	log, err := os.Create(logPath)
	if err != nil {
		return result, err
	}
	defer log.Close()
	if err = mkdir(a.DerivedData); err != nil {
		return result, err
	}
	kind := "-project"
	if filepath.Ext(a.Project) == ".xcworkspace" {
		kind = "-workspace"
	}
	// Simulator ad-hoc signing preserves app-group entitlements without an
	// Apple Developer account. Unsigned bundles can build but crash at startup.
	args := []string{kind, a.Project, "-scheme", a.Scheme, "-configuration", a.Configuration, "-sdk", "iphonesimulator", "-destination", "generic/platform=iOS Simulator", "-derivedDataPath", a.DerivedData, "-jobs", "2", "ARCHS=arm64", "ONLY_ACTIVE_ARCH=YES", "CODE_SIGNING_ALLOWED=YES", "CODE_SIGN_IDENTITY=-", "CODE_SIGNING_REQUIRED=NO"}
	keys := make([]string, 0, len(a.BuildSettings))
	for key := range a.BuildSettings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, key+"="+a.BuildSettings[key])
	}
	args = append(args, "build")
	command := exec.CommandContext(ctx, "/usr/bin/xcodebuild", args...)
	command.Dir = filepath.Dir(a.Project)
	command.Stdout = log
	command.Stderr = log
	s.event("", "build_start", map[string]any{"project": a.Project, "scheme": a.Scheme, "log": logPath})
	if s.buildCommand != nil {
		err = s.buildCommand(command)
	} else {
		err = command.Run()
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	if err != nil {
		return result, fmt.Errorf("Xcode build failed: %w; log: %s", err, logPath)
	}
	if err = log.Close(); err != nil {
		return result, err
	}
	if rev, err := s.command(ctx, "/usr/bin/git", "-C", filepath.Dir(a.Project), "rev-parse", "HEAD"); err == nil {
		result.Revision = strings.TrimSpace(string(rev))
	}
	products := filepath.Join(a.DerivedData, "Build", "Products", a.Configuration+"-iphonesimulator")
	entries, err := os.ReadDir(products)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || filepath.Ext(entry.Name()) != ".app" {
			continue
		}
		app := filepath.Join(products, entry.Name())
		bundle, err := s.command(ctx, "/usr/bin/plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", filepath.Join(app, "Info.plist"))
		if err != nil {
			continue
		}
		snapshot := filepath.Join(artifactDir, filepath.Base(app))
		if _, err = run(ctx, "/bin/cp", "-cR", app, snapshot); err != nil {
			if err = ctx.Err(); err != nil {
				return result, err
			}
			if err = os.RemoveAll(snapshot); err != nil {
				return result, err
			}
			if _, err = run(ctx, "/bin/cp", "-R", app, snapshot); err != nil {
				return result, fmt.Errorf("snapshot app: %w", err)
			}
		}
		result.Apps = append(result.Apps, AppBuild{snapshot, strings.TrimSpace(string(bundle))})
	}
	if len(result.Apps) == 0 {
		return result, fmt.Errorf("build succeeded but no simulator app bundle found; inspect %s", logPath)
	}
	result.ElapsedMS = time.Since(started).Milliseconds()
	if err = atomicJSON(filepath.Join(artifactDir, "build.json"), result); err != nil {
		return result, err
	}
	s.event("", "build_complete", result)
	return result, nil
}
