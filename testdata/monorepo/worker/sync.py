"""Pulls books from the backend."""

import os

import httpx

BASE = os.environ["BACKEND_URL"]


def fetch_book(book_id: str) -> dict:
    return httpx.get(f"{BASE}/books/{book_id}").json()
