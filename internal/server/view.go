package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/derekdshaw/agent-work-visualizer/internal/activity"
	"github.com/derekdshaw/agent-work-visualizer/internal/analyzer"
	"github.com/derekdshaw/agent-work-visualizer/internal/callgraph"
	"github.com/derekdshaw/agent-work-visualizer/internal/session"
	"github.com/derekdshaw/agent-work-visualizer/internal/transcript"
)

const (
	pollInterval   = 500 * time.Millisecond
	scanEveryPolls = 4
	refreshDelay   = 2 * time.Second
	// mismatchInterval is how often the server looks for agent work in other
	// worktrees. It reads the start of the newest transcript of every project.
	mismatchInterval = 10 * time.Second
	// mismatchAge is how recent a session in another worktree must be to count.
	mismatchAge = 10 * time.Minute
)

// rootView holds the graph of one folder, its session log, the latest control
// events, and the sessions that its transcript watcher follows. The log name
// identifies the view to the pages, so a page of another view or of an earlier
// server process starts over. root is "" for the view of no folder. The
// workers of a view that the server no longer shows can still change it, but
// no page reads it.
type rootView struct {
	root   string
	branch string
	log    *session.Log
	cancel context.CancelFunc

	mu       sync.RWMutex
	version  int
	payload  []byte
	sessions map[string]bool
	controls map[string]control
	sequence int
	waiters  map[chan struct{}]bool
}

// control is the latest event of one name that is not part of the session:
// "pause", "graph", "status", "mismatch", or "reset". sequence orders the
// controls of a view.
type control struct {
	name     string
	data     []byte
	sequence int
}

func newView(root string, branch string, sessions string) *rootView {
	return &rootView{
		root:     root,
		branch:   branch,
		log:      session.NewLog(sessions, session.NewName(time.Now()), root, branch),
		controls: make(map[string]control),
		waiters:  make(map[chan struct{}]bool),
	}
}

// emptyView returns the view of no folder, with an empty graph.
func emptyView(sessions string) *rootView {
	view := newView("", "", sessions)
	if _, err := view.setGraph(&callgraph.Graph{}); err != nil {
		// An empty graph always encodes.
		panic(err)
	}
	return view
}

// openRoot analyzes root and returns its view with a running transcript
// watcher, refresh loop, and worktree check. The workers stop when ctx ends or
// the view stops. workers counts them.
func openRoot(ctx context.Context, workers *sync.WaitGroup, a *analyzer.Analyzer, projects string, sessions string, root string) (*rootView, error) {
	// An agent reads and edits tests, so the live graph includes test files.
	// Without them, the viewer has no node to mark for a test callable.
	options := analyzer.Options{IncludeTests: true}
	graph, err := a.Analyze(root, options)
	if err != nil {
		return nil, err
	}
	view := newView(root, gitBranch(root), sessions)
	if _, err := view.setGraph(graph); err != nil {
		return nil, err
	}
	ctx, view.cancel = context.WithCancel(ctx)
	edits := make(chan struct{}, 1)
	watcher := transcript.NewWatcher(projects, root)
	// The gate holds only the sessions that the view follows. The first scan
	// runs before the server shows the view, so a pause holds them at once.
	watcher.Scan()
	view.setSessions(watcher.SessionIDs())
	mapper := activity.NewMapper(root, a)
	home, worktrees := worktreeRoots(root)
	workers.Add(3)
	go func() {
		defer workers.Done()
		view.watch(ctx, a, watcher, mapper, edits)
	}()
	go func() {
		defer workers.Done()
		view.refresh(ctx, edits, refreshDelay, func() (*callgraph.Graph, error) { return a.Analyze(root, options) })
	}()
	go func() {
		defer workers.Done()
		view.watchWorktrees(ctx, mismatchInterval, func() string {
			return agentElsewhere(projects, home, worktrees, time.Now().Add(-mismatchAge))
		})
	}()
	return view, nil
}

// stop stops the workers of the view and closes its log.
func (v *rootView) stop() {
	if v.cancel != nil {
		v.cancel()
	}
	if err := v.log.Close(); err != nil {
		log.Printf("Closing session log: %v", err)
	}
}

// setGraph encodes the graph payload for graph under the next version number
// and returns that number.
func (v *rootView) setGraph(graph *callgraph.Graph) (int, error) {
	nodes, edges := slimGraph(graph)
	v.mu.Lock()
	defer v.mu.Unlock()
	version := v.version + 1
	payload, err := json.Marshal(graphPayload{
		View: v.log.Name(), Root: v.root, Branch: v.branch, Version: version, Nodes: nodes, Edges: edges,
	})
	if err != nil {
		return 0, fmt.Errorf("encode graph payload: %w", err)
	}
	v.version, v.payload = version, payload
	return version, nil
}

// graphPayload returns the encoded graph payload.
func (v *rootView) graphPayload() []byte {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.payload
}

// setSessions replaces the set of session IDs that the view follows.
func (v *rootView) setSessions(sessions []string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.sessions = make(map[string]bool, len(sessions))
	for _, session := range sessions {
		v.sessions[session] = true
	}
}

// follows reports whether the view follows session.
func (v *rootView) follows(session string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.sessions[session]
}

