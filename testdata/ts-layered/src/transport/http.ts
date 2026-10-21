import type { BookService } from "../service/book";

export type Reply = { status: number; body: unknown };

/** GET /books */
export const listBooks = async (svc: BookService, title?: string): Promise<Reply> => {
  const books = await svc.list({ title });
  return { status: 200, body: books };
};
