package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	// gateHoldLimit is the longest time that the gate holds a tool call. It
	// is shorter than the curl and hook timeouts of the PreToolUse hook, so
	// the agent receives the denial instead of a timeout.
	gateHoldLimit = 50 * time.Minute
	// gateDenial is the PreToolUse hook output that denies a tool call.
	gateDenial = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"The user paused agent work from the live view. Stop and wait for the user."}}`
)

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
	s.current().setControl("pause", pauseState{Paused: s.gate.paused, Waiting: s.gate.waiting})
}
