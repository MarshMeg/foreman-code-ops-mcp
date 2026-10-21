import type { Book, BookFilter } from "../domain/book";

/** Stores books. */
export interface BookRepo {
  list(f: BookFilter): Promise<Book[]>;
}

export class MemRepo implements BookRepo {
  constructor(private readonly books: Book[] = []) {}

  /** Returns books matching f. */
  async list(f: BookFilter): Promise<Book[]> {
    const title = f.title ?? "";
    return this.books.filter((b) => b.title.includes(title)).slice(0, f.limit ?? 20);
  }
}

export function newMemRepo(books: Book[]): BookRepo {
  return new MemRepo(books);
}
