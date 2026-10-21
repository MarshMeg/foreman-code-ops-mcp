import type { Book, BookFilter } from "../domain/book";
import type { BookRepo } from "../repo/book";

/** Book use cases. */
export class BookService {
  constructor(private readonly repo: BookRepo) {}

  async list(f: BookFilter): Promise<Book[]> {
    return this.repo.list({ limit: 20, ...f });
  }

  async count(): Promise<number> {
    const books = await this.list({ limit: 1000 });
    return books.length;
  }
}
