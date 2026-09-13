package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ili16/recipe-generator/internal/auth"
	"github.com/ili16/recipe-generator/internal/storage"
	"github.com/jackc/pgx/v5"
)

// Household routes (BACKLOG 15.1). None of these call a model, so none are rate-limited
// or budget-capped; all require auth, because a household is a thing accounts join.
//
// There is no invite endpoint and no membership list beyond the members themselves: the
// join code is the whole mechanism, exactly as an unguessable URL is the whole mechanism
// for a shared recipe (BACKLOG 8.4). Which is also why it can be rotated.

// maxHouseholdNameLen keeps a name to something that fits on a phone screen. It is a
// display string with no other meaning, so this is the only validation it gets.
const maxHouseholdNameLen = 60

// householdError maps the storage layer's three failure modes onto status codes. A join
// code that does not resolve is pgx.ErrNoRows and comes back 404 — a wrong code and a
// rotated one are deliberately indistinguishable.
func householdError(w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, storage.ErrAlreadyInHousehold):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "already_in_household"})
	case errors.Is(err, storage.ErrNotInHousehold), errors.Is(err, pgx.ErrNoRows):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	default:
		slog.Error("household: "+what, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// HandleGetHousehold returns the caller's household and its members, or 204 when they are
// not in one — "no household" is a normal state, not an error, and the client renders a
// different screen for it rather than an error.
func HandleGetHousehold(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	h, err := storage.GetHousehold(r.Context(), user.ID)
	if errors.Is(err, storage.ErrNotInHousehold) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		householdError(w, err, "get")
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// HandleCreateHousehold makes one and moves everything the caller owns into it.
func HandleCreateHousehold(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > maxHouseholdNameLen {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	h, err := storage.CreateHousehold(r.Context(), user.ID, name)
	if err != nil {
		householdError(w, err, "create")
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

// HandleJoinHousehold moves the caller and everything they own into the household holding
// the code. The code is matched exactly as stored, after trimming and upcasing what the
// user typed — it is read aloud and retyped, so case is not part of the credential.
func HandleJoinHousehold(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	var req struct {
		JoinCode string `json:"join_code"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}
	code := strings.ToUpper(strings.TrimSpace(req.JoinCode))
	if code == "" {
		http.Error(w, "join_code is required", http.StatusBadRequest)
		return
	}
	h, err := storage.JoinHousehold(r.Context(), user.ID, code)
	if err != nil {
		householdError(w, err, "join")
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// HandleLeaveHousehold takes the caller out, with their own recipes and pantry lines. The
// last member leaving disbands it.
func HandleLeaveHousehold(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	if err := storage.LeaveHousehold(r.Context(), user.ID); err != nil {
		householdError(w, err, "leave")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleDisbandHousehold ends it for everyone. Any member may — see DisbandHousehold.
func HandleDisbandHousehold(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	if err := storage.DisbandHousehold(r.Context(), user.ID); err != nil {
		householdError(w, err, "disband")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// HandleRotateJoinCode issues a fresh code and kills the old one. This is the only way to
// revoke access after somebody leaves, which is why it exists at all.
func HandleRotateJoinCode(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.FromContext(r.Context())
	if !ok {
		http.Error(w, "Unauthorized: user context missing", http.StatusUnauthorized)
		return
	}
	code, err := storage.RotateJoinCode(r.Context(), user.ID)
	if err != nil {
		householdError(w, err, "rotate")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"join_code": code})
}
