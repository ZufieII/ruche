package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ruche/internal/protocol"
)

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

type Server struct {
	Store         *Store
	AdminPassword string
	Static        fs.FS
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Agent side: authenticated by the rig token.
	mux.HandleFunc("POST /api/agent/report", s.handleReport)

	// Dashboard side: authenticated by the admin password.
	admin := http.NewServeMux()
	admin.HandleFunc("GET /api/rigs", s.handleListRigs)
	admin.HandleFunc("POST /api/rigs", s.handleCreateRig)
	admin.HandleFunc("DELETE /api/rigs/{id}", s.handleDeleteRig)
	admin.HandleFunc("GET /api/rigs/{id}/history", s.handleHistory)
	admin.HandleFunc("POST /api/rigs/{id}/commands", s.handleCommand)
	admin.HandleFunc("PUT /api/rigs/{id}/flight-sheet", s.handleAssign)
	admin.HandleFunc("GET /api/flight-sheets", s.handleListFlightSheets)
	admin.HandleFunc("POST /api/flight-sheets", s.handleSaveFlightSheet)
	admin.HandleFunc("PUT /api/flight-sheets/{id}", s.handleSaveFlightSheet)
	admin.HandleFunc("DELETE /api/flight-sheets/{id}", s.handleDeleteFlightSheet)
	if s.Static != nil {
		admin.Handle("GET /", http.FileServerFS(s.Static))
	}
	mux.Handle("/", s.requireAdmin(admin))
	return mux
}

func (s *Server) requireAdmin(next http.Handler) http.Handler {
	want := []byte(s.AdminPassword)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="ruche"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		log.Printf("error: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	rigID, err := s.Store.RigByToken(token)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	} else if err != nil {
		writeErr(w, err)
		return
	}
	var rep protocol.Report
	if !readJSON(w, r, &rep) {
		return
	}
	resp, err := s.Store.SaveReport(rigID, rep)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, resp)
}

func (s *Server) handleListRigs(w http.ResponseWriter, r *http.Request) {
	rigs, err := s.Store.Rigs()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, rigs)
}

func (s *Server) handleCreateRig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	rig, token, err := s.Store.CreateRig(strings.TrimSpace(body.Name))
	if err != nil {
		writeErr(w, err)
		return
	}
	// The token is shown once; the server only keeps its hash.
	writeJSON(w, map[string]string{"id": rig.ID, "name": rig.Name, "token": token})
}

func (s *Server) handleDeleteRig(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteRig(r.PathValue("id")); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 || hours > 24*7 {
		hours = 24
	}
	pts, err := s.Store.History(r.PathValue("id"), time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, pts)
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Type string `json:"type"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.Store.QueueCommand(r.PathValue("id"), body.Type); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleAssign(w http.ResponseWriter, r *http.Request) {
	var body struct {
		FlightSheetID *int64 `json:"flight_sheet_id"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if err := s.Store.AssignFlightSheet(r.PathValue("id"), body.FlightSheetID); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListFlightSheets(w http.ResponseWriter, r *http.Request) {
	fss, err := s.Store.FlightSheets()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, fss)
}

func (s *Server) handleSaveFlightSheet(w http.ResponseWriter, r *http.Request) {
	var f protocol.FlightSheet
	if !readJSON(w, r, &f) {
		return
	}
	f.ID = 0
	if idStr := r.PathValue("id"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "bad id", http.StatusBadRequest)
			return
		}
		f.ID = id
	}
	switch f.Miner {
	case "":
		f.Miner = protocol.MinerXMRig
	case protocol.MinerXMRig, protocol.MinerLolMiner:
	default:
		http.Error(w, "unknown miner", http.StatusBadRequest)
		return
	}
	if f.Name == "" || f.Algo == "" || f.Pool == "" || f.Wallet == "" {
		http.Error(w, "name, algo, pool and wallet are required", http.StatusBadRequest)
		return
	}
	saved, err := s.Store.SaveFlightSheet(f)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, saved)
}

func (s *Server) handleDeleteFlightSheet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := s.Store.DeleteFlightSheet(id); err != nil {
		writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
