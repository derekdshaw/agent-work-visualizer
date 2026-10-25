// Package server serves the live agent view: the call graph of a folder and
// the agent activity that Claude Code session transcripts record for it.
package server

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

	"github.com/derekdshaw/agent-work-visualizer/internal/analyzer"
	"github.com/derekdshaw/agent-work-visualizer/internal/session"
	"github.com/derekdshaw/agent-work-visualizer/internal/transcript"
)

// sessionListLimit is the number of logs that the session picker offers.
const sessionListLimit = 50

// fileSizeLimit is the largest file that the code pane opens.
const fileSizeLimit = 2 << 20

// Config configures Run. Sessions is the directory of the session logs.
type Config struct {
	Analyzer *analyzer.Analyzer
	Root     string
	Port     int
	Sessions string
}

// Run analyzes the root with the analyzer, follows Claude Code session
// transcripts for agent work under the root, records it in a session log, and
// serves the live viewer on 127.0.0.1 until it receives an interrupt. With an
// empty root, the viewer starts with no folder, and the user picks one in the
// page. The server listens on loopback only because the session logs contain
// the source that the agent changed.
func Run(config Config) error {
	projects, err := transcript.ProjectsDir()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	// Deferred calls run in reverse order. cancel ends the workers of each
	// folder, and then Run waits for them.
	defer workers.Wait()
	defer cancel()
	live := &liveServer{projects: projects, sessions: config.Sessions, web: webHandler(builtWeb())}
	live.open = func(root string) (*rootView, error) {
		return openRoot(ctx, &workers, config.Analyzer, projects, config.Sessions, root)
	}
	root := config.Root
	if root == "" {
		live.view = emptyView(config.Sessions)
	} else if root, err = resolveRoot(root); err == nil {
		live.view, err = live.open(root)
	}
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(config.Port)))
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

// liveServer shows the view of one folder at a time and opens another folder
// on request. The pause gate belongs to the server, so Resume also releases
// the calls that the gate holds for a folder that the server no longer shows.
// gateLimit is the gate hold limit, and zero means gateHoldLimit. open
// analyzes a folder and returns its view with running workers. projects is the
// directory of the Claude Code session transcripts, and sessions the directory
// of the session logs. web serves the page.
type liveServer struct {
	gate      pauseGate
	gateLimit time.Duration
	open      func(root string) (*rootView, error)
	projects  string
	sessions  string
	web       http.Handler
	// opening is true while the server opens a folder.
	opening atomic.Bool
	mu      sync.RWMutex
	view    *rootView
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
// folder. The pages of the previous folder receive a "reset" event, and its
// workers stop. If the analysis fails, the server keeps the current folder.
func (s *liveServer) openFolder(path string) (*rootView, error) {
	if !s.opening.CompareAndSwap(false, true) {
		return nil, errOpening
	}
	defer s.opening.Store(false)
	root, err := resolveRoot(path)
	if err != nil {
		return nil, err
	}
	log.Printf("Opening %s", root)
	view, err := s.open(root)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	previous := s.view
	s.view = view
	s.mu.Unlock()
	// The new view sends the pause state to its pages.
	s.gate.mu.Lock()
	s.publishPauseLocked()
	s.gate.mu.Unlock()
	previous.setControl("reset", struct{}{})
	previous.stop()
	return view, nil
}

// current returns the view that the server shows.
func (s *liveServer) current() *rootView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.view
}

