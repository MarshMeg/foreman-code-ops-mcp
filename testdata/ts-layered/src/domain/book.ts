/** A catalogue entry. */
export interface Book {
  id: string;
  title: string;
}

export type BookFilter = { title?: string; limit?: number };
