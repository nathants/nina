// Integration test helpers: builds nina once, creates temp repos, runs binary
// in debug mode with output to files, and ensures all processes are cleaned up.
package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

// BuildOnce caches a single nina binary build for all tests.
var BuildOnce sync.Once
var NinaBin string

// ProcessCleanup tracks processes by UUID for cleanup
var ProcessCleanup struct {
	sync.Mutex
	uuids map[string]struct{}
}

func init() {
	ProcessCleanup.uuids = make(map[string]struct{})

	// Set up signal handlers for cleanup
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		// defer func() {}()
		<-sigChan
		cleanupAllProcesses()
		os.Exit(1)
	}()
}

// cleanupAllProcesses kills all processes with our --uuid flag
func cleanupAllProcesses() {
	ProcessCleanup.Lock()
	defer ProcessCleanup.Unlock()

	for uid := range ProcessCleanup.uuids {
		// Find all processes with this UUID using ps -ef to see full command
		cmd := exec.Command("bash", "-c", fmt.Sprintf(`ps -ef | grep "nina run " | grep -- "--uuid %s" | grep -v grep | awk '{print $2}'`, uid))
		output, _ := cmd.Output()
		pids := strings.TrimSpace(string(output))
		if pids != "" {
			for pid := range strings.SplitSeq(pids, "\n") {
				if pid != "" {
					// Kill the process
					_ = exec.Command("kill", "-9", pid).Run()
				}
			}
		}
	}
	ProcessCleanup.uuids = make(map[string]struct{})
}

// registerUUID adds a UUID for tracking
func registerUUID(uid string) {
	ProcessCleanup.Lock()
	defer ProcessCleanup.Unlock()
	ProcessCleanup.uuids[uid] = struct{}{}
}

// unregisterUUID removes a UUID from tracking
func unregisterUUID(uid string) {
	ProcessCleanup.Lock()
	defer ProcessCleanup.Unlock()
	delete(ProcessCleanup.uuids, uid)
}

// CreateTempRepo writes the provided file map into a temporary directory
// and returns the directory path.
func CreateTempRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "nina_loop_test_repo_")
	if err != nil {
	    panic(err)
	}

	fmt.Printf("\n[SETUP] Creating temp repo at: %s\n", dir)
	fmt.Printf("Initial files (%d):\n", len(files))

	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		// Show file info and first line of content
		lines := strings.Split(content, "\n")
		preview := lines[0]
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Printf("  - %s (%d bytes): %s\n", rel, len(content), preview)
	}

	return dir
}

// buildError holds any error from the build process
var buildError error
var buildMutex sync.Mutex

// BuildNinaLoop builds nina once to /tmp/nina.test.
func BuildNinaLoop(t *testing.T) {
	t.Helper()

	// Synchronize build process
	buildMutex.Lock()
	defer buildMutex.Unlock()

	// Check if already built successfully
	if NinaBin != "" && buildError == nil {
		return
	}

	// Check if previous build failed
	if buildError != nil {
		t.Fatalf("previous build failed: %v", buildError)
	}

	BuildOnce.Do(func() {
		out := "/tmp/nina.test"
		// Get project root from the location of this source file
		_, filename, _, ok := runtime.Caller(0)
		if !ok {
			buildError = fmt.Errorf("failed to get runtime caller info")
			return
		}
		// This file is at integration/helpers.go, so parent dir is project root
		projectRoot := filepath.Dir(filepath.Dir(filename))

		cmd := exec.Command("go", "build", "-o", out, "./cmd/nina")
		cmd.Dir = projectRoot
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			buildError = fmt.Errorf("build nina: %v (stderr: %s)", err, stderr.String())
			return
		}
		NinaBin = out
	})

	// Report any build error
	if buildError != nil {
		t.Fatalf("build failed: %v", buildError)
	}
}

