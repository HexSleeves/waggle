package queen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	runCmd(t, dir, "git", "init")
	runCmd(t, dir, "git", "config", "user.email", "test@test.com")
	runCmd(t, dir, "git", "config", "user.name", "Test")
}

func runCmd(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func TestGetGitState(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Create a file and commit
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "initial commit")

	gs := GetGitState(dir)
	if gs == nil {
		t.Fatal("expected non-nil GitState")
	}
	if gs.Branch == "" {
		t.Error("expected non-empty branch")
	}
	if gs.CommitHash == "" {
		t.Error("expected non-empty commit hash")
	}
	if gs.CommitSubject != "initial commit" {
		t.Errorf("expected 'initial commit', got %q", gs.CommitSubject)
	}
	if gs.DirtyFiles != 0 {
		t.Errorf("expected 0 dirty files, got %d", gs.DirtyFiles)
	}
	if gs.UntrackedFiles != 0 {
		t.Errorf("expected 0 untracked files, got %d", gs.UntrackedFiles)
	}

	// Add an untracked file
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	gs2 := GetGitState(dir)
	if gs2.UntrackedFiles != 1 {
		t.Errorf("expected 1 untracked file, got %d", gs2.UntrackedFiles)
	}

	// Modify tracked file
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("modified"), 0644); err != nil {
		t.Fatal(err)
	}
	gs3 := GetGitState(dir)
	if gs3.DirtyFiles != 1 {
		t.Errorf("expected 1 dirty file, got %d", gs3.DirtyFiles)
	}
}

func TestGitStateDiff(t *testing.T) {
	dir := t.TempDir()
	initGitRepo(t, dir)

	// Initial commit
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "first")

	before := GetGitState(dir)

	// Make a new commit
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	runCmd(t, dir, "git", "add", ".")
	runCmd(t, dir, "git", "commit", "-m", "second commit")

	after := GetGitState(dir)

	diff := before.Diff(after)
	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if !strings.Contains(diff, "new commit:") {
		t.Errorf("expected 'new commit:' in diff, got: %s", diff)
	}
	if !strings.Contains(diff, "second commit") {
		t.Errorf("expected commit subject in diff, got: %s", diff)
	}
	if !strings.HasPrefix(diff, "Git: ") {
		t.Errorf("expected 'Git: ' prefix, got: %s", diff)
	}
}

func TestGitStateEqual(t *testing.T) {
	a := &GitState{Branch: "main", CommitHash: "abc123", DirtyFiles: 0, UntrackedFiles: 0}
	b := &GitState{Branch: "main", CommitHash: "abc123", DirtyFiles: 0, UntrackedFiles: 0}

	if !a.Equal(b) {
		t.Error("expected equal states")
	}

	b.DirtyFiles = 1
	if a.Equal(b) {
		t.Error("expected unequal states after dirty change")
	}

	// nil comparison
	var nilState *GitState
	if !nilState.Equal(nil) {
		t.Error("nil.Equal(nil) should be true")
	}
	if nilState.Equal(a) {
		t.Error("nil.Equal(non-nil) should be false")
	}
	if a.Equal(nil) {
		t.Error("non-nil.Equal(nil) should be false")
	}
}

func TestGetGitState_NotARepo(t *testing.T) {
	dir := t.TempDir()
	// No git init
	gs := GetGitState(dir)
	if gs != nil {
		t.Errorf("expected nil for non-git dir, got: %v", gs)
	}
}

func TestGitStateString(t *testing.T) {
	gs := &GitState{Branch: "main", CommitHash: "abc", DirtyFiles: 2, UntrackedFiles: 1}
	s := gs.String()
	if !strings.Contains(s, "main@abc") {
		t.Errorf("expected branch@hash in string, got: %s", s)
	}
	if !strings.Contains(s, "dirty:2") {
		t.Errorf("expected dirty count in string, got: %s", s)
	}

	var nilState *GitState
	if nilState.String() != "not a git repo" {
		t.Errorf("expected 'not a git repo', got: %s", nilState.String())
	}
}

func TestGitStateDiff_DirtyFilesChange(t *testing.T) {
	before := &GitState{Branch: "main", CommitHash: "abc", DirtyFiles: 0}
	after := &GitState{Branch: "main", CommitHash: "abc", DirtyFiles: 3}

	diff := before.Diff(after)
	if !strings.Contains(diff, "+3 dirty files") {
		t.Errorf("expected dirty files diff, got: %s", diff)
	}
}

func TestGitStateDiff_BranchChange(t *testing.T) {
	before := &GitState{Branch: "main", CommitHash: "abc"}
	after := &GitState{Branch: "feature", CommitHash: "abc"}

	diff := before.Diff(after)
	if !strings.Contains(diff, "branch changed") {
		t.Errorf("expected branch change in diff, got: %s", diff)
	}
}

func TestGitStateDiff_NoChange(t *testing.T) {
	state := &GitState{Branch: "main", CommitHash: "abc", DirtyFiles: 1}
	diff := state.Diff(state)
	if diff != "" {
		t.Errorf("expected empty diff for same state, got: %s", diff)
	}
}
