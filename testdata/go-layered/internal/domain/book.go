// Package domain holds the book model.
package domain

// Book is a catalogue entry.
type Book struct {
	ID     ID
	Title  string
	Status Status
}

type (
	// ID identifies a book.
	ID string
	// Status is the lending state.
	Status int
)

// BookFilter narrows List results.
type BookFilter struct {
	Title  string
	Limit  int
	Offset int
}

const (
	Available Status = iota
	Lent
)
