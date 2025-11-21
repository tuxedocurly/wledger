package core

import (
	"log"
	"net/http"
)

// ServerError logs the error and sends a 500 Internal Server Error
func ServerError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("Internal Server Error: %s %s: %s", r.Method, r.URL.Path, err.Error())
	http.Error(w, "Internal Server Error", http.StatusInternalServerError)
}

// ClientError logs the error and sends a specific client-side error status
func ClientError(w http.ResponseWriter, r *http.Request, status int, message string, err error) {
	if err != nil {
		log.Printf("Client Error: %s %s: %s", r.Method, r.URL.Path, err.Error())
	}
	http.Error(w, message, status)
}
