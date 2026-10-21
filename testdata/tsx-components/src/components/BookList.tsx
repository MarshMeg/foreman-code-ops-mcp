"use client";

export type Book = { id: string; title: string };

/** Loads books (stub). */
export function useBooks(): Book[] {
  return [{ id: "1", title: "Dune" }];
}

export const BookItem = ({ book }: { book: Book }) => <li>{book.title}</li>;

/** Renders the list of books. */
export function BookList({ books }: { books: Book[] }) {
  return (
    <ul>
      {books.map((b) => (
        <BookItem key={b.id} book={b} />
      ))}
    </ul>
  );
}
