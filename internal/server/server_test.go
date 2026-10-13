package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/derek-shaw/agent-work-visualizer/internal/activity"
	"github.com/derek-shaw/agent-work-visualizer/internal/analyzer"
	"github.com/derek-shaw/agent-work-visualizer/internal/callgraph"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/langtest"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/python"
	"github.com/derek-shaw/agent-work-visualizer/internal/transcript"
	"github.com/derek-shaw/agent-work-visualizer/internal/transcript/transcripttest"
)

func TestServeRoutesRenderTheCurrentGraphVersion(t *testing.T) {
	live := &liveServer{view: &rootView{instance: "abc"}}
	graph := &callgraph.Graph{Nodes: []callgraph.Function{{ID: "a", Name: "alpha", Source: "/*__LIVE__*/null"}}}
	if version, err := live.view.setGraph(graph); err != nil || version != 1 {
		t.Fatalf("setGraph = %d, %v, want version 1", version, err)
	}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)

	page := getBody(t, server.URL+"/", "text/html; charset=utf-8")
	if !strings.Contains(page, `const LIVE={"version":1,"instance":"abc","root":""};`) {
		t.Fatal("page does not carry live version 1 and instance abc")
	}
	// Graph data that contains the LIVE marker stays unchanged.
	if strings.Count(page, "/*__LIVE__*/null") != 1 {
		t.Fatal("graph data containing the LIVE marker was replaced")
	}
	assertGraphPayload(t, getBody(t, server.URL+"/graph", "application/json"), 1, "a")

	if version, err := live.view.setGraph(&callgraph.Graph{Nodes: []callgraph.Function{{ID: "b", Name: "beta"}}}); err != nil || version != 2 {
		t.Fatalf("second setGraph = %d, %v, want version 2", version, err)
	}
	if page := getBody(t, server.URL+"/", "text/html; charset=utf-8"); !strings.Contains(page, `const LIVE={"version":2,"instance":"abc","root":""};`) {
		t.Fatal("page does not carry live version 2")
	}
	assertGraphPayload(t, getBody(t, server.URL+"/graph", "application/json"), 2, "b")

	response, err := http.Get(server.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /missing status = %d, want 404", response.StatusCode)
	}
}

func TestServeRoutesRejectOtherHosts(t *testing.T) {
	live := &liveServer{view: &rootView{}}
	if _, err := live.view.setGraph(&callgraph.Graph{}); err != nil {
		t.Fatal(err)
	}
	handler := live.routes()
	tests := map[string]int{
		"127.0.0.1:7777":                http.StatusOK,
		"localhost:7777":                http.StatusOK,
		"localhost":                     http.StatusOK,
		"attacker.example:7777":         http.StatusForbidden,
		"127.0.0.1.attacker.example:80": http.StatusForbidden,
		"localhost.attacker.example":    http.StatusForbidden,
		"":                              http.StatusForbidden,
	}
	// A canceled context ends the event stream after the history.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, path := range []string{"/", "/graph", "/events", "/roots"} {
		for host, want := range tests {
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
			request.Host = host
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != want {
				t.Errorf("GET %s with Host %q = %d, want %d", path, host, recorder.Code, want)
			}
		}
	}
}

func TestServeEventsReplaysHistoryAndResumes(t *testing.T) {
	live := &liveServer{view: &rootView{instance: "abc"}}
	live.view.publish("activity", map[string]int{"n": 1})
	live.view.publish("status", map[string]string{"message": "two"})
	server := httptest.NewServer(live.routes())
	// Close waits for open streams, so it runs after the stream cleanups.
	t.Cleanup(server.Close)

	// A page that just loaded names its instance and sends no Last-Event-ID.
	events := openEvents(t, server.URL+"/events?instance=abc", "")
	expectEvent(t, events, serverEvent{id: "abc-1", name: "activity", data: `{"n":1}`})
	expectEvent(t, events, serverEvent{id: "abc-2", name: "status", data: `{"message":"two"}`})
	live.view.publish("graph", map[string]int{"version": 2})
	expectEvent(t, events, serverEvent{id: "abc-3", name: "graph", data: `{"version":2}`})

	resumed := openEvents(t, server.URL+"/events?instance=abc", "abc-2")
	expectEvent(t, resumed, serverEvent{id: "abc-3", name: "graph", data: `{"version":2}`})
	live.view.publish("activity", map[string]int{"n": 4})
	expectEvent(t, resumed, serverEvent{id: "abc-4", name: "activity", data: `{"n":4}`})
	expectEvent(t, events, serverEvent{id: "abc-4", name: "activity", data: `{"n":4}`})

	// IDs or pages from an earlier server process and malformed IDs ask the
	// page to reload.
	for _, stale := range []struct{ url, lastEventID string }{
		{"/events", "old-2"}, {"/events", "abc-x"}, {"/events", "abc--1"}, {"/events", "7"},
		{"/events?instance=old", ""}, {"/events?instance=old", "abc-2"},
	} {
		reload := openEvents(t, server.URL+stale.url, stale.lastEventID)
		expectEvent(t, reload, serverEvent{name: "reload", data: "{}"})
		if event, ok := <-reload; ok {
			t.Fatalf("%s with Last-Event-ID %q: stream continued with %+v after reload", stale.url, stale.lastEventID, event)
		}
	}
}

