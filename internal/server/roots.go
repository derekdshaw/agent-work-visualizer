package server

import (
	"cmp"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/derek-shaw/agent-work-visualizer/internal/transcript"
)

// recentSessionAge is the age of the newest session transcript of a folder
// above which the folder picker does not offer the folder.
const recentSessionAge = 7 * 24 * time.Hour

// rootChoice is a folder that the folder picker offers. Session is the time of
// the latest change to a session transcript of the folder, and is zero if no
// session ran in the folder after the cutoff. Worktree is true for a folder in
// a git worktree of the repository of the current folder.
type rootChoice struct {
	Path     string    `json:"path"`
	Session  time.Time `json:"session,omitzero"`
	Worktree bool      `json:"worktree,omitempty"`
}

// rootChoices returns the working directories of the Claude Code sessions that
// changed after since, newest first. Then it returns the git worktree folders
// of the repository that contains base, in path order. A folder in both lists
// occurs once, in the first list.
func rootChoices(projects string, base string, since time.Time) []rootChoice {
	choices := []rootChoice{}
	for folder, changed := range sessionFolders(projects, since) {
		choices = append(choices, rootChoice{Path: folder, Session: changed})
	}
	slices.SortFunc(choices, func(a, b rootChoice) int {
		return cmp.Or(b.Session.Compare(a.Session), strings.Compare(a.Path, b.Path))
	})
	index := make(map[string]int, len(choices))
	for position, choice := range choices {
		index[choice.Path] = position
	}
	for _, folder := range worktreeFolders(base) {
		if position, ok := index[folder]; ok {
			choices[position].Worktree = true
		} else {
			choices = append(choices, rootChoice{Path: folder, Worktree: true})
		}
	}
	return choices
}

// sessionFolders returns the existing working directories of the sessions in
// projects whose newest transcript changed after since, with the time of that
// change. Each project directory holds the sessions of one working directory,
// so the newest transcript names the working directory.
func sessionFolders(projects string, since time.Time) map[string]time.Time {
	folders := make(map[string]time.Time)
	directories, err := os.ReadDir(projects)
	if err != nil {
		return folders
	}
	for _, directory := range directories {
		if !directory.IsDir() {
			continue
		}
		dir := filepath.Join(projects, directory.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		newest, changed := "", since
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
				continue
			}
			if info, err := entry.Info(); err == nil && info.ModTime().After(changed) {
				newest, changed = filepath.Join(dir, entry.Name()), info.ModTime()
			}
		}
		if newest == "" {
			continue
		}
		file, err := os.Open(newest)
		if err != nil {
			continue
		}
		cwd := transcript.Cwd(file)
		file.Close()
		if info, err := os.Stat(cwd); cwd == "" || err != nil || !info.IsDir() {
			continue
		}
		if changed.After(folders[cwd]) {
			folders[cwd] = changed
		}
	}
	return folders
}

// worktreeFolders returns a folder for each git worktree of the repository
// that contains base: the folder at the same path relative to the worktree as
// base, or the worktree itself if that folder does not exist. It returns nil
// if base is not in a git worktree or git does not answer in 5 seconds.
func worktreeFolders(base string) []string {
	if real, err := filepath.EvalSymlinks(base); err == nil {
		base = real
	}
	home, worktrees := worktreeRoots(base)
	if home == "" {
		return nil
	}
	relative, err := filepath.Rel(home, base)
	if err != nil {
		return nil
	}
	var folders []string
	for _, worktree := range worktrees {
		folder := filepath.Join(worktree, relative)
		if info, err := os.Stat(folder); err != nil || !info.IsDir() {
			folder = worktree
		}
		folders = append(folders, folder)
	}
	slices.Sort(folders)
	return slices.Compact(folders)
}

// worktreeRoots returns the git worktree that contains base and every
// worktree of its repository, without symbolic links. home is "" if base is
// not in a git worktree or git does not answer in 5 seconds.
func worktreeRoots(base string) (home string, worktrees []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", base, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return "", nil
	}
	// git can give paths with or without symbolic links, so the comparison
	// uses paths without them.
	for _, line := range strings.Split(string(output), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			if real, err := filepath.EvalSymlinks(path); err == nil {
				worktrees = append(worktrees, real)
			}
		}
	}
	home = owningWorktree(base, worktrees)
	if home == "" {
		return "", nil
	}
	return home, worktrees
}

// owningWorktree returns the longest worktree that contains path, because a
// worktree can lie inside another worktree, or "" if none does.
func owningWorktree(path string, worktrees []string) string {
	owner := ""
	for _, worktree := range worktrees {
		if transcript.Within(path, worktree) && len(worktree) > len(owner) {
			owner = worktree
		}
	}
	return owner
}

// agentElsewhere returns the working directory of the newest session that
// changed after since and runs in a worktree other than home, or "" if there
// is none. home and worktrees come from worktreeRoots.
func agentElsewhere(projects string, home string, worktrees []string, since time.Time) string {
	if home == "" {
		return ""
	}
	newest, newestTime := "", time.Time{}
	for folder, changed := range sessionFolders(projects, since) {
		if real, err := filepath.EvalSymlinks(folder); err == nil {
			folder = real
		}
		if owner := owningWorktree(folder, worktrees); owner != "" && owner != home && changed.After(newestTime) {
			newest, newestTime = folder, changed
		}
	}
	return newest
}

// gitBranch returns the current branch of the repository that contains root,
// or "" if git does not name one in 5 seconds.
func gitBranch(root string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
