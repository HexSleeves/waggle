package queen

import (
	"fmt"
	"os/exec"
	"strings"
)

// GitState represents the current state of the git repository.
type GitState struct {
	Branch         string
	CommitHash     string
	CommitSubject  string
	DirtyFiles     int
	UntrackedFiles int
}

// GetGitState reads the current git state from the project directory.
// Returns nil if not in a git repo or git is not available.
func GetGitState(projectDir string) *GitState {
	gs := &GitState{}

	// Get current branch
	branch, err := runGit(projectDir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return nil // Not a git repo
	}
	gs.Branch = strings.TrimSpace(branch)

	// Get current commit hash and subject
	hash, err := runGit(projectDir, "rev-parse", "--short", "HEAD")
	if err == nil {
		gs.CommitHash = strings.TrimSpace(hash)
	}
	subject, err := runGit(projectDir, "log", "-1", "--format=%s")
	if err == nil {
		gs.CommitSubject = strings.TrimSpace(subject)
	}

	// Count dirty and untracked files
	status, err := runGit(projectDir, "status", "--porcelain")
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "??") {
				gs.UntrackedFiles++
			} else {
				gs.DirtyFiles++
			}
		}
	}

	return gs
}

// Diff returns a human-readable summary of changes between two git states.
func (g *GitState) Diff(other *GitState) string {
	if g == nil || other == nil {
		return ""
	}

	var parts []string
	if g.CommitHash != other.CommitHash {
		parts = append(parts, fmt.Sprintf("new commit: %s (%s)", other.CommitHash, other.CommitSubject))
	}
	if g.Branch != other.Branch {
		parts = append(parts, fmt.Sprintf("branch changed: %s \u2192 %s", g.Branch, other.Branch))
	}
	dirtyDiff := other.DirtyFiles - g.DirtyFiles
	if dirtyDiff != 0 {
		parts = append(parts, fmt.Sprintf("%+d dirty files", dirtyDiff))
	}

	if len(parts) == 0 {
		return ""
	}
	return "Git: " + strings.Join(parts, ", ")
}

// Equal returns true if two git states are the same.
func (g *GitState) Equal(other *GitState) bool {
	if g == nil || other == nil {
		return g == other
	}
	return g.CommitHash == other.CommitHash &&
		g.Branch == other.Branch &&
		g.DirtyFiles == other.DirtyFiles &&
		g.UntrackedFiles == other.UntrackedFiles
}

// String returns a human-readable representation of the git state.
func (g *GitState) String() string {
	if g == nil {
		return "not a git repo"
	}
	return fmt.Sprintf("%s@%s (dirty:%d, untracked:%d)", g.Branch, g.CommitHash, g.DirtyFiles, g.UntrackedFiles)
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