func TestServeGateHoldsFollowedSessionsWhilePaused(t *testing.T) {
	// A short hold limit makes a failed test end instead of wait.
	live := &liveServer{gateLimit: 5 * time.Second, view: &rootView{}}
	live.view.setSessions([]string{"s1", "s2"})
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	// Cleanups run in reverse order, so held calls end before Close waits.
	t.Cleanup(func() { live.setPaused(false) })

	if body := postGate(t, server.URL, `{"session_id":"s1"}`); body != "{}" {
		t.Fatalf("gate before pause = %q, want {}", body)
	}
	for round := range 2 {
		postControl(t, server.URL+"/pause", "", http.StatusNoContent)
		// Calls of other sessions and calls without a session run while paused.
		for _, input := range []string{`{"session_id":"other"}`, `not json`} {
			if body := postGate(t, server.URL, input); body != "{}" {
				t.Fatalf("round %d: gate for %s while paused = %q, want {}", round, input, body)
			}
		}
		held := make(chan string, 2)
		for _, session := range []string{"s1", "s2"} {
			go func() { held <- postGate(t, server.URL, `{"session_id":"`+session+`"}`) }()
		}
		waitForPause(t, live.view, `{"paused":true,"waiting":2}`)
		if round == 0 {
			// A second pause keeps the held calls.
			postControl(t, server.URL+"/pause", "", http.StatusNoContent)
		}
		select {
		case body := <-held:
			t.Fatalf("round %d: gate answered %q before resume", round, body)
		case <-time.After(100 * time.Millisecond):
		}
		postControl(t, server.URL+"/resume", "", http.StatusNoContent)
		for range 2 {
			select {
			case body := <-held:
				if body != "{}" {
					t.Fatalf("round %d: gate after resume = %q, want {}", round, body)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("round %d: gate did not answer after resume", round)
			}
		}
	}
	postControl(t, server.URL+"/resume", "", http.StatusNoContent)
	var got []string
	for _, event := range waitForEvents(t, live.view, "pause", 14) {
		got = append(got, string(event.data))
	}
	held := []string{`{"paused":true,"waiting":0}`, `{"paused":true,"waiting":1}`, `{"paused":true,"waiting":2}`}
	released := []string{`{"paused":false,"waiting":2}`, `{"paused":false,"waiting":1}`, `{"paused":false,"waiting":0}`}
	want := slices.Concat(held, []string{`{"paused":true,"waiting":2}`}, released, held, released, []string{`{"paused":false,"waiting":0}`})
	if !slices.Equal(got, want) {
		t.Fatalf("pause events =\n%v\nwant\n%v", got, want)
	}
}

func TestServeGateDeniesAfterTheHoldLimit(t *testing.T) {
	live := &liveServer{gateLimit: 50 * time.Millisecond, view: &rootView{}}
	live.view.setSessions([]string{"s1"})
	live.setPaused(true)
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	if body := postGate(t, server.URL, `{"session_id":"s1"}`); body != gateDenial {
		t.Fatalf("gate after the hold limit = %q, want the denial", body)
	}
	events := waitForEvents(t, live.view, "pause", 3)
	if data := string(events[2].data); data != `{"paused":true,"waiting":0}` {
		t.Fatalf("pause event after the denial = %s, want no waiting calls", data)
	}
}

func TestServeGateAllowsTheCallWhenTheRequestEnds(t *testing.T) {
	// The default hold limit is longer than the request.
	live := &liveServer{view: &rootView{}}
	live.view.setSessions([]string{"s1"})
	live.setPaused(true)
	// The server cancels request contexts at shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/gate", strings.NewReader(`{"session_id":"s1"}`))
	request.Host = "127.0.0.1:7777"
	recorder := httptest.NewRecorder()
	live.routes().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != "{}" {
		t.Fatalf("gate with an ended request = %d %q, want 200 {}", recorder.Code, recorder.Body.String())
	}
}

func TestServeGateReleasesTheCallWhenTheClientLeaves(t *testing.T) {
	// The hold limit is longer than the wait in waitForPause, so only the
	// closed connection can release the call.
	live := &liveServer{gateLimit: time.Minute, view: &rootView{}}
	live.view.setSessions([]string{"s1"})
	live.setPaused(true)
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	t.Cleanup(func() { live.setPaused(false) })
	// Bytes after the JSON value stay unread if the gate reads only the value.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/gate", strings.NewReader(`{"session_id":"s1"}`+strings.Repeat(" ", 5000)))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		if response, err := http.DefaultClient.Do(request); err == nil {
			response.Body.Close()
		}
		close(done)
	}()
	waitForPause(t, live.view, `{"paused":true,"waiting":1}`)
	cancel()
	<-done
	waitForPause(t, live.view, `{"paused":true,"waiting":0}`)
}

