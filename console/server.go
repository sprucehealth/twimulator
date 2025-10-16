package console

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"sort"
	"strings"

	"twimulator/engine"
	"twimulator/model"
)

//go:embed templates/*.html static/*
var content embed.FS

// ConsoleServer provides a web UI for inspecting engine state
type ConsoleServer struct {
	Addr   string
	engine engine.Engine
	server *http.Server
	tmpl   *template.Template
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
	mux.HandleFunc("/", cs.handleSubAccounts)
	mux.HandleFunc("/subaccounts/", cs.handleSubAccountDetail)
	mux.HandleFunc("/calls/", cs.handleCallDetail)
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

func (cs *ConsoleServer) handleSubAccounts(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	subAccounts := cs.engine.ListSubAccounts()

	data := map[string]any{
		"SubAccounts": subAccounts,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "subaccounts.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (cs *ConsoleServer) handleSubAccountDetail(w http.ResponseWriter, r *http.Request) {
	accountSID := strings.TrimPrefix(r.URL.Path, "/subaccounts/")
	if accountSID == "" {
		http.Error(w, "SubAccount SID required", http.StatusBadRequest)
		return
	}

	subAccount, exists := cs.engine.GetSubAccount(model.SID(accountSID))
	if !exists {
		http.NotFound(w, r)
		return
	}

	snap := cs.engine.Snapshot()

	// Filter calls by AccountSID
	calls := make([]*model.Call, 0)
	for _, call := range snap.Calls {
		if call.AccountSID == subAccount.SID {
			calls = append(calls, call)
		}
	}
	sort.SliceStable(calls, func(i, j int) bool {
		if calls[i].StartAt.Equal(calls[j].StartAt) {
			return calls[i].SID < calls[j].SID
		}
		return calls[i].StartAt.Before(calls[j].StartAt)
	})

	// Filter queues by AccountSID
	queues := make([]*model.Queue, 0)
	for _, queue := range snap.Queues {
		if queue.AccountSID == subAccount.SID {
			queues = append(queues, queue)
		}
	}

	// Filter conferences by AccountSID
	conferences := make([]*model.Conference, 0)
	for _, conf := range snap.Conferences {
		if conf.AccountSID == subAccount.SID {
			conferences = append(conferences, conf)
		}
	}

	data := map[string]any{
		"SubAccount":  subAccount,
		"Calls":       calls,
		"Queues":      queues,
		"Conferences": conferences,
		"Timestamp":   snap.Timestamp,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "subaccount.html", data); err != nil {
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

func (cs *ConsoleServer) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	snap := cs.engine.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snap); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
