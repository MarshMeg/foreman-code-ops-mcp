"""HTTP-ish routes."""

from typing import Callable

from app.domain import BookFilter
from app.service import BookService

ROUTES: dict[str, Callable[..., object]] = {}


def route(path: str) -> Callable[[Callable[..., object]], Callable[..., object]]:
    def register(fn: Callable[..., object]) -> Callable[..., object]:
        ROUTES[path] = fn
        return fn

    return register


@route("/books")
def list_books(svc: BookService, title: str = "") -> list[dict[str, str]]:
    """GET /books."""
    return [{"id": b.id, "title": b.title} for b in svc.list(BookFilter(title=title))]