func TestServeControlsRejectOtherOrigins(t *testing.T) {
	live := &liveServer{view: &rootView{}}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	for _, path := range []string{"/pause", "/resume", "/gate", "/root"} {
		postControl(t, server.URL+path, "http://attacker.example", http.StatusForbidden)
		postControl(t, server.URL+path, "null", http.StatusForbidden)
	}
	if events := live.view.hub.since(0); len(events) != 0 {
		t.Fatalf("cross-origin requests published %d events", len(events))
	}
	postControl(t, server.URL+"/pause", server.URL, http.StatusNoContent)
	if !live.gate.paused {
		t.Fatal("same-origin pause did not pause")
	}
	// A GET does not change state, so the Origin check skips it.
	request, err := http.NewRequest(http.MethodGet, server.URL+"/missing", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://attacker.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-origin GET /missing status = %d, want 404", response.StatusCode)
	}
}

// waitForPause waits until the latest pause event has the data want.
func waitForPause(t *testing.T, view *rootView, want string) {
	t.Helper()
	notify, unsubscribe := view.hub.subscribe()
	defer unsubscribe()
	deadline := time.After(10 * time.Second)
	for {
		latest := ""
		for _, event := range view.hub.since(0) {
			if event.name == "pause" {
				latest = string(event.data)
			}
		}
		if latest == want {
			return
		}
		select {
		case <-notify:
		case <-deadline:
			t.Fatalf("latest pause event = %s, want %s", latest, want)
		}
	}
}

func TestLiveEventJSONFieldNames(t *testing.T) {
	// The viewer reads these field names.
	time := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	activity := activity.Activity{
		Time: time, Session: "s1", Agent: "main", Tool: "Write", Kind: "edit", File: "a.py", Line: 2,
		Functions: []activity.Callable{{Key: activity.Key("a.py", "a.f"), Name: "f", Line: 1, Hunks: []int{0}}},
		Hunks:     []transcript.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" def f():", "+    x = 1"}}},
		Created:   true,
		Note:      "n",
	}
	note := transcript.Note{Time: time, Session: "s1", Agent: "main", Kind: "step", Text: "Run"}
	tests := map[string]struct {
		value any
		want  string
	}{
		"session choice":  {rootChoice{Path: "/a", Session: time, Worktree: true}, `{"path":"/a","session":"2026-01-02T03:04:05Z","worktree":true}`},
		"worktree choice": {rootChoice{Path: "/b", Worktree: true}, `{"path":"/b","worktree":true}`},
		"activity": {activity, `{"time":"2026-01-02T03:04:05Z","session":"s1","agent":"main","tool":"Write","kind":"edit","file":"a.py","line":2,` +
			`"functions":[{"key":"a.py\u0000a.f","name":"f","line":1,"hunks":[0]}],` +
			`"hunks":[{"oldStart":1,"oldLines":1,"newStart":1,"newLines":2,"lines":[" def f():","+    x = 1"]}],"created":true,"note":"n"}`},
		"task note": {note, `{"time":"2026-01-02T03:04:05Z","session":"s1","agent":"main","kind":"step","text":"Run"}`},
	}
	for name, test := range tests {
		data, err := json.Marshal(test.value)
		if err != nil || string(data) != test.want {
			t.Errorf("%s JSON = %s, %v, want %s", name, data, err, test.want)
		}
	}
}

