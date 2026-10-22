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
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/derek-shaw/agent-work-visualizer/internal/activity"
	"github.com/derek-shaw/agent-work-visualizer/internal/analyzer"
	"github.com/derek-shaw/agent-work-visualizer/internal/callgraph"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/langtest"
	"github.com/derek-shaw/agent-work-visualizer/internal/lang/python"
	"github.com/derek-shaw/agent-work-visualizer/internal/session"
	"github.com/derek-shaw/agent-work-visualizer/internal/transcript"
	"github.com/derek-shaw/agent-work-visualizer/internal/transcript/transcripttest"
)

var testTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func TestGraphRouteServesTheCurrentGraphVersion(t *testing.T) {
	view := testView(t, "/repo")
	live := &liveServer{view: view, web: webHandler(fstest.MapFS{"index.html": {Data: []byte("page")}})}
	graph := &callgraph.Graph{Nodes: []callgraph.Function{{ID: "a.py:1:a.f", Name: "f", QualifiedName: "a.f", File: "a.py"}}}
	// testView sets version 1.
	if version, err := view.setGraph(graph); err != nil || version != 2 {
		t.Fatalf("setGraph = %d, %v, want version 2", version, err)
	}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)

	payload := decodeGraph(t, getBody(t, server.URL+"/api/graph", "application/json"))
	if payload.Version != 2 || payload.View != view.log.Name() || payload.Root != "/repo" || len(payload.Nodes) != 1 || payload.Nodes[0].ID != activity.Key("a.py", "a.f") {
		t.Fatalf("graph payload = %+v, want version 2 of the view with the node keyed by file and name", payload)
	}
	if _, err := view.setGraph(&callgraph.Graph{Nodes: []callgraph.Function{{ID: "b", Name: "g", QualifiedName: "b.g", File: "b.py"}}}); err != nil {
		t.Fatal(err)
	}
	if payload := decodeGraph(t, getBody(t, server.URL+"/api/graph", "application/json")); payload.Version != 3 || payload.Nodes[0].Label != "g" {
		t.Fatalf("graph payload = %+v, want version 3", payload)
	}
	if page := getBody(t, server.URL+"/", "text/html; charset=utf-8"); page != "page" {
		t.Fatalf("GET / = %q, want the built page", page)
	}
	response, err := http.Get(server.URL + "/missing")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /missing status = %d, want 404", response.StatusCode)
	}
}

func TestSlimGraphKeysNodesAndMergesCollisions(t *testing.T) {
	graph := &callgraph.Graph{
		Nodes: []callgraph.Function{
			{ID: "A.java:3:p.A.run", Name: "run", QualifiedName: "p.A.run", File: "A.java", Directory: "", Kind: "method", Language: "java", Line: 3, EndLine: 4},
			// An overload has the same file and qualified name.
			{ID: "A.java:6:p.A.run", Name: "run", QualifiedName: "p.A.run", File: "A.java", Kind: "method", Language: "java", Line: 6, EndLine: 7},
			{ID: "pkg/B.java:2:p.B.go", Name: "go", QualifiedName: "p.B.go", File: "pkg/B.java", Directory: "pkg", Kind: "method", Language: "java", Line: 2, EndLine: 9},
		},
		Edges: []callgraph.Edge{
			{Source: "A.java:3:p.A.run", Target: "A.java:6:p.A.run", Kind: "call"},
			{Source: "A.java:3:p.A.run", Target: "pkg/B.java:2:p.B.go", Kind: "call"},
			{Source: "A.java:6:p.A.run", Target: "pkg/B.java:2:p.B.go", Kind: "call"},
			{Source: "A.java:6:p.A.run", Target: "pkg/B.java:2:p.B.go", Kind: "dispatch"},
			{Source: "missing", Target: "pkg/B.java:2:p.B.go", Kind: "call"},
		},
	}
	nodes, edges := slimGraph(graph)
	run, goKey := activity.Key("A.java", "p.A.run"), activity.Key("pkg/B.java", "p.B.go")
	wantNodes := []graphNode{
		{ID: run, Label: "run", QualifiedName: "p.A.run", Module: ".", Kind: "method", Language: "java", File: "A.java", Line: 3, EndLine: 4},
		{ID: goKey, Label: "go", QualifiedName: "p.B.go", Module: "pkg", Kind: "method", Language: "java", File: "pkg/B.java", Line: 2, EndLine: 9},
	}
	wantEdges := []graphEdge{{A: run, B: goKey, Kind: "call"}, {A: run, B: goKey, Kind: "dispatch"}}
	if !reflect.DeepEqual(nodes, wantNodes) || !reflect.DeepEqual(edges, wantEdges) {
		t.Fatalf("slimGraph =\n%+v\n%+v\nwant\n%+v\n%+v", nodes, edges, wantNodes, wantEdges)
	}
	if nodes, edges := slimGraph(&callgraph.Graph{}); nodes == nil || edges == nil {
		t.Fatal("an empty graph has nil lists, which encode as null")
	}
}

