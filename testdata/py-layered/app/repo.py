"""Book storage."""

from app.domain import Book, BookFilter


class MemRepo:
    """Keeps books in memory."""

    def __init__(self, books: list[Book] | None = None) -> None:
        self.books = list(books or [])

    def list(self, f: BookFilter) -> list[Book]:
        """Return books matching f."""
        return [b for b in self.books if f.title in b.title][: f.limit]

    @staticmethod
    def empty() -> "MemRepo":
        return MemRepo()
