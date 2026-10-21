package transport

import (
	"encoding/json"
	"net/http"

	"example.com/books/internal/domain"
	"example.com/books/internal/service"
)

// BookHandler serves books over HTTP.
type BookHandler struct {
	svc *service.BookService
}

// List handles GET /books.
func (h *BookHandler) List(w http.ResponseWriter, r *http.Request) {
	books, err := h.svc.List(r.Context(), domain.BookFilter{Title: r.URL.Query().Get("title")})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(books)
}
