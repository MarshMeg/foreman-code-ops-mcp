package main

import (
	"log"
	"net/http"
)

func routes(h *BookHandler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/books", h.List)
	mux.HandleFunc("GET /api/books/{id}", h.Get)
	mux.HandleFunc("POST /api/books", h.Create)
	return mux
}

func main() {
	log.Fatal(http.ListenAndServe(":8080", routes(&BookHandler{})))
}
