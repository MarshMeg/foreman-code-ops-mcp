"""Book use cases."""

from app.domain import Book, BookFilter
from app.repo import MemRepo


class BookService:
    """Implements book use cases."""

    def __init__(self, repo: MemRepo) -> None:
        self.repo = repo

    # Lists books; a zero limit means the default.
    def list(self, f: BookFilter) -> list[Book]:
        if f.limit == 0:
            f.limit = 20
        return self.repo.list(f)

    def count(self) -> int:
        return len(self.repo.list(BookFilter(limit=1000)))


def default_service() -> BookService:
    return BookService(MemRepo())
