// SPDX-License-Identifier: GPL-3.0-or-later

// Copyright (c) 2025 Spruce Health

package console

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"time"

	openapi "github.com/twilio/twilio-go/rest/api/v2010"

	"github.com/sprucehealth/twimulator/engine"
	"github.com/sprucehealth/twimulator/model"
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
	Addresses    []addressView
	SigningKeys  []signingKeyView
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

type addressView struct {
	SID              string
	CustomerName     string
	Street           string
	StreetSecondary  string
	City             string
	Region           string
	PostalCode       string
	IsoCountry       string
	FriendlyName     string
	EmergencyEnabled bool
	Validated        bool
	Verified         bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type signingKeyView struct {
	SID          string
	FriendlyName string
	Secret       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type sipDomainView struct {
	SID                       string
	DomainName                string
	FriendlyName              string
	VoiceUrl                  string
	VoiceMethod               string
	VoiceStatusCallbackUrl    string
	VoiceStatusCallbackMethod string
	SipRegistration           bool
	Secure                    bool
	AuthCallsMappingsCount    int
	AuthRegMappingsCount      int
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

type sipCredentialListView struct {
	SID          string
	FriendlyName string
	Credentials  []sipCredentialView
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type sipCredentialView struct {
	SID               string
	Username          string
	CredentialListSID string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type sipDomainDetailView struct {
	sipDomainView
	CallsMappings         []sipCredentialListMappingView
	RegistrationsMappings []sipCredentialListMappingView
}

type sipCredentialListMappingView struct {
	SID                  string
	CredentialListSID    string
	CredentialListName   string
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
	mux.HandleFunc("/conferences/", cs.handleConferenceDetail)
	mux.HandleFunc("/queues/", cs.handleQueueDetail)
	mux.HandleFunc("/numbers/", cs.handleNumberDetail)
	mux.HandleFunc("/addresses/", cs.handleAddressDetail)
	mux.HandleFunc("/api/snapshot", cs.handleSnapshot)
	mux.HandleFunc("/Accounts/", cs.handleRecording)
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

		addresses := make([]addressView, len(sa.Addresses))
		for idx, addr := range sa.Addresses {
			addresses[idx] = addressView{
				SID:              string(addr.SID),
				CustomerName:     addr.CustomerName,
				Street:           addr.Street,
				StreetSecondary:  addr.StreetSecondary,
				City:             addr.City,
				Region:           addr.Region,
				PostalCode:       addr.PostalCode,
				IsoCountry:       addr.IsoCountry,
				FriendlyName:     addr.FriendlyName,
				EmergencyEnabled: addr.EmergencyEnabled,
				Validated:        addr.Validated,
				Verified:         addr.Verified,
				CreatedAt:        addr.CreatedAt,
				UpdatedAt:        addr.UpdatedAt,
			}
		}
		views[i].Addresses = addresses

		signingKeys := make([]signingKeyView, len(sa.SigningKeys))
		for idx, key := range sa.SigningKeys {
			signingKeys[idx] = signingKeyView{
				SID:          key.SID,
				FriendlyName: key.FriendlyName,
				Secret:       key.Secret,
				CreatedAt:    key.CreatedAt,
				UpdatedAt:    key.UpdatedAt,
			}
		}
		views[i].SigningKeys = signingKeys
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

	addresses := make([]addressView, len(subAccountModel.Addresses))
	for idx, addr := range subAccountModel.Addresses {
		addresses[idx] = addressView{
			SID:              string(addr.SID),
			CustomerName:     addr.CustomerName,
			Street:           addr.Street,
			StreetSecondary:  addr.StreetSecondary,
			City:             addr.City,
			Region:           addr.Region,
			PostalCode:       addr.PostalCode,
			IsoCountry:       addr.IsoCountry,
			FriendlyName:     addr.FriendlyName,
			EmergencyEnabled: addr.EmergencyEnabled,
			Validated:        addr.Validated,
			Verified:         addr.Verified,
			CreatedAt:        addr.CreatedAt,
			UpdatedAt:        addr.UpdatedAt,
		}
	}
	view.Addresses = addresses

	signingKeys := make([]signingKeyView, len(subAccountModel.SigningKeys))
	for idx, key := range subAccountModel.SigningKeys {
		signingKeys[idx] = signingKeyView{
			SID:          key.SID,
			FriendlyName: key.FriendlyName,
			Secret:       key.Secret,
			CreatedAt:    key.CreatedAt,
			UpdatedAt:    key.UpdatedAt,
		}
	}
	view.SigningKeys = signingKeys

	// Extract SIP domains with detailed mappings
	accountSIDStr := accountSID
	sipDomainsDetailed := make([]sipDomainDetailView, len(subAccountModel.SipDomains))
	for idx, domain := range subAccountModel.SipDomains {
		// Build calls mappings with credential list names
		callsMappings := make([]sipCredentialListMappingView, 0, len(domain.AuthCallsMappings))
		for _, mapping := range domain.AuthCallsMappings {
			// Find the credential list name
			credListName := string(mapping.CredentialListSID)
			// Try to fetch the credential list to get its friendly name
			credListsResp, err := cs.engine.ListSipCredentialList(&openapi.ListSipCredentialListParams{
				PathAccountSid: &accountSIDStr,
			})
			if err == nil {
				for _, cl := range credListsResp {
					if cl.Sid != nil && *cl.Sid == string(mapping.CredentialListSID) {
						if cl.FriendlyName != nil {
							credListName = *cl.FriendlyName
						}
						break
					}
				}
			}

			callsMappings = append(callsMappings, sipCredentialListMappingView{
				SID:                string(mapping.SID),
				CredentialListSID:  string(mapping.CredentialListSID),
				CredentialListName: credListName,
				CreatedAt:          mapping.CreatedAt,
			})
		}

		// Build registrations mappings with credential list names
		regMappings := make([]sipCredentialListMappingView, 0, len(domain.AuthRegistrationsMappings))
		for _, mapping := range domain.AuthRegistrationsMappings {
			credListName := string(mapping.CredentialListSID)
			// Try to fetch the credential list to get its friendly name
			credListsResp, err := cs.engine.ListSipCredentialList(&openapi.ListSipCredentialListParams{
				PathAccountSid: &accountSIDStr,
			})
			if err == nil {
				for _, cl := range credListsResp {
					if cl.Sid != nil && *cl.Sid == string(mapping.CredentialListSID) {
						if cl.FriendlyName != nil {
							credListName = *cl.FriendlyName
						}
						break
					}
				}
			}

			regMappings = append(regMappings, sipCredentialListMappingView{
				SID:                string(mapping.SID),
				CredentialListSID:  string(mapping.CredentialListSID),
				CredentialListName: credListName,
				CreatedAt:          mapping.CreatedAt,
			})
		}

		sipDomainsDetailed[idx] = sipDomainDetailView{
			sipDomainView: sipDomainView{
				SID:                       string(domain.SID),
				DomainName:                domain.DomainName,
				FriendlyName:              domain.FriendlyName,
				VoiceUrl:                  domain.VoiceUrl,
				VoiceMethod:               domain.VoiceMethod,
				VoiceStatusCallbackUrl:    domain.VoiceStatusCallbackUrl,
				VoiceStatusCallbackMethod: domain.VoiceStatusCallbackMethod,
				SipRegistration:           domain.SipRegistration,
				Secure:                    domain.Secure,
				AuthCallsMappingsCount:    len(domain.AuthCallsMappings),
				AuthRegMappingsCount:      len(domain.AuthRegistrationsMappings),
				CreatedAt:                 domain.CreatedAt,
				UpdatedAt:                 domain.UpdatedAt,
			},
			CallsMappings:         callsMappings,
			RegistrationsMappings: regMappings,
		}
	}

	// Extract credential lists from ListSipCredentialList API
	// Since credential lists are stored separately in the engine state,
	// we need to call the API to get them
	credListsResp, err := cs.engine.ListSipCredentialList(&openapi.ListSipCredentialListParams{
		PathAccountSid: &accountSIDStr,
	})
	if err != nil {
		// If there's an error, just use empty list
		credListsResp = []openapi.ApiV2010SipCredentialList{}
	}

	sipCredentialLists := make([]sipCredentialListView, 0, len(credListsResp))
	for _, credList := range credListsResp {
		var createdAt, updatedAt time.Time
		if credList.DateCreated != nil {
			if t, err := time.Parse(time.RFC1123Z, *credList.DateCreated); err == nil {
				createdAt = t
			}
		}
		if credList.DateUpdated != nil {
			if t, err := time.Parse(time.RFC1123Z, *credList.DateUpdated); err == nil {
				updatedAt = t
			}
		}
		friendlyName := ""
		if credList.FriendlyName != nil {
			friendlyName = *credList.FriendlyName
		}
		sid := ""
		if credList.Sid != nil {
			sid = *credList.Sid
		}

		// Fetch credentials for this credential list
		credentials := make([]sipCredentialView, 0)
		if sid != "" {
			credsResp, err := cs.engine.ListSipCredential(sid, &openapi.ListSipCredentialParams{
				PathAccountSid: &accountSIDStr,
			})
			if err == nil {
				for _, cred := range credsResp {
					var credCreatedAt, credUpdatedAt time.Time
					if cred.DateCreated != nil {
						if t, err := time.Parse(time.RFC1123Z, *cred.DateCreated); err == nil {
							credCreatedAt = t
						}
					}
					if cred.DateUpdated != nil {
						if t, err := time.Parse(time.RFC1123Z, *cred.DateUpdated); err == nil {
							credUpdatedAt = t
						}
					}
					username := ""
					if cred.Username != nil {
						username = *cred.Username
					}
					credSid := ""
					if cred.Sid != nil {
						credSid = *cred.Sid
					}
					credListSid := ""
					if cred.CredentialListSid != nil {
						credListSid = *cred.CredentialListSid
					}

					credentials = append(credentials, sipCredentialView{
						SID:               credSid,
						Username:          username,
						CredentialListSID: credListSid,
						CreatedAt:         credCreatedAt,
						UpdatedAt:         credUpdatedAt,
					})
				}
			}
		}

		sipCredentialLists = append(sipCredentialLists, sipCredentialListView{
			SID:          sid,
			FriendlyName: friendlyName,
			Credentials:  credentials,
			CreatedAt:    createdAt,
			UpdatedAt:    updatedAt,
		})
	}

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

	// Get recordings for this account from the snapshot
	recordings := make([]*model.Recording, 0, len(snap.Recordings))
	for _, recording := range snap.Recordings {
		recordings = append(recordings, recording)
	}
	// Sort recordings by created time (most recent first)
	sort.SliceStable(recordings, func(i, j int) bool {
		return recordings[i].CreatedAt.After(recordings[j].CreatedAt)
	})

	data := map[string]any{
		"SubAccount":          view,
		"Applications":        view.Applications,
		"Numbers":             numbers,
		"Addresses":           addresses,
		"SigningKeys":         signingKeys,
		"SipDomains":          sipDomainsDetailed,
		"SipCredentialLists":  sipCredentialLists,
		"Calls":               calls,
		"Queues":              queues,
		"Conferences":         conferences,
		"Recordings":          recordings,
		"Timestamp":           snap.Timestamp,
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
	var conferenceInfo *model.Conference
	var recordings []*model.Recording
	callSID := model.SID(sid)

	for _, snap := range snaps {
		if c, exists := snap.Calls[callSID]; exists {
			call = c
			// Check if call is in a conference
			if strings.HasPrefix(c.CurrentEndpoint, "conference:") {
				confName := strings.TrimPrefix(c.CurrentEndpoint, "conference:")
				// Find the conference by name
				for _, conf := range snap.Conferences {
					if conf.Name == confName {
						conferenceInfo = conf
						break
					}
				}
			}

			// Get recordings associated with this call
			for _, recording := range snap.Recordings {
				if recording.CallSID != nil && *recording.CallSID == callSID {
					recordings = append(recordings, recording)
				}
			}
			break
		}
	}
	if call == nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{
		"Call":       call,
		"Conference": conferenceInfo,
		"Recordings": recordings,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "call.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (cs *ConsoleServer) handleConferenceDetail(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimPrefix(r.URL.Path, "/conferences/")
	if sid == "" {
		http.Error(w, "Conference SID required", http.StatusBadRequest)
		return
	}

	snaps := cs.engine.SnapshotAll()
	var conference *model.Conference
	var accountSID model.SID
	for _, snap := range snaps {
		// Conferences are keyed by name, but we're looking for SID
		// We need to iterate through all conferences
		for _, c := range snap.Conferences {
			if string(c.SID) == sid {
				conference = c
				accountSID = c.AccountSID
				break
			}
		}
		if conference != nil {
			break
		}
	}
	if conference == nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{
		"Conference": conference,
		"AccountSID": accountSID,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "conference.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (cs *ConsoleServer) handleQueueDetail(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimPrefix(r.URL.Path, "/queues/")
	if sid == "" {
		http.Error(w, "Queue SID required", http.StatusBadRequest)
		return
	}

	snaps := cs.engine.SnapshotAll()
	var queue *model.Queue
	for _, snap := range snaps {
		for _, q := range snap.Queues {
			if string(q.SID) == sid {
				queue = q
				break
			}
		}
		if queue != nil {
			break
		}
	}
	if queue == nil {
		http.NotFound(w, r)
		return
	}

	data := map[string]any{
		"Queue": queue,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "queue.html", data); err != nil {
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

func (cs *ConsoleServer) handleAddressDetail(w http.ResponseWriter, r *http.Request) {
	sid := strings.TrimPrefix(r.URL.Path, "/addresses/")
	if sid == "" {
		http.Error(w, "Address SID required", http.StatusBadRequest)
		return
	}

	snaps := cs.engine.SnapshotAll()
	var address addressView
	var accountName string
	found := false

	for _, snap := range snaps {
		for _, sa := range snap.SubAccounts {
			for _, addr := range sa.Addresses {
				if string(addr.SID) == sid {
					address = addressView{
						SID:              string(addr.SID),
						CustomerName:     addr.CustomerName,
						Street:           addr.Street,
						StreetSecondary:  addr.StreetSecondary,
						City:             addr.City,
						Region:           addr.Region,
						PostalCode:       addr.PostalCode,
						IsoCountry:       addr.IsoCountry,
						FriendlyName:     addr.FriendlyName,
						EmergencyEnabled: addr.EmergencyEnabled,
						Validated:        addr.Validated,
						Verified:         addr.Verified,
						CreatedAt:        addr.CreatedAt,
						UpdatedAt:        addr.UpdatedAt,
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
		"Address":     address,
		"AccountName": accountName,
	}

	if err := cs.tmpl.ExecuteTemplate(w, "address.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleRecording serves recording files
// URL format: /Accounts/{AccountSID}/Recordings/{RecordingSID}
func (cs *ConsoleServer) handleRecording(w http.ResponseWriter, r *http.Request) {
	// Parse URL path: /Accounts/{AccountSID}/Recordings/{RecordingSID}
	path := strings.TrimPrefix(r.URL.Path, "/Accounts/")
	parts := strings.Split(path, "/")

	if len(parts) != 3 || parts[1] != "Recordings" {
		http.Error(w, "Invalid recording URL format. Expected: /Accounts/{AccountSID}/Recordings/{RecordingSID}", http.StatusBadRequest)
		return
	}

	accountSID := model.SID(parts[0])
	recordingSid := parts[2]
	// Remove any file extension from the recording sid (e.g. .wav, .mp3)
	ext := filepath.Ext(recordingSid)
	if ext != "" {
		recordingSid = recordingSid[:len(recordingSid)-len(ext)]
	}
	recordingSID := model.SID(recordingSid)

	// Get the recording from the engine
	recording, err := cs.engine.GetRecording(accountSID, recordingSID)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// Serve the recording file
	http.ServeFile(w, r, recording.FilePath)
}