func TestGraphPayloadJSONFieldNames(t *testing.T) {
	// web/src/graph/load.ts reads these field names.
	payload := graphPayload{
		View: "v", Root: "/r", Branch: "main", Version: 2,
		Nodes: []graphNode{{ID: "k", Label: "f", QualifiedName: "a.f", Module: ".", Kind: "function", Language: "python", File: "a.py", Line: 1, EndLine: 2}},
		Edges: []graphEdge{{A: "k", B: "j", Kind: "call"}},
	}
	want := `{"view":"v","root":"/r","branch":"main","version":2,` +
		`"nodes":[{"id":"k","label":"f","qualifiedName":"a.f","module":".","kind":"function","language":"python","file":"a.py","line":1,"endLine":2}],` +
		`"edges":[{"a":"k","b":"j","kind":"call"}]}`
	choice := rootChoice{Path: "/a", Session: testTime, Worktree: true}
	if data, err := json.Marshal(payload); err != nil || string(data) != want {
		t.Errorf("graph payload JSON = %s, %v, want %s", data, err, want)
	}
	if data, err := json.Marshal(choice); err != nil || string(data) != `{"path":"/a","session":"2026-01-02T03:04:05Z","worktree":true}` {
		t.Errorf("root choice JSON = %s, %v", data, err)
	}
}

