from app.domain import Book
from app.repo import MemRepo
from app.service import BookService


def test_count() -> None:
    svc = BookService(MemRepo([Book("1", "Dune"), Book("2", "Emma")]))
    assert svc.count() == 2