// setControl replaces the control event name and wakes the event streams.
func (v *rootView) setControl(name string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("Encoding %s event: %v", name, err)
		return
	}
	v.mu.Lock()
	v.sequence++
	v.controls[name] = control{name: name, data: data, sequence: v.sequence}
	v.mu.Unlock()
	v.wake()
}

// controlsSince returns the controls that changed after sequence, oldest first.
func (v *rootView) controlsSince(sequence int) []control {
	v.mu.RLock()
	defer v.mu.RUnlock()
	var changed []control
	for _, control := range v.controls {
		if control.sequence > sequence {
			changed = append(changed, control)
		}
	}
	slices.SortFunc(changed, func(a, b control) int { return a.sequence - b.sequence })
	return changed
}

// appendEvents adds events to the session log and wakes the event streams.
func (v *rootView) appendEvents(events []session.Event) {
	if len(events) == 0 {
		return
	}
	v.log.Append(events)
	v.wake()
}

// subscribe returns a channel that receives a value after the log or a
// control changes, and a function that ends the subscription.
func (v *rootView) subscribe() (<-chan struct{}, func()) {
	notify := make(chan struct{}, 1)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.waiters[notify] = true
	return notify, func() {
		v.mu.Lock()
		defer v.mu.Unlock()
		delete(v.waiters, notify)
	}
}

func (v *rootView) wake() {
	v.mu.RLock()
	defer v.mu.RUnlock()
	for notify := range v.waiters {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
}

// serveEvents streams the session log as server-sent "log" events, then new
// lines until the client disconnects. A line's event ID is its line number in
// the log, so a reconnecting browser resumes after its Last-Event-ID. Control
// events have no ID; a stream starts with the latest of each. The page names
// its view in the view query parameter. Another view, or a Last-Event-ID
// beyond the log, gets a single "reset" event, because the page state came from
// another folder or an earlier server process.
func (v *rootView) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	next := 0
	stale := r.URL.Query().Get("view") != v.log.Name()
	if last := r.Header.Get("Last-Event-ID"); last != "" && !stale {
		line, err := strconv.Atoi(last)
		stale = err != nil || line < 0 || line > v.log.Len()
		next = line
	}
	if stale {
		_, _ = fmt.Fprint(w, "event: reset\ndata: {}\n\n")
		flusher.Flush()
		return
	}
	notify, unsubscribe := v.subscribe()
	defer unsubscribe()
	sequence := 0
	for {
		for _, line := range v.log.Lines(next) {
			next++
			if _, err := fmt.Fprintf(w, "id: %d\nevent: log\ndata: %s\n\n", next, line); err != nil {
				return
			}
		}
		for _, control := range v.controlsSince(sequence) {
			sequence = control.sequence
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", control.name, control.data); err != nil {
				return
			}
		}
		flusher.Flush()
		select {
		case <-r.Context().Done():
			return
		case <-notify:
		}
	}
}

// watch polls transcripts and appends their events to the session log. After
// a poll that found an edit to a source file, it signals edits without
// blocking.
func (v *rootView) watch(ctx context.Context, a *analyzer.Analyzer, watcher *transcript.Watcher, mapper *activity.Mapper, edits chan<- struct{}) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for polls := 0; ; polls++ {
		if polls%scanEveryPolls == 0 {
			watcher.Scan()
			v.setSessions(watcher.SessionIDs())
		}
		toolEvents := watcher.Poll()
		var events []session.Event
		for _, note := range watcher.TakeNotes() {
			if event, ok := session.FromNote(note); ok {
				events = append(events, event)
			}
		}
		edited := false
		for _, mapped := range mapper.Map(toolEvents) {
			events = append(events, session.FromActivity(mapped))
			if mapped.Kind == "edit" && a.Supports(mapped.File) {
				edited = true
			}
		}
		v.appendEvents(events)
		if edited {
			select {
			case edits <- struct{}{}:
			default:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// refresh reanalyzes the root after edits stop for delay. A failed analysis
// keeps the current graph and publishes a status event.
func (v *rootView) refresh(ctx context.Context, edits <-chan struct{}, delay time.Duration, analyze func() (*callgraph.Graph, error)) {
	timer := time.NewTimer(delay)
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-edits:
			timer.Reset(delay)
		case <-timer.C:
			graph, err := analyze()
			if err == nil {
				var version int
				if version, err = v.setGraph(graph); err == nil {
					v.setControl("status", map[string]string{"message": ""})
					v.setControl("graph", map[string]int{"version": version})
					continue
				}
			}
			log.Printf("Keeping the previous graph: %v", err)
			v.setControl("status", map[string]string{"message": "Graph refresh failed, showing the previous graph: " + err.Error()})
		}
	}
}

// watchWorktrees publishes a "mismatch" event whenever elsewhere returns a
// different folder: the folder of recent agent work in another worktree, or
// "" for none.
func (v *rootView) watchWorktrees(ctx context.Context, interval time.Duration, elsewhere func() string) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := ""
	for first := true; ; first = false {
		if path := elsewhere(); first || path != last {
			last = path
			v.setControl("mismatch", map[string]string{"path": path})
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
