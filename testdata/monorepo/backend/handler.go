package main

import (
	"encoding/json"
	"net/http"
)

// Book is a catalogue entry.
type Book struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// BookHandler serves books.
type BookHandler struct {
	books []Book
}

// List handles GET /api/books.
func (h *BookHandler) List(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(h.books)
}

// Get handles GET /api/books/{id}.
func (h *BookHandler) Get(w http.ResponseWriter, r *http.Request) {
	for _, b := range h.books {
		if b.ID == r.PathValue("id") {
			json.NewEncoder(w).Encode(b)
			return
		}
	}
	http.NotFound(w, r)
}

// Create handles POST /api/books.
func (h *BookHandler) Create(w http.ResponseWriter, r *http.Request) {
	var b Book
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.books = append(h.books, b)
	w.WriteHeader(http.StatusCreated)
}