// RunNinaLoop executes nina run in debug mode with output to files.
func RunNinaLoop(t *testing.T, repo string, prompt string, model string) error {
	t.Helper()
	BuildNinaLoop(t)

	// Generate UUID for this test run
	testUUID := uuid.New().String()
	registerUUID(testUUID)
	defer unregisterUUID(testUUID)

	// Create output files with simpler names (t.Name() contains slashes which cause issues)
	testName := strings.ReplaceAll(t.Name(), "/", "_")
	outputDir, err := os.MkdirTemp("/tmp", "nina_loop_test_")
	if err != nil {
	    panic(err)
	}
	stdoutFile := filepath.Join(outputDir, fmt.Sprintf("%s.stdout.log", testName))
	stderrFile := filepath.Join(outputDir, fmt.Sprintf("%s.stderr.log", testName))
	fmt.Printf("\n>>> STARTING NINA: %s <<<\n", t.Name())
	fmt.Printf("Model: %s", model)
	fmt.Printf("\n")
	fmt.Printf("Working directory: %s\n", repo)
	fmt.Printf("Timeout: 120s\n")
	fmt.Printf("Prompt length: %d bytes\n", len(prompt))
	fmt.Printf("Process UUID: %s\n", testUUID)
	fmt.Printf("Output files:\n")
	fmt.Printf("  stdout: %s\n", stdoutFile)
	fmt.Printf("  stderr: %s\n", stderrFile)
	fmt.Printf("  (use 'tail -f' to monitor)\n")
	fmt.Printf("---\n")

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		// defer func() {}()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				elapsed := int(time.Since(start).Seconds())
				remaining := 120 - elapsed
				fmt.Printf("[PROGRESS] Test '%s' running for %ds (timeout in %ds)\n", t.Name(), elapsed, remaining)
			case <-done:
				return
			}
		}
	}()

	// Always run in debug mode
	cmd := exec.CommandContext(ctx, NinaBin, "run", "-m", model, "-d", "--uuid", testUUID)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(prompt)

	// Create output files
	stdout, err := os.Create(stdoutFile)
	if err != nil {
		return fmt.Errorf("create stdout file: %v", err)
	}
	defer func() { _ = stdout.Close() }()

	stderr, err := os.Create(stderrFile)
	if err != nil {
		return fmt.Errorf("create stderr file: %v", err)
	}
	defer func() { _ = stderr.Close() }()

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("start command: %v", err)
	}

	// Clean up process on timeout or exit
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// Kill any child processes with our UUID
		killCmd := exec.Command("bash", "-c", fmt.Sprintf(`pkill -9 -f -- "--uuid %s"`, testUUID))
		_ = killCmd.Run()
	}()

	err = cmd.Wait()
	close(done)
	elapsed := time.Since(start)

	// Read and display summary of output
	stdoutContent, _ := os.ReadFile(stdoutFile)
	stderrContent, _ := os.ReadFile(stderrFile)
	stdoutLines := strings.Count(string(stdoutContent), "\n")
	stderrLines := strings.Count(string(stderrContent), "\n")

	fmt.Printf("\n[OUTPUT SUMMARY] stdout: %d lines, stderr: %d lines\n", stdoutLines, stderrLines)

	if ctx.Err() == context.DeadlineExceeded {
		fmt.Printf("\n[TIMEOUT] Nina exceeded 120s timeout after %v\n", elapsed)
		fmt.Printf("Check output files for details:\n")
		fmt.Printf("  cat %s\n", stdoutFile)
		fmt.Printf("  cat %s\n", stderrFile)
		return context.DeadlineExceeded
	}

	if err != nil {
		fmt.Printf("\n[ERROR] Nina failed after %v: %v\n", elapsed, err)
		// Show last 20 lines of stderr on error
		lines := strings.Split(string(stderrContent), "\n")
		if len(lines) > 20 {
			fmt.Printf("\n[LAST 20 LINES OF STDERR]\n")
			for _, line := range lines[len(lines)-21:] {
				fmt.Printf("%s\n", line)
			}
		}
	} else {
		fmt.Printf("\n[SUCCESS] Nina completed in %v\n", elapsed)
	}

	return err
}

// RunNinaLoopWithFiles executes nina with a plain prompt.
func RunNinaLoopWithFiles(t *testing.T, repo string, prompt string, _ map[string]string, _ string) error {
	t.Helper()
	// Use environment variable for model, default to o4-mini-flex
	model := os.Getenv("MODEL")
	if model == "" {
		model = "o4-mini-flex"
	}
	// Files are already written to disk by CreateTempRepo
	// Just pass the plain prompt to nina
	return RunNinaLoop(t, repo, prompt, model)
}