// postGate sends a PreToolUse hook input to the gate and returns the body.
func postGate(t *testing.T, url string, input string) string {
	t.Helper()
	response, err := http.Post(url+"/gate", "application/json", strings.NewReader(input))
	if err != nil {
		t.Error(err)
		return ""
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Error(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/json" {
		t.Errorf("POST /gate = %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	return string(body)
}

// postControl sends a POST with an optional Origin header and checks the
// status code.
func postControl(t *testing.T, url string, origin string, want int) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("POST %s with Origin %q = %d, want %d", url, origin, response.StatusCode, want)
	}
}

func TestRefreshPublishesGraphOrKeepsPreviousGraph(t *testing.T) {
	const delay = 200 * time.Millisecond
	live := &liveServer{view: &rootView{}}
	if _, err := live.view.setGraph(&callgraph.Graph{}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	analyze := func() (*callgraph.Graph, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("broken.py:1:1: syntax error")
		}
		return &callgraph.Graph{Nodes: []callgraph.Function{{ID: "fresh"}}}, nil
	}
	edits := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		live.view.refresh(ctx, edits, delay, analyze)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Two quick edits produce one analysis.
	edits <- struct{}{}
	time.Sleep(50 * time.Millisecond)
	edits <- struct{}{}
	events := waitForEvents(t, live.view, "", 1)
	if events[0].name != "status" || !strings.Contains(string(events[0].data), "showing the previous graph: broken.py:1:1: syntax error") {
		t.Fatalf("event = %s %s, want a status event with the error", events[0].name, events[0].data)
	}
	time.Sleep(2 * delay)
	if got := calls.Load(); got != 1 {
		t.Fatalf("analyze calls after two quick edits = %d, want 1", got)
	}
	if page, _ := live.view.content(); !strings.Contains(string(page), `const LIVE={"version":1,`) {
		t.Fatal("failed refresh replaced the graph")
	}

	edits <- struct{}{}
	events = waitForEvents(t, live.view, "", 2)
	if events[1].name != "graph" || string(events[1].data) != `{"version":2}` {
		t.Fatalf("event = %s %s, want graph version 2", events[1].name, events[1].data)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("analyze calls = %d, want 2", got)
	}
}

func TestRefreshWaitsForEditsToStop(t *testing.T) {
	const delay = 200 * time.Millisecond
	live := &liveServer{view: &rootView{}}
	analyzed := make(chan time.Time, 4)
	analyze := func() (*callgraph.Graph, error) {
		analyzed <- time.Now()
		return &callgraph.Graph{}, nil
	}
	edits := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		live.view.refresh(ctx, edits, delay, analyze)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Edits arrive faster than delay for more than twice delay.
	var last time.Time
	for range 5 {
		last = time.Now()
		edits <- struct{}{}
		time.Sleep(delay * 6 / 10)
	}
	select {
	case at := <-analyzed:
		if at.Sub(last) < delay {
			t.Fatalf("analysis ran %v after the last edit, want at least %v", at.Sub(last), delay)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no analysis after the edits stopped")
	}
	select {
	case <-analyzed:
		t.Fatal("edits produced more than one analysis")
	case <-time.After(2 * delay):
	}
}

func TestRefreshAnalyzesAnEditThatArrivesDuringAnalysis(t *testing.T) {
	live := &liveServer{view: &rootView{}}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	analyze := func() (*callgraph.Graph, error) {
		started <- struct{}{}
		<-release
		return &callgraph.Graph{}, nil
	}
	edits := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		live.view.refresh(ctx, edits, 50*time.Millisecond, analyze)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	edits <- struct{}{}
	expectSignal(t, started, "no analysis after the first edit")
	edits <- struct{}{}
	release <- struct{}{}
	expectSignal(t, started, "an edit during analysis did not start another analysis")
	release <- struct{}{}
}

func expectSignal(t *testing.T, signals <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
}

func TestRunFollowsSessionsUnderASymlinkedRoot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "repo")
	langtest.WriteFixture(t, root, "app.py", "def run():\n    return 1\n")
	langtest.WriteFixture(t, root, "test_app.py", "def test_run():\n    return 1\n")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	// Claude Code records the real path of the working directory.
	session := filepath.Join(config, "projects", transcript.EncodeProjectPath(root), "s1.jsonl")
	transcripttest.WriteTranscript(t, session, transcripttest.SessionStart(root), transcripttest.ToolUseLine("r1", "Read"), transcripttest.ToolResultLine("r1", transcripttest.ReadResult(filepath.Join(root, "app.py"), 1, 2)))

	url, result := startServe(t, link)
	if page := getBody(t, url+"/", "text/html; charset=utf-8"); !strings.Contains(page, `const LIVE={"version":1,"instance":"`) {
		t.Fatal("page does not carry live version 1")
	}
	// An agent reads and edits tests, so the live graph includes test files without --include-tests.
	if body := getBody(t, url+"/graph", "application/json"); !strings.Contains(body, `"name":"test_run"`) {
		t.Fatal("live graph does not contain the callable in the test file")
	}
	events := openEvents(t, url+"/events", "")
	for deadline := time.After(10 * time.Second); ; {
		select {
		case event := <-events:
			// Task events for the request and the tool call come first.
			if event.name == "task" {
				continue
			}
			assertActivityEvent(t, hubEvent{name: event.name, data: []byte(event.data)}, "read", "app.py", "run")
		case <-deadline:
			t.Fatal("no activity event")
		}
		break
	}

	// A source edit makes the server analyze root again.
	langtest.WriteFixture(t, root, "app.py", "def run():\n    return added()\n\ndef added():\n    return 1\n")
	patch := []transcript.Hunk{{OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 4, Lines: []string{"-    return 1", "+    return added()", "+", "+def added():", "+    return 1"}}}
	transcripttest.AppendTranscript(t, session, transcripttest.MustJSON(t, transcripttest.ToolUseLine("e1", "Edit"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("e1", map[string]any{"filePath": filepath.Join(root, "app.py"), "structuredPatch": patch}))+"\n")
	for deadline := time.After(15 * time.Second); ; {
		select {
		case event := <-events:
			if event.name != "graph" {
				continue
			}
			if event.data != `{"version":2}` {
				t.Fatalf("graph event = %s, want version 2", event.data)
			}
		case <-deadline:
			t.Fatal("no graph event after the source edit")
		}
		break
	}
	if body := getBody(t, url+"/graph", "application/json"); !strings.Contains(body, `"name":"added"`) || !strings.Contains(body, `"name":"test_run"`) {
		t.Fatal("refreshed graph does not contain the added callable and the callable in the test file")
	}

	// The open event stream must not stop the shutdown.
	stopServe(t, result)
}

// startServe runs Run for root on a free port and returns the server URL and
// the channel that receives the result of Run.
func startServe(t *testing.T, root string) (string, <-chan error) {
	t.Helper()
	logs := &lockedBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	result := make(chan error, 1)
	go func() { result <- Run(pythonAnalyzer(t), root, 0) }()
	address := regexp.MustCompile(`Live view at (http://127\.0\.0\.1:\d+)`)
	for deadline := time.Now().Add(10 * time.Second); ; {
		select {
		case err := <-result:
			t.Fatalf("Run returned %v before it served", err)
		case <-time.After(10 * time.Millisecond):
		}
		if match := address.FindStringSubmatch(logs.String()); match != nil {
			return match[1], result
		} else if time.Now().After(deadline) {
			t.Fatalf("no server address in the log:\n%s", logs.String())
		}
	}
}

// stopServe sends SIGINT to the test process and waits for Run to
// return nil.
func stopServe(t *testing.T, result <-chan error) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("Run = %v, want nil after SIGINT", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop after SIGINT")
	}
}

