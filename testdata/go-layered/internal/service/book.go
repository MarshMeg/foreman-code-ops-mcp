package service

import (
	"context"

	"example.com/books/internal/domain"
	"example.com/books/internal/repo"
)

var defaultLimit = 20 // used when the filter has no limit

// BookService implements book use cases.
type BookService struct {
	repo repo.BookRepo
}

// NewBookService creates a BookService.
func NewBookService(r repo.BookRepo) *BookService {
	return &BookService{repo: r}
}

// List returns books matching f.
// A zero limit means defaultLimit.
func (s *BookService) List(ctx context.Context, f domain.BookFilter) ([]domain.Book, error) {
	if f.Limit == 0 {
		f.Limit = defaultLimit
	}
	return s.repo.List(ctx, f)
}

// Count returns the number of books.
func (s *BookService) Count(ctx context.Context) (int, error) {
	books, err := s.List(ctx, domain.BookFilter{})
	return len(books), err
}

func unusedHelper() {}