// RunNinaLoopWithModel executes nina with a specific model.
func RunNinaLoopWithModel(t *testing.T, repo string, prompt string, _ map[string]string, _ string, model string) error {
	t.Helper()
	return RunNinaLoop(t, repo, prompt, model)
}

// RunNinaLoopWithContinue executes nina with --continue flag.
func RunNinaLoopWithContinue(t *testing.T, repo string, prompt string, model string) error {
	t.Helper()
	BuildNinaLoop(t)

	// Generate UUID for this test run
	testUUID := uuid.New().String()
	registerUUID(testUUID)
	defer unregisterUUID(testUUID)

	// Create output files with simpler names (t.Name() contains slashes which cause issues)
	testName := strings.ReplaceAll(t.Name(), "/", "_")
	outputDir, err := os.MkdirTemp("/tmp", "nina_loop_test_")
	if err != nil {
	    panic(err)
	}
	stdoutFile := filepath.Join(outputDir, fmt.Sprintf("%s.stdout.log", testName))
	stderrFile := filepath.Join(outputDir, fmt.Sprintf("%s.stderr.log", testName))
	fmt.Printf("\n>>> STARTING NINA WITH --continue: %s <<<\n", t.Name())
	fmt.Printf("Model: %s", model)
	fmt.Printf("\n")
	fmt.Printf("Working directory: %s\n", repo)
	fmt.Printf("Timeout: 120s\n")
	fmt.Printf("Prompt length: %d bytes\n", len(prompt))
	fmt.Printf("Process UUID: %s\n", testUUID)
	fmt.Printf("Output files:\n")
	fmt.Printf("  stdout: %s\n", stdoutFile)
	fmt.Printf("  stderr: %s\n", stderrFile)
	fmt.Printf("  (use 'tail -f' to monitor)\n")
	fmt.Printf("---\n")

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		// defer func() {}()
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				elapsed := int(time.Since(start).Seconds())
				remaining := 120 - elapsed
				fmt.Printf("[PROGRESS] Test '%s' running for %ds (timeout in %ds)\n", t.Name(), elapsed, remaining)
			case <-done:
				return
			}
		}
	}()

	// Run with debug mode and --continue flag
	cmd := exec.CommandContext(ctx, NinaBin, "run", "-m", model, "-d", "--continue", "--uuid", testUUID)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(prompt)

	// Create output files
	stdout, err := os.Create(stdoutFile)
	if err != nil {
		return fmt.Errorf("create stdout file: %v", err)
	}
	defer func() { _ = stdout.Close() }()

	stderr, err := os.Create(stderrFile)
	if err != nil {
		return fmt.Errorf("create stderr file: %v", err)
	}
	defer func() { _ = stderr.Close() }()

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	close(done)

	elapsed := time.Since(start)
	fmt.Printf("\n>>> COMPLETED: %s (took %.1fs) <<<\n", t.Name(), elapsed.Seconds())

	// Read and display output files
	stdoutContent, _ := os.ReadFile(stdoutFile)
	stderrContent, _ := os.ReadFile(stderrFile)

	fmt.Printf("\n[STDOUT] (%d bytes)\n", len(stdoutContent))
	if len(stdoutContent) > 0 {
		fmt.Printf("---\n%s\n---\n", stdoutContent)
	} else {
		fmt.Printf("(empty)\n")
	}

	fmt.Printf("\n[STDERR] (%d bytes)\n", len(stderrContent))
	if len(stderrContent) > 0 {
		// Truncate very long stderr to last 10000 bytes
		if len(stderrContent) > 10000 {
			fmt.Printf("(truncated to last 10KB)\n")
			stderrContent = stderrContent[len(stderrContent)-10000:]
		}
		fmt.Printf("---\n%s\n---\n", stderrContent)
	} else {
		fmt.Printf("(empty)\n")
	}

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("nina timed out after 120s")
		}
		return fmt.Errorf("nina run failed: %v", err)
	}

	return nil
}

// RunNinaLoopWithModelAndContext - not needed, simplified to just use RunNinaLoopWithContinue
func RunNinaLoopWithModelAndContext(_ context.Context, t *testing.T, repo string, prompt string, _ map[string]string, _ string, model string) error {
	t.Helper()
	return RunNinaLoopWithContinue(t, repo, prompt, model)
}