func TestRunWithoutRootOpensThePickedFolder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "repo")
	langtest.WriteFixture(t, root, "app.py", "def run():\n    return 1\n")
	transcripttest.WriteTranscript(t, filepath.Join(config, "projects", transcript.EncodeProjectPath(root), "s1.jsonl"),
		transcripttest.SessionStart(root), transcripttest.ToolUseLine("r1", "Read"), transcripttest.ToolResultLine("r1", transcripttest.ReadResult(filepath.Join(root, "app.py"), 1, 2)))

	url, result := startServe(t, "")
	page := getBody(t, url+"/", "text/html; charset=utf-8")
	instance := regexp.MustCompile(`const LIVE=\{"version":1,"instance":"([^"]+)","root":""\};`).FindStringSubmatch(page)
	if instance == nil {
		t.Fatal("page without ROOT does not carry an empty root")
	}
	if graph := getBody(t, url+"/graph", "application/json"); !strings.Contains(graph, `"nodes":[],"edges":[],"unresolved":[]`) {
		t.Fatalf("graph without ROOT = %s, want empty lists", graph)
	}
	var roots struct {
		Current string
		Choices []rootChoice
	}
	if err := json.Unmarshal([]byte(getBody(t, url+"/roots", "application/json")), &roots); err != nil {
		t.Fatal(err)
	}
	if roots.Current != "" || !slices.ContainsFunc(roots.Choices, func(choice rootChoice) bool { return choice.Path == root && !choice.Session.IsZero() }) {
		t.Fatalf("roots = %+v, want no current folder and the session folder %s", roots, root)
	}
	events := openEvents(t, url+"/events?instance="+instance[1], "")
	postOpen(t, url, transcripttest.MustJSON(t, map[string]string{"path": root}), http.StatusNoContent, "")
	expectEvent(t, events, serverEvent{id: instance[1] + "-1", name: "reload", data: "{}"})
	if page := getBody(t, url+"/", "text/html; charset=utf-8"); !strings.Contains(page, `"root":`+transcripttest.MustJSON(t, root)+`};`) {
		t.Fatal("page after the switch does not carry the opened folder")
	}
	// The workers of the opened folder follow its session.
	events = openEvents(t, url+"/events", "")
	for deadline := time.After(10 * time.Second); ; {
		select {
		case event := <-events:
			if event.name != "activity" {
				continue
			}
			assertActivityEvent(t, hubEvent{name: event.name, data: []byte(event.data)}, "read", "app.py", "run")
		case <-deadline:
			t.Fatal("no activity event from the opened folder")
		}
		break
	}
	stopServe(t, result)
}

