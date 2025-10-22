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
	"time"

	openapi "github.com/twilio/twilio-go/rest/api/v2010"

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

type accountView struct {
	SID          string
	FriendlyName string
	Status       string
	CreatedAt    time.Time
	AuthToken    string
	Numbers      []numberView
	Applications []applicationView
}

type numberView struct {
	SID             string
	PhoneNumber     string
	ApplicationSID  string
	ApplicationName string
	CreatedAt       time.Time
}
type applicationView struct {
	SID                  string
	FriendlyName         string
	VoiceURL             string
	VoiceMethod          string
	StatusCallback       string
	StatusCallbackMethod string
	CreatedAt            time.Time
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
	mux.HandleFunc("/numbers/", cs.handleNumberDetail)
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

	accounts, err := cs.engine.ListAccount(nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list accounts: %v", err), http.StatusInternalServerError)
		return
	}

	views := make([]accountView, 0, len(accounts))
	for _, acct := range accounts {
		views = append(views, toAccountView(acct))
	}

	snaps := cs.engine.SnapshotAll()
	// Create a lookup map for snapshots by account SID
	snapLookup := make(map[model.SID]*engine.StateSnapshot)
	for _, snap := range snaps {
		for sid := range snap.SubAccounts {
			snapLookup[sid] = snap
			break // Each snapshot should have only one subaccount
		}
	}

	for i := range views {
		snap, ok := snapLookup[model.SID(views[i].SID)]
		if !ok {
			continue
		}
		sa, ok := snap.SubAccounts[model.SID(views[i].SID)]
		if !ok {
			continue
		}

		appLookup := make(map[string]applicationView, len(sa.Applications))
		apps := make([]applicationView, len(sa.Applications))
		for idx, app := range sa.Applications {
			viewApp := applicationView{
				SID:                  app.SID,
				FriendlyName:         app.FriendlyName,
				VoiceURL:             app.VoiceURL,
				VoiceMethod:          app.VoiceMethod,
				StatusCallback:       app.StatusCallback,
				StatusCallbackMethod: app.StatusCallbackMethod,
				CreatedAt:            app.CreatedAt,
			}
			apps[idx] = viewApp
			appLookup[app.SID] = viewApp
		}
		views[i].Applications = apps

		numbers := make([]numberView, len(sa.IncomingNumbers))
		for idx, num := range sa.IncomingNumbers {
			viewNum := numberView{
				SID:         num.SID,
				PhoneNumber: num.PhoneNumber,
				CreatedAt:   num.CreatedAt,
			}
			if num.VoiceApplicationSID != nil {
				viewNum.ApplicationSID = *num.VoiceApplicationSID
				if appView, ok := appLookup[viewNum.ApplicationSID]; ok {
					viewNum.ApplicationName = appView.FriendlyName
				}
			}
			numbers[idx] = viewNum
		}
		views[i].Numbers = numbers
	}

	sort.SliceStable(views, func(i, j int) bool {
		if views[i].CreatedAt.Equal(views[j].CreatedAt) {
			return views[i].SID < views[j].SID
		}
		return views[i].CreatedAt.Before(views[j].CreatedAt)
	})

	data := map[string]any{
		"SubAccounts": views,
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

	accounts, err := cs.engine.ListAccount(nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list accounts: %v", err), http.StatusInternalServerError)
		return
	}

	var view *accountView
	for _, acct := range accounts {
		if acct.Sid != nil && *acct.Sid == accountSID {
			v := toAccountView(acct)
			view = &v
			break
		}
	}

	if view == nil {
		http.NotFound(w, r)
		return
	}

	accountModelSID := model.SID(accountSID)
	snap, err := cs.engine.Snapshot(accountModelSID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	subAccountModel, ok := snap.SubAccounts[accountModelSID]
	if !ok {
		http.NotFound(w, r)
		return
	}
	appLookup := make(map[string]applicationView, len(subAccountModel.Applications))
	apps := make([]applicationView, len(subAccountModel.Applications))
	for idx, app := range subAccountModel.Applications {
		viewApp := applicationView{
			SID:                  app.SID,
			FriendlyName:         app.FriendlyName,
			VoiceURL:             app.VoiceURL,
			VoiceMethod:          app.VoiceMethod,
			StatusCallback:       app.StatusCallback,
			StatusCallbackMethod: app.StatusCallbackMethod,
			CreatedAt:            app.CreatedAt,
		}
		apps[idx] = viewApp
		appLookup[app.SID] = viewApp
	}
	view.Applications = apps

	numbers := make([]numberView, len(subAccountModel.IncomingNumbers))
	for idx, num := range subAccountModel.IncomingNumbers {
		viewNum := numberView{
			SID:         num.SID,
			PhoneNumber: num.PhoneNumber,
			CreatedAt:   num.CreatedAt,
		}
		if num.VoiceApplicationSID != nil {
			viewNum.ApplicationSID = *num.VoiceApplicationSID
			if appView, ok := appLookup[viewNum.ApplicationSID]; ok {
				viewNum.ApplicationName = appView.FriendlyName
			}
		}
		numbers[idx] = viewNum
	}

	view.Numbers = numbers

	// Get calls for this account from the snapshot
	calls := make([]*model.Call, 0, len(snap.Calls))
	for _, call := range snap.Calls {
		calls = append(calls, call)
	}
	sort.SliceStable(calls, func(i, j int) bool {
		if calls[i].StartAt.Equal(calls[j].StartAt) {
			return calls[i].SID < calls[j].SID
		}
		return calls[i].StartAt.Before(calls[j].StartAt)
	})

	// Get queues for this account from the snapshot
	queues := make([]*model.Queue, 0, len(snap.Queues))
	for _, queue := range snap.Queues {
		queues = append(queues, queue)
	}

	// Get conferences for this account from the snapshot
	conferences := make([]*model.Conference, 0, len(snap.Conferences))
	for _, conf := range snap.Conferences {
		conferences = append(conferences, conf)
	}

	data := map[string]any{
		"SubAccount":   view,
		"Applications": view.Applications,
		"Numbers":      numbers,
		"Calls":        calls,
		"Queues":       queues,
		"Conferences":  conferences,
		"Timestamp":    snap.Timestamp,
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

	snaps := cs.engine.SnapshotAll()
	var call *model.Call
	for _, snap := range snaps {
		if c, exists := snap.Calls[model.SID(sid)]; exists {
			call = c
			break
		}
	}
	if call == nil {
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
	snaps := cs.engine.SnapshotAll()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snaps); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func toAccountView(acct openapi.ApiV2010Account) accountView {
	var created time.Time
	if acct.DateCreated != nil {
		if t, err := time.Parse(time.RFC1123Z, *acct.DateCreated); err == nil {
			created = t
		}
	}

	view := accountView{CreatedAt: created}
	if acct.Sid != nil {
		view.SID = *acct.Sid
	}
	if acct.FriendlyName != nil {
		view.FriendlyName = *acct.FriendlyName
	}
	if acct.Status != nil {
		view.Status = *acct.Status
	}
	if acct.AuthToken != nil {
		view.AuthToken = *acct.AuthToken
	}
	return view
}

func (cs *ConsoleServer) handleNumberDetail(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimPrefix(r.URL.Path, "/numbers/")
	if sid == "" {
		http.Error(w, "Number SID required", http.StatusBadRequest)
		return
	}

	snaps := cs.engine.SnapshotAll()
	var number numberView
	var accountName string
	found := false

	for _, snap := range snaps {
		for _, sa := range snap.SubAccounts {
			appLookup := make(map[string]string, len(sa.Applications))
			for _, app := range sa.Applications {
				appLookup[app.SID] = app.FriendlyName
			}

			for _, num := range sa.IncomingNumbers {
				if num.SID == sid {
					number = numberView{
						SID:         num.SID,
						PhoneNumber: num.PhoneNumber,
						CreatedAt:   num.CreatedAt,
					}
					if num.VoiceApplicationSID != nil {
						number.ApplicationSID = *num.VoiceApplicationSID
						if name, ok := appLookup[number.ApplicationSID]; ok {
							number.ApplicationName = name
						}
					}
					accountName = sa.FriendlyName
					found = true
					break
				}
			}

			if found {
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{
		"Number":      number,
		"AccountName": accountName,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "number.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
