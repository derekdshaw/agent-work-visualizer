package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultServePort = 7777
	pollInterval     = 500 * time.Millisecond
	scanEveryPolls   = 4
	refreshDelay     = 2 * time.Second
	// gateHoldLimit is the longest time that the gate holds a tool call. It
	// is shorter than the curl and hook timeouts of the PreToolUse hook, so
	// the agent receives the denial instead of a timeout.
	gateHoldLimit = 50 * time.Minute
	// gateDenial is the PreToolUse hook output that denies a tool call.
	gateDenial = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"The user paused agent work from the live view. Stop and wait for the user."}}`
)

// runServe analyzes ROOT, follows Claude Code session transcripts for agent
// work under ROOT, and serves a live viewer on 127.0.0.1 until it receives an
// interrupt. Without ROOT, the viewer starts with no folder, and the user picks
// one in the page. The server listens on loopback only because the graph
// contains the source of every callable.
func runServe(arguments []string) error {
	root, port, err := parseServeArguments(arguments)
	if err != nil {
		return err
	}
	projects, err := claudeProjectsDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	// Deferred calls run in reverse order. cancel ends the workers of each
	// folder, and then runServe waits for them.
	defer workers.Wait()
	defer cancel()
	live := &liveServer{projects: projects}
	live.open = func(root string) (*rootView, error) { return openRoot(ctx, &workers, projects, root) }
	if root == "" {
		live.view, err = emptyView()
	} else if root, err = resolveRoot(root); err == nil {
		live.view, err = live.open(root)
	}
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	// The startup analysis runs before the server catches signals, so an
	// interrupt during the analysis stops the process at once.
	signals, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Request contexts derive from ctx, so open event streams end at shutdown.
	server := &http.Server{Handler: live.routes(), BaseContext: func(net.Listener) context.Context { return ctx }}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-signals.Done()
		// Shutdown waits for a folder that the server analyzes, and an analysis
		// does not stop. stop restores the default action, so a second
		// interrupt stops the process at once.
		stop()
		cancel()
		if err := server.Shutdown(context.Background()); err != nil {
			log.Printf("Shutting down server: %v", err)
		}
	}()
	log.Printf("Live view at http://%s", listener.Addr())
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve: %w", err)
}

// parseServeArguments returns ROOT, or "" if the arguments name no ROOT, and
// the port.
func parseServeArguments(arguments []string) (root string, port int, err error) {
	usage := errors.New("usage: code-graph-visualizer serve [ROOT] [--port N]")
	port = defaultServePort
	for index := 0; index < len(arguments); index++ {
		switch arguments[index] {
		case "--port":
			index++
			if index >= len(arguments) {
				return "", 0, errors.New("--port requires a number")
			}
			port, err = strconv.Atoi(arguments[index])
			if err != nil || port < 0 || port > 65535 {
				return "", 0, fmt.Errorf("invalid port %q", arguments[index])
			}
		case "--include-tests":
			// serve always includes tests. The option stays valid for
			// commands that already pass it.
		case "-h", "--help":
			return "", 0, usage
		default:
			if len(arguments[index]) > 0 && arguments[index][0] == '-' {
				return "", 0, fmt.Errorf("unknown option %s", arguments[index])
			}
			if root != "" {
				return "", 0, errors.New("at most one ROOT is allowed")
			}
			root = arguments[index]
		}
	}
	return root, port, nil
}

// liveServer shows the view of one folder at a time and opens another folder
// on request. The pause gate belongs to the server, so Resume also releases
// the calls that the gate holds for a folder that the server no longer shows.
// gateLimit is the gate hold limit, and zero means gateHoldLimit. open
// analyzes a folder and returns its view with running workers. projects is the
// directory of the Claude Code session transcripts.
type liveServer struct {
	gate      pauseGate
	gateLimit time.Duration
	open      func(root string) (*rootView, error)
	projects  string
	// opening is true while the server opens a folder.
	opening atomic.Bool
	mu      sync.RWMutex
	view    *rootView
}

// rootView holds the graph of one folder, the event history that the viewer
// streams, and the sessions that its transcript watcher follows. instance
// identifies the view in event IDs, so a page of another view or of an earlier
// server process reloads. root is "" for the view of no folder. The workers of
// a view that the server no longer shows can still change it, but no page
// reads it.
type rootView struct {
	root     string
	instance string
	hub      hub
	cancel   context.CancelFunc
	mu       sync.RWMutex
	version  int
	page     []byte
	graph    []byte
	sessions map[string]bool
}

// pauseGate records whether the user paused agent work and the number of tool
// calls that the gate holds. resumed closes when the user resumes.
type pauseGate struct {
	mu      sync.Mutex
	paused  bool
	resumed chan struct{}
	waiting int
}

// pauseState is the data of a "pause" event.
type pauseState struct {
	Paused  bool `json:"paused"`
	Waiting int  `json:"waiting"`
}

// newInstance returns an instance ID for a new view.
func newInstance() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

// emptyView returns the view of no folder, with an empty graph.
func emptyView() (*rootView, error) {
	view := &rootView{instance: newInstance()}
	// The viewer reads nodes, edges, and unresolved as JSON arrays. A nil slice encodes as null.
	if _, err := view.setGraph(&Graph{Nodes: []Function{}, Edges: []Edge{}, Unresolved: []UnresolvedCall{}}); err != nil {
		return nil, err
	}
	return view, nil
}

// openRoot analyzes root and returns its view with a running transcript
// watcher and refresh loop. The workers stop when ctx ends or the view stops.
// workers counts them.
func openRoot(ctx context.Context, workers *sync.WaitGroup, projects string, root string) (*rootView, error) {
	// An agent reads and edits tests, so the live graph includes test files.
	// Without them, the viewer has no node to open for a test callable.
	options := Options{IncludeTests: true, bazel: &bazelCache{}}
	graph, err := Analyze(root, options)
	if err != nil {
		return nil, err
	}
	view := &rootView{root: root, instance: newInstance()}
	if _, err := view.setGraph(graph); err != nil {
		return nil, err
	}
	ctx, view.cancel = context.WithCancel(ctx)
	edits := make(chan struct{}, 1)
	watcher := newTranscriptWatcher(projects, root)
	// The gate holds only the sessions that the view follows. The first scan
	// runs before the server shows the view, so a pause holds them at once.
	watcher.scan()
	view.setSessions(watcher.sessionIDs())
	mapper := newActivityMapper(root)
	workers.Add(2)
	go func() {
		defer workers.Done()
		view.watch(ctx, watcher, mapper, edits)
	}()
	go func() {
		defer workers.Done()
		view.refresh(ctx, edits, refreshDelay, func() (*Graph, error) { return Analyze(root, options) })
	}()
	return view, nil
}

// resolveRoot returns the absolute path of the folder path. A leading "~"
// names the home directory. Claude Code records the real path of its working
// directory, so the result contains no symbolic links.
func resolveRoot(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("find home directory: %w", err)
		}
		path = filepath.Join(home, path[1:])
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("resolve root: %s is not a folder", root)
	}
	return root, nil
}

// errOpening means that the server already opens another folder.
var errOpening = errors.New("the server already opens another folder")

// openFolder analyzes the folder path and shows it in place of the current
// folder. The pages of the previous folder receive a "reload" event, and its
// workers stop. If the analysis fails, the server keeps the current folder.
func (s *liveServer) openFolder(path string) error {
	if !s.opening.CompareAndSwap(false, true) {
		return errOpening
	}
	defer s.opening.Store(false)
	root, err := resolveRoot(path)
	if err != nil {
		return err
	}
	log.Printf("Opening %s", root)
	view, err := s.open(root)
	if err != nil {
		return err
	}
	s.mu.Lock()
	previous := s.view
	s.view = view
	s.mu.Unlock()
	// The new history contains the pause state, so the page shows it.
	s.gate.mu.Lock()
	s.publishPauseLocked()
	s.gate.mu.Unlock()
	previous.publish("reload", struct{}{})
	previous.stop()
	return nil
}

// current returns the view that the server shows.
func (s *liveServer) current() *rootView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.view
}

// stop stops the workers of the view.
func (v *rootView) stop() {
	if v.cancel != nil {
		v.cancel()
	}
}

// setGraph renders the viewer page and graph payload for graph under the next
// version number and returns that number.
func (v *rootView) setGraph(graph *Graph) (int, error) {
	data, err := marshalGraph(graph)
	if err != nil {
		return 0, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	version := v.version + 1
	// json.Marshal escapes "<", so a root path cannot end the script element of
	// the page.
	live, err := json.Marshal(struct {
		Version  int    `json:"version"`
		Instance string `json:"instance"`
		Root     string `json:"root"`
	}{version, v.instance, v.root})
	if err != nil {
		return 0, err
	}
	page, err := renderViewer(graph, data, string(live))
	if err != nil {
		return 0, err
	}
	payload, err := json.Marshal(struct {
		Version int             `json:"version"`
		Graph   json.RawMessage `json:"graph"`
	}{version, data})
	if err != nil {
		return 0, fmt.Errorf("encode graph payload: %w", err)
	}
	v.version, v.page, v.graph = version, page, payload
	return version, nil
}

// content returns the viewer page and the graph payload.
func (v *rootView) content() (page []byte, graph []byte) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.page, v.graph
}

// routes returns the live server handler. It rejects a request whose Host
// header names another host. This stops a web page that rebinds its own domain
// name to 127.0.0.1 from reading the graph.
func (s *liveServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		page, _ := s.current().content()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	})
	mux.HandleFunc("GET /graph", func(w http.ResponseWriter, r *http.Request) {
		_, graph := s.current().content()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(graph)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) { s.current().serveEvents(w, r) })
	mux.HandleFunc("GET /roots", s.serveRoots)
	mux.HandleFunc("POST /root", s.serveOpen)
	mux.HandleFunc("POST /gate", s.serveGate)
	mux.HandleFunc("POST /pause", func(w http.ResponseWriter, r *http.Request) {
		s.setPaused(true)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /resume", func(w http.ResponseWriter, r *http.Request) {
		s.setPaused(false)
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		// A browser sends Origin with a POST. This stops a form on another
		// site from pausing or resuming agent work or from opening a folder.
		if origin := r.Header.Get("Origin"); r.Method != http.MethodGet && origin != "" && origin != "http://"+r.Host {
			http.Error(w, "cross-origin request", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// serveRoots answers with the current folder, or "" for no folder, and the
// folders that the folder picker offers. The working directory of the server
// takes the place of no folder when the server looks for git worktrees.
func (s *liveServer) serveRoots(w http.ResponseWriter, r *http.Request) {
	current := s.current().root
	base := current
	if base == "" {
		base, _ = os.Getwd()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Current string       `json:"current"`
		Choices []rootChoice `json:"choices"`
	}{current, rootChoices(s.projects, base, time.Now().Add(-recentSessionAge))})
}

// serveOpen opens the folder that the JSON body names in its path field. It
// answers 204 when the server shows the folder, 409 while it opens another
// folder, and 422 with the error text when the folder does not open.
func (s *liveServer) serveOpen(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Path) == "" {
		http.Error(w, "The request names no folder.", http.StatusBadRequest)
		return
	}
	switch err := s.openFolder(strings.TrimSpace(input.Path)); {
	case errors.Is(err, errOpening):
		http.Error(w, "The server already opens another folder.", http.StatusConflict)
	case err != nil:
		log.Printf("Keeping the current folder: %v", err)
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// serveGate answers a Claude Code PreToolUse hook. While work is paused, it
// holds the request of a followed session until the user resumes or the server
// stops, and then allows the tool call. If the hold limit passes first, it
// denies the tool call. It allows all other tool calls at once.
func (s *liveServer) serveGate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SessionID string `json:"session_id"`
	}
	// A body that does not decode names no followed session, so the call runs.
	_ = json.NewDecoder(r.Body).Decode(&input)
	// The server sees a closed connection, and ends the request context, only
	// after the handler reads the body to the end.
	_, _ = io.Copy(io.Discard, r.Body)
	w.Header().Set("Content-Type", "application/json")
	resumed := s.hold(input.SessionID)
	if resumed == nil {
		_, _ = fmt.Fprint(w, "{}")
		return
	}
	defer s.release()
	limit := s.gateLimit
	if limit == 0 {
		limit = gateHoldLimit
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-resumed:
	case <-r.Context().Done():
	case <-timer.C:
		_, _ = fmt.Fprint(w, gateDenial)
		return
	}
	_, _ = fmt.Fprint(w, "{}")
}

// setPaused pauses or resumes agent work and publishes the new state.
func (s *liveServer) setPaused(paused bool) {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	if paused && !s.gate.paused {
		s.gate.resumed = make(chan struct{})
	} else if !paused && s.gate.paused {
		close(s.gate.resumed)
	}
	s.gate.paused = paused
	s.publishPauseLocked()
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

// hold returns the channel that closes when the user resumes, if the gate
// holds a tool call of session. It returns nil if the tool call can run. The
// gate holds only the sessions that the current view follows.
func (s *liveServer) hold(session string) <-chan struct{} {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	if !s.gate.paused || !s.current().follows(session) {
		return nil
	}
	s.gate.waiting++
	s.publishPauseLocked()
	return s.gate.resumed
}

// release records that the gate no longer holds a tool call.
func (s *liveServer) release() {
	s.gate.mu.Lock()
	defer s.gate.mu.Unlock()
	s.gate.waiting--
	s.publishPauseLocked()
}

// publishPauseLocked publishes the pause state to the current view. The caller
// holds s.gate.mu, so the events keep the order of the changes.
func (s *liveServer) publishPauseLocked() {
	s.current().publish("pause", pauseState{Paused: s.gate.paused, Waiting: s.gate.waiting})
}

// serveEvents streams the event history as server-sent events, then streams
// new events until the client disconnects. An event ID is the server instance
// and the 1-based position in the history, so a reconnecting browser resumes
// after its Last-Event-ID. The page also sends its instance in the instance
// query parameter, because a browser sends Last-Event-ID only after it
// receives an event. Another instance in either place gets a single "reload"
// event, because the page state came from an earlier server process.
func (v *rootView) serveEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	next := 0
	page := r.URL.Query().Get("instance")
	stale := page != "" && page != v.instance
	if last := r.Header.Get("Last-Event-ID"); last != "" && !stale {
		instance, position, _ := strings.Cut(last, "-")
		index, err := strconv.Atoi(position)
		stale = instance != v.instance || err != nil || index < 0
		next = index
	}
	if stale {
		_, _ = fmt.Fprint(w, "event: reload\ndata: {}\n\n")
		flusher.Flush()
		return
	}
	notify, unsubscribe := v.hub.subscribe()
	defer unsubscribe()
	for {
		for _, event := range v.hub.since(next) {
			next++
			if _, err := fmt.Fprintf(w, "id: %s-%d\nevent: %s\ndata: %s\n\n", v.instance, next, event.name, event.data); err != nil {
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

// watch polls transcripts and publishes task and activity events. After a
// poll that found an edit to a source file, it signals edits without blocking.
func (v *rootView) watch(ctx context.Context, watcher *transcriptWatcher, mapper *activityMapper, edits chan<- struct{}) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for polls := 0; ; polls++ {
		if polls%scanEveryPolls == 0 {
			watcher.scan()
			v.setSessions(watcher.sessionIDs())
		}
		events := watcher.poll()
		for _, note := range watcher.takeNotes() {
			v.publish("task", note)
		}
		edited := false
		for _, activity := range mapper.mapEvents(events) {
			v.publish("activity", activity)
			if activity.Kind == "edit" && sourceLanguage(activity.File) != "" {
				edited = true
			}
		}
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
func (v *rootView) refresh(ctx context.Context, edits <-chan struct{}, delay time.Duration, analyze func() (*Graph, error)) {
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
					v.publish("graph", map[string]int{"version": version})
					continue
				}
			}
			log.Printf("Keeping the previous graph: %v", err)
			v.publish("status", map[string]string{"message": "Graph refresh failed, showing the previous graph: " + err.Error()})
		}
	}
}

func (v *rootView) publish(name string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		log.Printf("Encoding %s event: %v", name, err)
		return
	}
	v.hub.publish(name, data)
}

type hubEvent struct {
	name string
	data []byte
}

// hub keeps every published event and wakes subscribers. Each subscriber
// reads the history from its own position, so a slow reader never blocks a
// publisher or loses events.
type hub struct {
	mu          sync.Mutex
	events      []hubEvent
	subscribers map[chan struct{}]bool
}

func (h *hub) publish(name string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, hubEvent{name: name, data: data})
	for notify := range h.subscribers {
		select {
		case notify <- struct{}{}:
		default:
		}
	}
}

func (h *hub) subscribe() (<-chan struct{}, func()) {
	notify := make(chan struct{}, 1)
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subscribers == nil {
		h.subscribers = make(map[chan struct{}]bool)
	}
	h.subscribers[notify] = true
	return notify, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		delete(h.subscribers, notify)
	}
}

// since returns the events from position index onward. The result shares
// storage with the history, which publish only appends to.
func (h *hub) since(index int) []hubEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	if index >= len(h.events) {
		return nil
	}
	return h.events[index:len(h.events):len(h.events)]
}