func TestServeOpenFolderShowsTheNewFolder(t *testing.T) {
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := &rootView{root: "/first", instance: "one"}
	if _, err := first.setGraph(&callgraph.Graph{}); err != nil {
		t.Fatal(err)
	}
	var stopped atomic.Bool
	first.cancel = func() { stopped.Store(true) }
	first.setSessions([]string{"s1"})
	live := &liveServer{gateLimit: 5 * time.Second, view: first}
	live.open = func(root string) (*rootView, error) {
		view := &rootView{root: root, instance: "two"}
		view.setSessions([]string{"s2"})
		_, err := view.setGraph(&callgraph.Graph{Nodes: []callgraph.Function{{ID: "b", Name: "beta"}}})
		return view, err
	}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	t.Cleanup(func() { live.setPaused(false) })

	// A held call of the first folder waits through the switch.
	postControl(t, server.URL+"/pause", "", http.StatusNoContent)
	held := make(chan string, 1)
	go func() { held <- postGate(t, server.URL, `{"session_id":"s1"}`) }()
	waitForPause(t, first, `{"paused":true,"waiting":1}`)
	events := openEvents(t, server.URL+"/events?instance=one", "")
	postOpen(t, server.URL, transcripttest.MustJSON(t, map[string]string{"path": folder}), http.StatusNoContent, "")
	for deadline := time.After(5 * time.Second); ; {
		select {
		case event := <-events:
			if event.name != "reload" {
				continue
			}
		case <-deadline:
			t.Fatal("the page of the first folder got no reload event")
		}
		break
	}
	if !stopped.Load() {
		t.Fatal("the workers of the first folder did not stop")
	}
	if page := getBody(t, server.URL+"/", "text/html; charset=utf-8"); !strings.Contains(page, `const LIVE={"version":1,"instance":"two","root":`+transcripttest.MustJSON(t, folder)+`};`) {
		t.Fatal("page does not show the new folder")
	}
	assertGraphPayload(t, getBody(t, server.URL+"/graph", "application/json"), 1, "b")
	if roots := getBody(t, server.URL+"/roots", "application/json"); !strings.HasPrefix(roots, `{"current":`+transcripttest.MustJSON(t, folder)+`,"choices":[`) {
		t.Fatalf("roots = %s, want the new folder as the current folder", roots)
	}
	// A page of the first folder that connects later reloads.
	reload := openEvents(t, server.URL+"/events?instance=one", "")
	expectEvent(t, reload, serverEvent{name: "reload", data: "{}"})

	// The new history contains the pause state, and the gate holds only the
	// sessions of the new folder.
	waitForPause(t, live.current(), `{"paused":true,"waiting":1}`)
	if body := postGate(t, server.URL, `{"session_id":"s1"}`); body != "{}" {
		t.Fatalf("gate for a session of the first folder = %q, want {}", body)
	}
	select {
	case body := <-held:
		t.Fatalf("held call answered %q before resume", body)
	case <-time.After(100 * time.Millisecond):
	}
	postControl(t, server.URL+"/resume", "", http.StatusNoContent)
	select {
	case body := <-held:
		if body != "{}" {
			t.Fatalf("held call after resume = %q, want {}", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resume did not release the held call of the first folder")
	}
}

func TestServeOpenFolderKeepsTheCurrentFolderOnError(t *testing.T) {
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, "file.txt")
	langtest.WriteFixture(t, folder, "file.txt", "x")
	first := &rootView{root: "/first", instance: "one"}
	if _, err := first.setGraph(&callgraph.Graph{}); err != nil {
		t.Fatal(err)
	}
	live := &liveServer{view: first}
	started := make(chan struct{})
	release := make(chan struct{})
	live.open = func(root string) (*rootView, error) {
		if strings.HasSuffix(root, "slow") {
			close(started)
			<-release
		}
		return nil, errors.New("broken.py:1:1: syntax error")
	}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)

	tests := []struct {
		body   string
		status int
		text   string
	}{
		{`not json`, http.StatusBadRequest, "The request names no folder."},
		{`{"path":"  "}`, http.StatusBadRequest, "The request names no folder."},
		{transcripttest.MustJSON(t, map[string]string{"path": filepath.Join(folder, "missing")}), http.StatusUnprocessableEntity, "resolve root:"},
		{transcripttest.MustJSON(t, map[string]string{"path": file}), http.StatusUnprocessableEntity, "is not a folder"},
		{transcripttest.MustJSON(t, map[string]string{"path": folder}), http.StatusUnprocessableEntity, "broken.py:1:1: syntax error"},
	}
	for _, test := range tests {
		postOpen(t, server.URL, test.body, test.status, test.text)
	}
	slow := filepath.Join(folder, "slow")
	if err := os.Mkdir(slow, 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		postOpen(t, server.URL, transcripttest.MustJSON(t, map[string]string{"path": slow}), http.StatusUnprocessableEntity, "syntax error")
		close(done)
	}()
	expectSignal(t, started, "the slow folder did not start to open")
	postOpen(t, server.URL, transcripttest.MustJSON(t, map[string]string{"path": folder}), http.StatusConflict, "already opens another folder")
	close(release)
	<-done

	if live.current() != first || len(first.hub.since(0)) != 0 {
		t.Fatal("a folder that did not open replaced the current folder or published events")
	}
}

func TestOpenRootFollowsSessionsBeforeItReturns(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	projects := filepath.Join(base, "projects")
	root := filepath.Join(base, "repo")
	langtest.WriteFixture(t, root, "app.py", "def run():\n    return 1\n")
	transcripttest.WriteTranscript(t, filepath.Join(projects, transcript.EncodeProjectPath(root), "s1.jsonl"), transcripttest.SessionStart(root))
	var workers sync.WaitGroup
	view, err := openRoot(context.Background(), &workers, pythonAnalyzer(t), projects, root)
	if err != nil {
		t.Fatal(err)
	}
	if !view.follows("s1") {
		t.Error("a paused gate does not hold the sessions of a new folder until the first background scan")
	}
	view.stop()
	stopped := make(chan struct{})
	go func() {
		workers.Wait()
		close(stopped)
	}()
	expectSignal(t, stopped, "the workers of a stopped view did not stop")
}