// routes returns the live server handler. It rejects a request whose Host
// header names another host. This stops a web page that rebinds its own domain
// name to 127.0.0.1 from reading the graph.
func (s *liveServer) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/graph", func(w http.ResponseWriter, r *http.Request) {
		writeJSONBytes(w, s.current().graphPayload())
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) { s.current().serveEvents(w, r) })
	mux.HandleFunc("GET /api/roots", s.serveRoots)
	mux.HandleFunc("GET /api/sessions", s.serveSessions)
	mux.HandleFunc("GET /api/sessions/{name}", s.serveSession)
	mux.HandleFunc("GET /api/file", s.serveFile)
	mux.HandleFunc("POST /api/root", s.serveOpen)
	mux.HandleFunc("POST /api/pause", func(w http.ResponseWriter, r *http.Request) {
		s.setPaused(true)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/resume", func(w http.ResponseWriter, r *http.Request) {
		s.setPaused(false)
		w.WriteHeader(http.StatusNoContent)
	})
	// The PreToolUse hook in the README posts here.
	mux.HandleFunc("POST /gate", s.serveGate)
	if s.web != nil {
		mux.Handle("GET /", s.web)
	}
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

func writeJSONBytes(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(value)
}

// serveFile answers with the text of a file in the current folder, path
// relative to it. os.Root refuses a path that leaves the folder, through ".."
// or a symbolic link.
func (s *liveServer) serveFile(w http.ResponseWriter, r *http.Request) {
	root := s.current().root
	if root == "" {
		http.Error(w, "no folder is open", http.StatusNotFound)
		return
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	defer dir.Close()
	file, err := dir.Open(filepath.FromSlash(r.URL.Query().Get("path")))
	if err != nil {
		http.Error(w, "no such file in the folder", http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "not a regular file", http.StatusNotFound)
		return
	}
	if info.Size() > fileSizeLimit {
		http.Error(w, "file is too large to show", http.StatusRequestEntityTooLarge)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.Copy(w, file)
}

// serveRoots answers with the current folder, or "" for no folder, and the
// folders that the working tree field offers. The working directory of the
// server takes the place of no folder when the server looks for git worktrees.
func (s *liveServer) serveRoots(w http.ResponseWriter, r *http.Request) {
	current := s.current().root
	base := current
	if base == "" {
		base, _ = os.Getwd()
	}
	writeJSON(w, struct {
		Current string       `json:"current"`
		Choices []rootChoice `json:"choices"`
	}{current, rootChoices(s.projects, base, time.Now().Add(-recentSessionAge))})
}

// serveSessions answers with the name of the current log and the summaries of
// the logs of the current folder, newest first.
func (s *liveServer) serveSessions(w http.ResponseWriter, r *http.Request) {
	view := s.current()
	summaries := []session.Summary{}
	if view.root != "" {
		var err error
		if summaries, err = session.List(s.sessions, view.root, sessionListLimit); err != nil {
			log.Printf("Listing session logs: %v", err)
			summaries = []session.Summary{}
		}
	}
	writeJSON(w, struct {
		Current  string            `json:"current"`
		Sessions []session.Summary `json:"sessions"`
	}{view.log.Name(), summaries})
}

// serveSession answers with the JSONL of the log name. The current log comes
// from memory, so it is complete even if its file could not be written.
func (s *liveServer) serveSession(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var data []byte
	if view := s.current(); name == view.log.Name() {
		for _, line := range view.log.Lines(0) {
			data = append(append(data, line...), '\n')
		}
	} else {
		var err error
		data, err = session.Read(s.sessions, name)
		if errors.Is(err, session.ErrNotFound) {
			http.Error(w, "no session log has this name", http.StatusNotFound)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

// serveOpen opens the folder that the JSON body names in its path field. It
// answers with the graph of the new folder when the server shows it, 409 while
// it opens another folder, and 422 with the error text when the folder does
// not open.
func (s *liveServer) serveOpen(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil || strings.TrimSpace(input.Path) == "" {
		http.Error(w, "The request names no folder.", http.StatusBadRequest)
		return
	}
	view, err := s.openFolder(strings.TrimSpace(input.Path))
	switch {
	case errors.Is(err, errOpening):
		http.Error(w, "The server already opens another folder.", http.StatusConflict)
	case err != nil:
		log.Printf("Keeping the current folder: %v", err)
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
	default:
		writeJSONBytes(w, view.graphPayload())
	}
}
