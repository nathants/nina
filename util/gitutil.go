// gitutil.go provides git repository utilities used across nina projects
package util

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// GetGitRoot returns the git repository root directory or empty string if not in a git repo
func GetGitRoot() string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// GetAgentsDir returns the appropriate agents directory path
func GetAgentsDir() string {
	gitRoot := GetGitRoot()
	if gitRoot != "" {
		return filepath.Join(gitRoot, "agents")
	}
	return "agents"
}

// GetAgentsSubdir returns a subdirectory path under the agents directory
func GetAgentsSubdir(subdir string) string {
	return filepath.Join(GetAgentsDir(), subdir)
}