func TestRunStopsWorkersWhenListenFails(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	root := t.TempDir()
	langtest.WriteFixture(t, root, "app.py", "def run():\n    return 1\n")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	result := make(chan error, 1)
	go func() { result <- Run(pythonAnalyzer(t), root, port) }()
	select {
	case err := <-result:
		if err == nil || !strings.Contains(err.Error(), "listen:") {
			t.Fatalf("Run = %v, want a listen error", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not stop the workers of the folder after listen failed")
	}
}

func TestResolveRootExpandsHome(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	project := filepath.Join(home, "proj")
	langtest.WriteFixture(t, project, "a.py", "x = 1\n")
	tests := []struct {
		path string
		want string
	}{
		{"~", home},
		{"~/proj", project},
		{"~/proj/../proj/", project},
		// Only "~" and "~/" name the home directory.
		{"~proj", ""},
	}
	for _, test := range tests {
		got, err := resolveRoot(test.path)
		if got != test.want || (err != nil) != (test.want == "") {
			t.Errorf("resolveRoot(%q) = %q, %v, want %q", test.path, got, err, test.want)
		}
	}
}

func TestRootViewPageEscapesTheRoot(t *testing.T) {
	view := &rootView{root: "/tmp/a</script><img src=x onerror=alert(1)>/*__GRAPH__*/b", instance: "one"}
	if _, err := view.setGraph(&callgraph.Graph{Nodes: []callgraph.Function{{ID: "n", Name: "f"}}}); err != nil {
		t.Fatal(err)
	}
	page, _ := view.content()
	if strings.Contains(string(page), "</script><img") {
		t.Fatal("the root path ends the script element of the page")
	}
	if !strings.Contains(string(page), `let DATA={"root":`) {
		t.Fatal("the graph data replaces a marker in the root path instead of the graph marker")
	}
	match := regexp.MustCompile(`const LIVE=(\{.*?\});`).FindSubmatch(page)
	var live struct {
		Root string `json:"root"`
	}
	if match == nil || json.Unmarshal(match[1], &live) != nil || live.Root != view.root {
		t.Fatalf("LIVE = %q, want the root %q", match, view.root)
	}
}

// postOpen asks the server to open a folder and checks the status code and
// that the answer contains text.
func postOpen(t *testing.T, url string, body string, status int, text string) {
	t.Helper()
	response, err := http.Post(url+"/root", "application/json", strings.NewReader(body))
	if err != nil {
		t.Error(err)
		return
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		t.Error(err)
	}
	if response.StatusCode != status || !strings.Contains(string(answer), text) {
		t.Errorf("POST /root %s = %d %q, want %d with %q", body, response.StatusCode, answer, status, text)
	}
}

func TestWatchPublishesActivityAndSignalsSourceEdits(t *testing.T) {
	projects := t.TempDir()
	root := t.TempDir()
	langtest.WriteFixture(t, root, "app.py", "def run():\n    return 1\n")
	langtest.WriteFixture(t, root, "notes.md", "# Notes\n")
	source := filepath.Join(root, "app.py")
	notes := filepath.Join(root, "notes.md")
	patch := []transcript.Hunk{{OldStart: 1, OldLines: 1, NewStart: 1, NewLines: 2, Lines: []string{" def run():", "+    return 1"}}}
	session := filepath.Join(projects, transcript.EncodeProjectPath(root), "s1.jsonl")
	transcripttest.WriteTranscript(t, session,
		transcripttest.SessionStart(root),
		transcripttest.ToolUseLine("e1", "Edit"), transcripttest.ToolResultLine("e1", map[string]any{"filePath": notes, "structuredPatch": patch}),
	)

	live := &liveServer{view: &rootView{}}
	edits := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a := pythonAnalyzer(t)
		live.view.watch(ctx, a, transcript.NewWatcher(projects, root), activity.NewMapper(root, a), edits)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	events := waitForEvents(t, live.view, "activity", 1)
	assertActivityEvent(t, events[0], "edit", "notes.md")
	// The task notes of a poll come before its activity, and the gate follows
	// the session.
	all := live.view.hub.since(0)
	var note transcript.Note
	if all[0].name != "task" || json.Unmarshal(all[0].data, &note) != nil || note.Kind != "request" || note.Text != "start" || note.Session != "s1" {
		t.Fatalf("first event = %s %s, want the request note of s1", all[0].name, all[0].data)
	}
	if live.hold("s1") != nil {
		t.Fatal("gate holds a call while work is not paused")
	}
	live.setPaused(true)
	if live.hold("s1") == nil {
		t.Fatal("gate does not hold a call of the followed session")
	}
	live.release()
	live.setPaused(false)

	transcripttest.AppendTranscript(t, session, transcripttest.MustJSON(t, transcripttest.ToolUseLine("r1", "Read"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("r1", transcripttest.ReadResult(source, 1, 2)))+"\n")
	events = waitForEvents(t, live.view, "activity", 2)
	assertActivityEvent(t, events[1], "read", "app.py", "run")
	// The watcher handled the notes edit and the read without a source edit.
	select {
	case <-edits:
		t.Fatal("edits to non-source files or reads signaled a refresh")
	default:
	}

	transcripttest.AppendTranscript(t, session, transcripttest.MustJSON(t, transcripttest.ToolUseLine("e2", "Edit"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("e2", map[string]any{"filePath": source, "structuredPatch": patch}))+"\n")
	events = waitForEvents(t, live.view, "activity", 3)
	assertActivityEvent(t, events[2], "edit", "app.py", "run")
	select {
	case <-edits:
	case <-time.After(5 * time.Second):
		t.Fatal("source edit did not signal a refresh")
	}

	// The gate follows a session that starts after the first scan.
	start := transcripttest.SessionStart(root)
	start["sessionId"] = "s2"
	transcripttest.WriteTranscript(t, filepath.Join(projects, transcript.EncodeProjectPath(root), "s2.jsonl"),
		start, transcripttest.ToolUseLine("r2", "Read"), transcripttest.ToolResultLine("r2", transcripttest.ReadResult(source, 1, 2)),
	)
	waitForEvents(t, live.view, "activity", 4)
	live.setPaused(true)
	if live.hold("s2") == nil {
		t.Fatal("gate does not hold a call of a session that started after the first scan")
	}
	live.release()
	live.setPaused(false)
}

// lockedBuffer collects log output from several goroutines.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

type serverEvent struct {
	id   string
	name string
	data string
}

// openEvents streams the events URL and sends each parsed event on the
// returned channel. The stream closes when the test ends.
func openEvents(t *testing.T, url string, lastEventID string) <-chan serverEvent {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		response.Body.Close()
	})
	if got := response.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q", got)
	}
	events := make(chan serverEvent, 16)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(response.Body)
		var event serverEvent
		for scanner.Scan() {
			field, value, _ := strings.Cut(scanner.Text(), ": ")
			switch field {
			case "id":
				event.id = value
			case "event":
				event.name = value
			case "data":
				event.data = value
			case "":
				events <- event
				event = serverEvent{}
			}
		}
	}()
	return events
}

