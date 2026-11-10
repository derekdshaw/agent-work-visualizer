package server

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/derekdshaw/agent-work-visualizer/internal/settings"
)

// serveSettings returns the current settings.
func (s *liveServer) serveSettings(w http.ResponseWriter, r *http.Request) {
	s.settingsMu.Lock()
	current := s.settings
	s.settingsMu.Unlock()
	writeSettings(w, current)
}

// saveSettings replaces the settings with the full object in the request,
// writes the file, and returns the new settings. The settings and the file
// do not change if the object is not valid, if it has a field that the
// server does not know, or if the write fails. The error text goes to the
// page, so the viewer can see what was wrong.
func (s *liveServer) saveSettings(w http.ResponseWriter, r *http.Request) {
	var next settings.Settings
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&next); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := settings.Validate(next); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if err := settings.Save(s.settingsPath, next); err != nil {
		log.Printf("Keeping the current settings: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.settings = next
	writeSettings(w, next)
}

func writeSettings(w http.ResponseWriter, current settings.Settings) {
	data, err := json.Marshal(current)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONBytes(w, data)
}
