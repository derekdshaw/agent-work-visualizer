package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/derekdshaw/agent-work-visualizer/internal/lang/langtest"
	"github.com/derekdshaw/agent-work-visualizer/internal/transcript/transcripttest"
)

func TestRootChoices(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(base, "projects")
	now := time.Now().Truncate(time.Second)
	since := now.Add(-recentSessionAge)
	session := func(project string, name string, cwd string, changed time.Time) {
		t.Helper()
		transcript := filepath.Join(projects, project, name)
		transcripttest.WriteTranscript(t, transcript, transcripttest.SessionStart(cwd))
		if err := os.Chtimes(transcript, changed, changed); err != nil {
			t.Fatal(err)
		}
	}
	folder := func(name string) string {
		t.Helper()
		folder := filepath.Join(base, name)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		return folder
	}

	t.Run("sessions", func(t *testing.T) {
		alpha, beta, gamma, delta, older := folder("alpha"), folder("beta"), folder("gamma"), folder("delta"), folder("older")
		session("alpha", "s1.jsonl", alpha, now.Add(-time.Hour))
		// The newest transcript of a project directory names its folder.
		session("beta", "s1.jsonl", beta, now.Add(-2*time.Hour))
		session("beta", "s0.jsonl", older, now.Add(-3*time.Hour))
		// Equal times sort by path.
		session("gamma", "s1.jsonl", gamma, now.Add(-time.Hour))
		session("delta", "s1.jsonl", delta, since.Add(-time.Minute))
		session("missing", "s1.jsonl", filepath.Join(base, "missing"), now)
		session("empty", "s1.jsonl", "", now)
		// The newest transcript of two project directories with the same folder
		// gives the time. The newer directory sorts first, so the older one
		// must not replace its time.
		session("a-beta-copy", "s1.jsonl", beta, now.Add(-90*time.Minute))
		langtest.WriteFixture(t, filepath.Join(projects, "alpha"), "notes.txt", "x")
		langtest.WriteFixture(t, projects, "file.jsonl", "{}")

		got := rootChoices(projects, folder("plain"), since)
		want := []rootChoice{
			{Path: alpha, Session: now.Add(-time.Hour)},
			{Path: gamma, Session: now.Add(-time.Hour)},
			{Path: beta, Session: now.Add(-90 * time.Minute)},
		}
		assertRootChoices(t, got, want)
	})

	t.Run("missing projects", func(t *testing.T) {
		if got := rootChoices(filepath.Join(base, "none"), base, since); got == nil || len(got) != 0 {
			t.Fatalf("rootChoices = %#v, want an empty list", got)
		}
	})

	t.Run("worktrees", func(t *testing.T) {
		if _, err := exec.LookPath("git"); err != nil {
			t.Skip("git is not installed")
		}
		t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
		t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
		repo := folder("repo")
		git := func(arguments ...string) {
			t.Helper()
			command := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=test", "-c", "user.email=test@example.com"}, arguments...)...)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", arguments, err, output)
			}
		}
		git("init", "-q")
		git("commit", "-q", "--allow-empty", "-m", "start")
		git("worktree", "add", "-q", filepath.Join(base, "two"))
		git("worktree", "add", "-q", filepath.Join(base, "three"))
		// A worktree can lie inside the main worktree.
		nested := filepath.Join(repo, ".worktrees", "x")
		git("worktree", "add", "-q", nested)
		inside := folder("repo/sub")
		folder("three/sub")
		folder("repo/.worktrees/x/sub")
		projects := filepath.Join(base, "worktree-projects")
		transcript := filepath.Join(projects, "repo-sub", "s1.jsonl")
		transcripttest.WriteTranscript(t, transcript, transcripttest.SessionStart(inside))
		if err := os.Chtimes(transcript, now, now); err != nil {
			t.Fatal(err)
		}

		got := rootChoices(projects, inside, since)
		want := []rootChoice{
			{Path: inside, Session: now, Worktree: true},
			{Path: filepath.Join(nested, "sub"), Worktree: true},
			{Path: filepath.Join(base, "three", "sub"), Worktree: true},
			{Path: filepath.Join(base, "two"), Worktree: true},
		}
		assertRootChoices(t, got, want)
		// The folder in the nested worktree lies in the nested worktree, not in
		// the main worktree.
		if got, want := worktreeFolders(filepath.Join(nested, "sub")), worktreeFolders(inside); len(want) != 4 || !reflect.DeepEqual(got, want) {
			t.Fatalf("worktreeFolders in a nested worktree = %v, want %v", got, want)
		}
		// The directory where the server starts can have a symbolic link.
		link := filepath.Join(base, "link")
		if err := os.Symlink(repo, link); err != nil {
			t.Fatal(err)
		}
		if got, want := worktreeFolders(filepath.Join(link, "sub")), worktreeFolders(inside); len(want) != 4 || !reflect.DeepEqual(got, want) {
			t.Fatalf("worktreeFolders through a symbolic link = %v, want %v", got, want)
		}

		// Recent agent work in another worktree is a mismatch, and work in the
		// same worktree or an older session is not.
		home, worktrees := worktreeRoots(inside)
		if home != repo || len(worktrees) != 4 {
			t.Fatalf("worktreeRoots = %q, %v, want %s and 4 worktrees", home, worktrees, repo)
		}
		if got := agentElsewhere(projects, home, worktrees, since); got != "" {
			t.Fatalf("agentElsewhere with work in the same worktree = %q, want none", got)
		}
		elsewhere := filepath.Join(projects, "three-sub", "s1.jsonl")
		transcripttest.WriteTranscript(t, elsewhere, transcripttest.SessionStart(filepath.Join(base, "three", "sub")))
		if got := agentElsewhere(projects, home, worktrees, since); got != filepath.Join(base, "three", "sub") {
			t.Fatalf("agentElsewhere = %q, want the folder in worktree three", got)
		}
		if got := agentElsewhere(projects, home, worktrees, now.Add(time.Hour)); got != "" {
			t.Fatalf("agentElsewhere with only older sessions = %q, want none", got)
		}
		if got := agentElsewhere(projects, "", nil, since); got != "" {
			t.Fatalf("agentElsewhere outside a repository = %q, want none", got)
		}
		if err := os.Remove(elsewhere); err != nil {
			t.Fatal(err)
		}
		if gitBranch(repo) == "" || gitBranch(folder("not-a-repo")) != "" {
			t.Fatalf("gitBranch = %q in the repository and %q outside", gitBranch(repo), gitBranch(folder("not-a-repo")))
		}

		// Without a current folder, the list starts at the directory where the
		// server started.
		t.Chdir(filepath.Join(link, "sub"))
		live := &liveServer{projects: projects, view: &rootView{}}
		recorder := httptest.NewRecorder()
		live.serveRoots(recorder, httptest.NewRequest(http.MethodGet, "/roots", nil))
		if body := recorder.Body.String(); !strings.HasPrefix(body, `{"current":"","choices":[{"path":`+transcripttest.MustJSON(t, inside)+`,"session":`) {
			t.Fatalf("roots = %s, want no current folder and the session folder first", body)
		}

		// git still lists a worktree whose folder is gone.
		if err := os.RemoveAll(filepath.Join(base, "two")); err != nil {
			t.Fatal(err)
		}
		if got := worktreeFolders(inside); slices.Contains(got, filepath.Join(base, "two")) || len(got) != 3 {
			t.Fatalf("worktreeFolders with a removed worktree = %v, want the 3 other folders", got)
		}
		if got := worktreeFolders(folder("outside")); got != nil {
			t.Fatalf("worktreeFolders outside a repository = %v, want nil", got)
		}
	})
}

func assertRootChoices(t *testing.T, got []rootChoice, want []rootChoice) {
	t.Helper()
	type choice struct {
		path     string
		session  int64
		worktree bool
	}
	simple := func(choices []rootChoice) []choice {
		var result []choice
		for _, item := range choices {
			session := int64(0)
			if !item.Session.IsZero() {
				session = item.Session.Unix()
			}
			result = append(result, choice{item.Path, session, item.Worktree})
		}
		return result
	}
	if !reflect.DeepEqual(simple(got), simple(want)) {
		t.Fatalf("rootChoices = %+v, want %+v", simple(got), simple(want))
	}
}