func expectEvent(t *testing.T, events <-chan serverEvent, want serverEvent) {
	t.Helper()
	select {
	case got, ok := <-events:
		if !ok {
			t.Fatalf("stream ended, want %+v", want)
		}
		if got != want {
			t.Fatalf("event = %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("no event, want %+v", want)
	}
}

// waitForEvents waits until the hub holds count events named name, or count
// events of any name if name is empty, and returns those events.
func waitForEvents(t *testing.T, view *rootView, name string, count int) []hubEvent {
	t.Helper()
	notify, unsubscribe := view.hub.subscribe()
	defer unsubscribe()
	deadline := time.After(10 * time.Second)
	for {
		var events []hubEvent
		for _, event := range view.hub.since(0) {
			if name == "" || event.name == name {
				events = append(events, event)
			}
		}
		if len(events) >= count {
			return events
		}
		select {
		case <-notify:
		case <-deadline:
			t.Fatalf("got %d %q events, want %d", len(events), name, count)
		}
	}
}

func assertActivityEvent(t *testing.T, event hubEvent, kind string, file string, names ...string) {
	t.Helper()
	var activity activity.Activity
	if event.name != "activity" || json.Unmarshal(event.data, &activity) != nil {
		t.Fatalf("event = %s %s, want an activity event", event.name, event.data)
	}
	var got []string
	for _, function := range activity.Functions {
		got = append(got, function.Name)
	}
	if activity.Kind != kind || activity.File != file || strings.Join(got, ",") != strings.Join(names, ",") {
		t.Fatalf("activity = %s, want %s %s %v", event.data, kind, file, names)
	}
}

func getBody(t *testing.T, url string, contentType string) string {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != contentType || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("GET %s = %d %q %q", url, response.StatusCode, response.Header.Get("Content-Type"), response.Header.Get("Cache-Control"))
	}
	return string(body)
}

func assertGraphPayload(t *testing.T, body string, version int, nodeID string) {
	t.Helper()
	var payload struct {
		Version int             `json:"version"`
		Graph   callgraph.Graph `json:"graph"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != version || len(payload.Graph.Nodes) != 1 || payload.Graph.Nodes[0].ID != nodeID {
		t.Fatalf("graph payload = %s, want version %d with node %s", body, version, nodeID)
	}
}

// pythonAnalyzer returns an analyzer for the Python fixtures.
func pythonAnalyzer(t *testing.T) *analyzer.Analyzer {
	t.Helper()
	a := analyzer.New()
	if err := a.Register(python.New()); err != nil {
		t.Fatal(err)
	}
	return a
}
