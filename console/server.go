package console

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"

	"twimulator/engine"
	"twimulator/model"
)

//go:embed templates/*.html static/*
var content embed.FS

// ConsoleServer provides a web UI for inspecting engine state
type ConsoleServer struct {
	Addr    string
	engine  engine.Engine
	server  *http.Server
	tmpl    *template.Template
}

// NewConsoleServer creates a new console server
func NewConsoleServer(e engine.Engine, addr string) (*ConsoleServer, error) {
	if addr == "" {
		addr = ":8089"
	}

	// Create template with custom functions
	funcs := template.FuncMap{
		"json": func(v any) string {
			b, _ := json.MarshalIndent(v, "", "  ")
			return string(b)
		},
	}

	tmpl, err := template.New("").Funcs(funcs).ParseFS(content, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	cs := &ConsoleServer{
		Addr:   addr,
		engine: e,
		tmpl:   tmpl,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", cs.handleIndex)
	mux.HandleFunc("/calls/", cs.handleCallDetail)
	mux.HandleFunc("/queues", cs.handleQueues)
	mux.HandleFunc("/conferences", cs.handleConferences)
	mux.HandleFunc("/api/snapshot", cs.handleSnapshot)
	mux.Handle("/static/", http.FileServer(http.FS(content)))

	cs.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return cs, nil
}

// Start starts the console server
func (cs *ConsoleServer) Start() error {
	log.Printf("Console UI running at http://localhost%s", cs.Addr)
	return cs.server.ListenAndServe()
}

// Stop gracefully stops the server
func (cs *ConsoleServer) Stop(ctx context.Context) error {
	return cs.server.Shutdown(ctx)
}

func (cs *ConsoleServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	snap := cs.engine.Snapshot()

	// Convert to slice for sorting
	calls := make([]*model.Call, 0, len(snap.Calls))
	for _, call := range snap.Calls {
		calls = append(calls, call)
	}

	data := map[string]any{
		"Calls":     calls,
		"Timestamp": snap.Timestamp,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (cs *ConsoleServer) handleCallDetail(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimPrefix(r.URL.Path, "/calls/")
	if sid == "" {
		http.Error(w, "Call SID required", http.StatusBadRequest)
		return
	}

	call, exists := cs.engine.GetCall(model.SID(sid))
	if !exists {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{
		"Call": call,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "call.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (cs *ConsoleServer) handleQueues(w http.ResponseWriter, r *http.Request) {
	snap := cs.engine.Snapshot()

	queues := make([]*model.Queue, 0, len(snap.Queues))
	for _, q := range snap.Queues {
		queues = append(queues, q)
	}

	data := map[string]any{
		"Queues": queues,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "queues.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (cs *ConsoleServer) handleConferences(w http.ResponseWriter, r *http.Request) {
	snap := cs.engine.Snapshot()

	conferences := make([]*model.Conference, 0, len(snap.Conferences))
	for _, c := range snap.Conferences {
		conferences = append(conferences, c)
	}

	data := map[string]any{
		"Conferences": conferences,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "conferences.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (cs *ConsoleServer) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := cs.engine.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