func TestServeRoutesRejectOtherHosts(t *testing.T) {
	live := &liveServer{view: testView(t, ""), web: webHandler(fstest.MapFS{})}
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
	paths := map[string]int{"/": http.StatusServiceUnavailable, "/api/graph": http.StatusOK, "/events": http.StatusOK,
		"/api/roots": http.StatusOK, "/api/sessions": http.StatusOK, "/api/sessions/20260101-000000-a": http.StatusNotFound}
	for path, allowed := range paths {
		for host, want := range tests {
			if want == http.StatusOK {
				want = allowed
			}
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

func TestServeControlsRejectOtherOrigins(t *testing.T) {
	live := &liveServer{view: testView(t, "")}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	for _, path := range []string{"/api/pause", "/api/resume", "/gate", "/api/root"} {
		postControl(t, server.URL+path, "http://attacker.example", http.StatusForbidden)
		postControl(t, server.URL+path, "null", http.StatusForbidden)
	}
	if controls := live.view.controlsSince(0); len(controls) != 0 {
		t.Fatalf("cross-origin requests published %d events", len(controls))
	}
	postControl(t, server.URL+"/api/pause", server.URL, http.StatusNoContent)
	if !live.gate.paused {
		t.Fatal("same-origin pause did not pause")
	}
	// A GET does not change state, so the Origin check skips it.
	request, err := http.NewRequest(http.MethodGet, server.URL+"/api/graph", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", "http://attacker.example")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("cross-origin GET /api/graph status = %d, want 200", response.StatusCode)
	}
}

func TestServeEventsStreamsTheLogAndResumes(t *testing.T) {
	view := testView(t, "/repo")
	view.appendEvents([]session.Event{{At: testTime, Type: "prompt", Text: "go"}})
	view.setControl("pause", pauseState{})
	live := &liveServer{view: view}
	server := httptest.NewServer(live.routes())
	// Close waits for open streams, so it runs after the stream cleanups.
	t.Cleanup(server.Close)
	name := view.log.Name()

	events := openEvents(t, server.URL+"/events?view="+name, "")
	header := expectLogEvent(t, events, "1")
	if !strings.HasPrefix(header, `{"t":0,"type":"session","root":"/repo"`) {
		t.Fatalf("first line = %s, want the header", header)
	}
	if line := expectLogEvent(t, events, "2"); !strings.Contains(line, `"type":"prompt","text":"go"`) {
		t.Fatalf("second line = %s, want the prompt", line)
	}
	expectEvent(t, events, serverEvent{name: "pause", data: `{"paused":false,"waiting":0}`})
	view.appendEvents([]session.Event{{At: testTime.Add(time.Second), Type: "plan", Text: "next"}})
	if line := expectLogEvent(t, events, "3"); !strings.Contains(line, `"t":1,`) {
		t.Fatalf("third line = %s, want t 1", line)
	}
	view.setControl("graph", map[string]int{"version": 2})
	expectEvent(t, events, serverEvent{name: "graph", data: `{"version":2}`})

	// A reconnect resumes after its Last-Event-ID and gets the latest controls.
	resumed := openEvents(t, server.URL+"/events?view="+name, "2")
	expectLogEvent(t, resumed, "3")
	expectEvent(t, resumed, serverEvent{name: "pause", data: `{"paused":false,"waiting":0}`})
	expectEvent(t, resumed, serverEvent{name: "graph", data: `{"version":2}`})

	// Another view, a missing view, and IDs that do not fit the log reset the page.
	for _, stale := range []struct{ query, lastEventID string }{
		{"?view=other", ""}, {"", ""}, {"?view=" + name, "x"}, {"?view=" + name, "-1"}, {"?view=" + name, "4"},
	} {
		reset := openEvents(t, server.URL+"/events"+stale.query, stale.lastEventID)
		expectEvent(t, reset, serverEvent{name: "reset", data: "{}"})
		if event, ok := <-reset; ok {
			t.Fatalf("%s with Last-Event-ID %q: stream continued with %+v after reset", stale.query, stale.lastEventID, event)
		}
	}
}

func TestServeGateHoldsFollowedSessionsWhilePaused(t *testing.T) {
	// A short hold limit makes a failed test end instead of wait.
	live := &liveServer{gateLimit: 5 * time.Second, view: testView(t, "")}
	live.view.setSessions([]string{"s1", "s2"})
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	// Cleanups run in reverse order, so held calls end before Close waits.
	t.Cleanup(func() { live.setPaused(false) })

	if body := postGate(t, server.URL, `{"session_id":"s1"}`); body != "{}" {
		t.Fatalf("gate before pause = %q, want {}", body)
	}
	for round := range 2 {
		postControl(t, server.URL+"/api/pause", "", http.StatusNoContent)
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
		waitForControl(t, live.view, "pause", `{"paused":true,"waiting":2}`)
		if round == 0 {
			// A second pause keeps the held calls.
			postControl(t, server.URL+"/api/pause", "", http.StatusNoContent)
		}
		select {
		case body := <-held:
			t.Fatalf("round %d: gate answered %q before resume", round, body)
		case <-time.After(100 * time.Millisecond):
		}
		postControl(t, server.URL+"/api/resume", "", http.StatusNoContent)
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
		waitForControl(t, live.view, "pause", `{"paused":false,"waiting":0}`)
	}
}

func TestServeGateDeniesAfterTheHoldLimit(t *testing.T) {
	live := &liveServer{gateLimit: 50 * time.Millisecond, view: testView(t, "")}
	live.view.setSessions([]string{"s1"})
	live.setPaused(true)
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	if body := postGate(t, server.URL, `{"session_id":"s1"}`); body != gateDenial {
		t.Fatalf("gate after the hold limit = %q, want the denial", body)
	}
	waitForControl(t, live.view, "pause", `{"paused":true,"waiting":0}`)
}

func TestServeGateAllowsTheCallWhenTheRequestEnds(t *testing.T) {
	// The default hold limit is longer than the request.
	live := &liveServer{view: testView(t, "")}
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
	// The hold limit is longer than the wait in waitForControl, so only the
	// closed connection can release the call.
	live := &liveServer{gateLimit: time.Minute, view: testView(t, "")}
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
	waitForControl(t, live.view, "pause", `{"paused":true,"waiting":1}`)
	cancel()
	<-done
	waitForControl(t, live.view, "pause", `{"paused":true,"waiting":0}`)
}

func TestRefreshPublishesGraphOrKeepsPreviousGraph(t *testing.T) {
	const delay = 200 * time.Millisecond
	// testView sets graph version 1.
	view := testView(t, "")
	var calls atomic.Int32
	analyze := func() (*callgraph.Graph, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("broken.py:1:1: syntax error")
		}
		return &callgraph.Graph{Nodes: []callgraph.Function{{ID: "fresh", QualifiedName: "fresh", File: "a.py"}}}, nil
	}
	edits := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		view.refresh(ctx, edits, delay, analyze)
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
	waitForControl(t, view, "status", `{"message":"Graph refresh failed, showing the previous graph: broken.py:1:1: syntax error"}`)
	time.Sleep(2 * delay)
	if got := calls.Load(); got != 1 {
		t.Fatalf("analyze calls after two quick edits = %d, want 1", got)
	}
	if payload := decodeGraph(t, string(view.graphPayload())); payload.Version != 1 {
		t.Fatal("failed refresh replaced the graph")
	}

	edits <- struct{}{}
	waitForControl(t, view, "graph", `{"version":2}`)
	waitForControl(t, view, "status", `{"message":""}`)
	if got := calls.Load(); got != 2 {
		t.Fatalf("analyze calls = %d, want 2", got)
	}
}

func TestRefreshWaitsForEditsToStop(t *testing.T) {
	const delay = 200 * time.Millisecond
	view := testView(t, "")
	analyzed := make(chan time.Time, 4)
	analyze := func() (*callgraph.Graph, error) {
		analyzed <- time.Now()
		return &callgraph.Graph{}, nil
	}
	edits := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		view.refresh(ctx, edits, delay, analyze)
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
	view := testView(t, "")
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
		view.refresh(ctx, edits, 50*time.Millisecond, analyze)
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

func TestWatchWorktreesPublishesChanges(t *testing.T) {
	view := testView(t, "")
	answers := make(chan string)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		view.watchWorktrees(ctx, time.Millisecond, func() string { return <-answers })
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		close(answers)
		<-done
	})
	// The first check always publishes, and later checks publish changes.
	answers <- ""
	waitForControl(t, view, "mismatch", `{"path":""}`)
	sequence := controlSequence(view, "mismatch")
	answers <- ""
	answers <- "/other/worktree"
	waitForControl(t, view, "mismatch", `{"path":"/other/worktree"}`)
	if got := controlSequence(view, "mismatch"); got != sequence+1 {
		t.Fatalf("mismatch sequence = %d, want %d: an unchanged answer published", got, sequence+1)
	}
}

func TestWatchAppendsEventsAndSignalsSourceEdits(t *testing.T) {
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

	live := &liveServer{view: testView(t, root)}
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

	events := waitForLog(t, live.view, 2)
	if events[0].Type != "prompt" || events[0].Text != "start" || events[1].Type != "edit" || events[1].Path != "notes.md" || events[1].NodeIDs != nil {
		t.Fatalf("events = %+v, want the request, then a file-level edit of notes.md", events)
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
	events = waitForLog(t, live.view, 3)
	if read := events[2]; read.Type != "read" || read.Path != "app.py" || !slices.Equal(read.NodeIDs, []string{activity.Key("app.py", "app.run")}) {
		t.Fatalf("read event = %+v, want a read of app.run", read)
	}
	// The watcher handled the notes edit and the read without a source edit.
	select {
	case <-edits:
		t.Fatal("edits to non-source files or reads signaled a refresh")
	default:
	}

	transcripttest.AppendTranscript(t, session, transcripttest.MustJSON(t, transcripttest.ToolUseLine("e2", "Edit"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("e2", map[string]any{"filePath": source, "structuredPatch": patch}))+"\n")
	events = waitForLog(t, live.view, 4)
	if edit := events[3]; edit.Type != "edit" || !slices.Equal(edit.NodeIDs, []string{activity.Key("app.py", "app.run")}) || !reflect.DeepEqual(edit.Hunks, patch) {
		t.Fatalf("edit event = %+v, want an edit of app.run with its hunks", edit)
	}
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
	waitForLog(t, live.view, 6)
	live.setPaused(true)
	if live.hold("s2") == nil {
		t.Fatal("gate does not hold a call of a session that started after the first scan")
	}
	live.release()
	live.setPaused(false)
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
	transcriptPath := filepath.Join(config, "projects", transcript.EncodeProjectPath(root), "s1.jsonl")
	transcripttest.WriteTranscript(t, transcriptPath, transcripttest.SessionStart(root), transcripttest.ToolUseLine("r1", "Read"), transcripttest.ToolResultLine("r1", transcripttest.ReadResult(filepath.Join(root, "app.py"), 1, 2)))
	sessions := t.TempDir()

	url, result := startServe(t, link, sessions)
	payload := decodeGraph(t, getBody(t, url+"/api/graph", "application/json"))
	// An agent reads and edits tests, so the live graph includes test files.
	if payload.Root != root || !slices.ContainsFunc(payload.Nodes, func(node graphNode) bool { return node.Label == "test_run" }) {
		t.Fatalf("graph = %+v, want the real root and the callable in the test file", payload)
	}
	events := openEvents(t, url+"/events?view="+payload.View, "")
	waitForStreamEvent(t, events, func(event serverEvent) bool {
		return event.name == "log" && strings.Contains(event.data, `"type":"read"`) && strings.Contains(event.data, `"nodeIds":["app.py\u0000app.run"]`)
	}, "no read event for app.run")

	// A source edit makes the server analyze root again.
	langtest.WriteFixture(t, root, "app.py", "def run():\n    return added()\n\ndef added():\n    return 1\n")
	patch := []transcript.Hunk{{OldStart: 2, OldLines: 1, NewStart: 2, NewLines: 4, Lines: []string{"-    return 1", "+    return added()", "+", "+def added():", "+    return 1"}}}
	transcripttest.AppendTranscript(t, transcriptPath, transcripttest.MustJSON(t, transcripttest.ToolUseLine("e1", "Edit"))+"\n"+transcripttest.MustJSON(t, transcripttest.ToolResultLine("e1", map[string]any{"filePath": filepath.Join(root, "app.py"), "structuredPatch": patch}))+"\n")
	waitForStreamEvent(t, events, func(event serverEvent) bool {
		return event.name == "graph" && event.data == `{"version":2}`
	}, "no graph event after the source edit")
	refreshed := decodeGraph(t, getBody(t, url+"/api/graph", "application/json"))
	if !slices.ContainsFunc(refreshed.Nodes, func(node graphNode) bool { return node.Label == "added" }) {
		t.Fatal("refreshed graph does not contain the added callable")
	}
	// The session log file holds what the page streamed.
	data, err := os.ReadFile(filepath.Join(sessions, payload.View+".jsonl"))
	if err != nil || !strings.Contains(string(data), `"type":"edit"`) {
		t.Fatalf("session log = %s, %v, want the edit", data, err)
	}

	// The open event stream must not stop the shutdown.
	stopServe(t, result)
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

	url, result := startServe(t, "", t.TempDir())
	empty := getBody(t, url+"/api/graph", "application/json")
	if !strings.Contains(empty, `"root":"","version":1,"nodes":[],"edges":[]`) {
		t.Fatalf("graph without ROOT = %s, want no root and empty lists", empty)
	}
	view := decodeGraph(t, empty).View
	var roots struct {
		Current string
		Choices []rootChoice
	}
	if err := json.Unmarshal([]byte(getBody(t, url+"/api/roots", "application/json")), &roots); err != nil {
		t.Fatal(err)
	}
	if roots.Current != "" || !slices.ContainsFunc(roots.Choices, func(choice rootChoice) bool { return choice.Path == root && !choice.Session.IsZero() }) {
		t.Fatalf("roots = %+v, want no current folder and the session folder %s", roots, root)
	}
	events := openEvents(t, url+"/events?view="+view, "")
	opened := decodeGraph(t, postOpen(t, url, transcripttest.MustJSON(t, map[string]string{"path": root}), http.StatusOK, `"root":`+transcripttest.MustJSON(t, root)))
	waitForStreamEvent(t, events, func(event serverEvent) bool { return event.name == "reset" }, "the page of no folder got no reset event")
	if opened.View == view || len(opened.Nodes) != 1 {
		t.Fatalf("opened graph = %+v, want a new view with the callable", opened)
	}
	// The workers of the opened folder follow its session.
	events = openEvents(t, url+"/events?view="+opened.View, "")
	waitForStreamEvent(t, events, func(event serverEvent) bool {
		return event.name == "log" && strings.Contains(event.data, `"type":"read","agent":"main","path":"app.py"`)
	}, "no read event from the opened folder")
	stopServe(t, result)
}

func TestServeOpenFolderShowsTheNewFolder(t *testing.T) {
	folder, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := testView(t, "/first")
	var stopped atomic.Bool
	first.cancel = func() { stopped.Store(true) }
	first.setSessions([]string{"s1"})
	live := &liveServer{gateLimit: 5 * time.Second, view: first}
	live.open = func(root string) (*rootView, error) {
		view := testView(t, root)
		view.setSessions([]string{"s2"})
		_, err := view.setGraph(&callgraph.Graph{Nodes: []callgraph.Function{{ID: "b", Name: "beta", QualifiedName: "b.beta", File: "b.py"}}})
		return view, err
	}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)
	t.Cleanup(func() { live.setPaused(false) })

	// A held call of the first folder waits through the switch.
	postControl(t, server.URL+"/api/pause", "", http.StatusNoContent)
	held := make(chan string, 1)
	go func() { held <- postGate(t, server.URL, `{"session_id":"s1"}`) }()
	waitForControl(t, first, "pause", `{"paused":true,"waiting":1}`)
	events := openEvents(t, server.URL+"/events?view="+first.log.Name(), "")
	payload := decodeGraph(t, postOpen(t, server.URL, transcripttest.MustJSON(t, map[string]string{"path": folder}), http.StatusOK, `"label":"beta"`))
	waitForStreamEvent(t, events, func(event serverEvent) bool { return event.name == "reset" }, "the page of the first folder got no reset event")
	if !stopped.Load() {
		t.Fatal("the workers of the first folder did not stop")
	}
	if current := decodeGraph(t, getBody(t, server.URL+"/api/graph", "application/json")); current.View != payload.View || current.Root != folder {
		t.Fatalf("graph after the switch = %+v, want the new folder", current)
	}
	if roots := getBody(t, server.URL+"/api/roots", "application/json"); !strings.HasPrefix(roots, `{"current":`+transcripttest.MustJSON(t, folder)+`,"choices":[`) {
		t.Fatalf("roots = %s, want the new folder as the current folder", roots)
	}
	// A page of the first folder that connects later resets.
	reset := openEvents(t, server.URL+"/events?view="+first.log.Name(), "")
	expectEvent(t, reset, serverEvent{name: "reset", data: "{}"})

	// The new view has the pause state, and the gate holds only the sessions
	// of the new folder.
	waitForControl(t, live.current(), "pause", `{"paused":true,"waiting":1}`)
	if body := postGate(t, server.URL, `{"session_id":"s1"}`); body != "{}" {
		t.Fatalf("gate for a session of the first folder = %q, want {}", body)
	}
	select {
	case body := <-held:
		t.Fatalf("held call answered %q before resume", body)
	case <-time.After(100 * time.Millisecond):
	}
	postControl(t, server.URL+"/api/resume", "", http.StatusNoContent)
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
	first := testView(t, "/first")
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

	if live.current() != first || len(first.controlsSince(0)) != 0 {
		t.Fatal("a folder that did not open replaced the current folder or published events")
	}
}

func TestSessionRoutes(t *testing.T) {
	sessions := t.TempDir()
	old := session.NewLog(sessions, "20260101-000000-a", "/repo", "main")
	old.Append([]session.Event{{At: testTime, Type: "title", Text: "Earlier"}})
	other := session.NewLog(sessions, "20260101-000000-b", "/other", "main")
	other.Append([]session.Event{{At: testTime, Type: "prompt"}})
	for _, l := range []*session.Log{old, other} {
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
	}
	// The current log cannot write its file, and the route serves it from memory.
	blocked := filepath.Join(t.TempDir(), "file")
	langtest.WriteFixture(t, filepath.Dir(blocked), "file", "")
	view := newView("/repo", "main", blocked)
	view.appendEvents([]session.Event{{At: testTime, Type: "plan", Text: "now"}})
	live := &liveServer{view: view, sessions: sessions}
	server := httptest.NewServer(live.routes())
	t.Cleanup(server.Close)

	var list struct {
		Current  string
		Sessions []session.Summary
	}
	if err := json.Unmarshal([]byte(getBody(t, server.URL+"/api/sessions", "application/json")), &list); err != nil {
		t.Fatal(err)
	}
	if list.Current != view.log.Name() || len(list.Sessions) != 1 || list.Sessions[0].Name != "20260101-000000-a" || list.Sessions[0].Title != "Earlier" {
		t.Fatalf("sessions = %+v, want the current name and the earlier log of /repo", list)
	}
	if body := getBody(t, server.URL+"/api/sessions/20260101-000000-a", "application/x-ndjson"); !strings.Contains(body, `"text":"Earlier"`) {
		t.Fatalf("earlier log = %s", body)
	}
	if body := getBody(t, server.URL+"/api/sessions/"+view.log.Name(), "application/x-ndjson"); !strings.Contains(body, `"text":"now"`) || strings.Count(body, "\n") != 2 {
		t.Fatalf("current log = %q, want the header and the plan", body)
	}
	for _, name := range []string{"20261231-000000-z", "..%2Fx"} {
		response, err := http.Get(server.URL + "/api/sessions/" + name)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("GET /api/sessions/%s = %d, want 404", name, response.StatusCode)
		}
	}
}

func TestWebHandler(t *testing.T) {
	built := webHandler(fstest.MapFS{"index.html": {Data: []byte("<p>app</p>")}, "assets/app-1.js": {Data: []byte("js")}})
	for path, want := range map[string]string{"/": "<p>app</p>", "/assets/app-1.js": "js"} {
		recorder := httptest.NewRecorder()
		built.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		cache := recorder.Header().Get("Cache-Control")
		if recorder.Code != http.StatusOK || recorder.Body.String() != want || (path == "/") != (cache == "no-store") {
			t.Errorf("GET %s = %d %q, Cache-Control %q", path, recorder.Code, recorder.Body.String(), cache)
		}
	}
	recorder := httptest.NewRecorder()
	webHandler(fstest.MapFS{}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "npm --prefix web run build") {
		t.Fatalf("unbuilt page = %d %q", recorder.Code, recorder.Body.String())
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
	view, err := openRoot(context.Background(), &workers, pythonAnalyzer(t), projects, t.TempDir(), root)
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
	go func() {
		result <- Run(Config{Analyzer: pythonAnalyzer(t), Root: root, Port: port, Sessions: t.TempDir()})
	}()
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

// testView returns a view of root whose log writes into a temporary directory.
func testView(t *testing.T, root string) *rootView {
	t.Helper()
	view := newView(root, "", t.TempDir())
	if _, err := view.setGraph(&callgraph.Graph{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = view.log.Close() })
	return view
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

// startServe runs Run for root on a free port and returns the server URL and
// the channel that receives the result of Run.
func startServe(t *testing.T, root string, sessions string) (string, <-chan error) {
	t.Helper()
	logs := &lockedBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	result := make(chan error, 1)
	go func() { result <- Run(Config{Analyzer: pythonAnalyzer(t), Root: root, Sessions: sessions}) }()
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

// stopServe sends SIGINT to the test process and waits for Run to return nil.
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

// waitForControl waits until the latest control event name has the data want.
func waitForControl(t *testing.T, view *rootView, name string, want string) {
	t.Helper()
	notify, unsubscribe := view.subscribe()
	defer unsubscribe()
	deadline := time.After(10 * time.Second)
	for {
		view.mu.RLock()
		latest := string(view.controls[name].data)
		view.mu.RUnlock()
		if latest == want {
			return
		}
		select {
		case <-notify:
		case <-deadline:
			t.Fatalf("latest %s event = %s, want %s", name, latest, want)
		}
	}
}

func controlSequence(view *rootView, name string) int {
	view.mu.RLock()
	defer view.mu.RUnlock()
	return view.controls[name].sequence
}

// waitForLog waits until the log holds count events after its header and
// returns them.
func waitForLog(t *testing.T, view *rootView, count int) []session.Event {
	t.Helper()
	notify, unsubscribe := view.subscribe()
	defer unsubscribe()
	deadline := time.After(10 * time.Second)
	for {
		var events []session.Event
		for _, line := range view.log.Lines(1) {
			var event session.Event
			if err := json.Unmarshal(line, &event); err != nil {
				t.Fatal(err)
			}
			events = append(events, event)
		}
		if len(events) >= count {
			return events
		}
		select {
		case <-notify:
		case <-deadline:
			t.Fatalf("got %d events, want %d: %+v", len(events), count, events)
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

// postOpen asks the server to open a folder, checks the status code and that
// the answer contains text, and returns the answer.
func postOpen(t *testing.T, url string, body string, status int, text string) string {
	t.Helper()
	response, err := http.Post(url+"/api/root", "application/json", strings.NewReader(body))
	if err != nil {
		t.Error(err)
		return ""
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	if err != nil {
		t.Error(err)
	}
	if response.StatusCode != status || !strings.Contains(string(answer), text) {
		t.Errorf("POST /api/root %s = %d %q, want %d with %q", body, response.StatusCode, answer, status, text)
	}
	return string(answer)
}

func expectSignal(t *testing.T, signals <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signals:
	case <-time.After(5 * time.Second):
		t.Fatal(message)
	}
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
	events := make(chan serverEvent, 64)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(response.Body)
		scanner.Buffer(nil, 16<<20)
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

// expectLogEvent reads the next event, checks that it is a log line with ID
// id, and returns its data.
func expectLogEvent(t *testing.T, events <-chan serverEvent, id string) string {
	t.Helper()
	select {
	case got, ok := <-events:
		if !ok || got.name != "log" || got.id != id {
			t.Fatalf("event = %+v, %t, want log line %s", got, ok, id)
		}
		return got.data
	case <-time.After(5 * time.Second):
		t.Fatalf("no event, want log line %s", id)
	}
	return ""
}

// waitForStreamEvent reads events until match accepts one.
func waitForStreamEvent(t *testing.T, events <-chan serverEvent, match func(serverEvent) bool, message string) {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatal(message + ": stream ended")
			}
			if match(event) {
				return
			}
		case <-deadline:
			t.Fatal(message)
		}
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

func decodeGraph(t *testing.T, body string) graphPayload {
	t.Helper()
	var payload graphPayload
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("graph payload %s: %v", body, err)
	}
	return payload
}
