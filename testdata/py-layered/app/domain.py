"""Book model."""

from dataclasses import dataclass


@dataclass
class Book:
    """A catalogue entry."""

    id: str
    title: str


@dataclass
class BookFilter:
    title: str = ""
    limit: int = 20
