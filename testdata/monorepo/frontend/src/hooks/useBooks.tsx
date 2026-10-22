import type { Book } from "../api";

const API = import.meta.env.VITE_API_URL;

/** Loads all books. */
export async function useBooks(): Promise<Book[]> {
  const res = await fetch(`${API}/books`);
  return (await res.json()) as Book[];
}
