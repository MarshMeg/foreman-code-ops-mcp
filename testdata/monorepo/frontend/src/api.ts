import axios from "axios";

export type Book = { id: string; title: string };

const api = axios.create({ baseURL: import.meta.env.VITE_API_URL });

/** Creates a book. */
export async function createBook(book: Book): Promise<Book> {
  const res = await api.post<Book>("/books", book);
  return res.data;
}
