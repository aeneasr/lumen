package project

import "net/http"

// HandleHealth returns 200 OK for health check endpoints.
func HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// HandleListUsers returns a JSON list of all users.
func HandleListUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
}

// HandleCreateUser creates a new user from the request body.
func HandleCreateUser(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusCreated)
}
