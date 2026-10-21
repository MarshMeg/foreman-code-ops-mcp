package repo

import (
	"context"

	"example.com/books/internal/domain"
)

// BookRepo stores books.
type BookRepo interface {
	List(ctx context.Context, f domain.BookFilter) ([]domain.Book, error)
}

// Page is one page of results.
type Page[T any] struct {
	Items []T
}

// Len returns the number of items.
func (p Page[T]) Len() int { return len(p.Items) }

type memRepo struct {
	books []domain.Book
}

// NewMemRepo returns an in-memory BookRepo.
func NewMemRepo(books ...domain.Book) BookRepo {
	return &memRepo{books: books}
}

func (r *memRepo) List(ctx context.Context, f domain.BookFilter) ([]domain.Book, error) {
	return r.books, nil
}
