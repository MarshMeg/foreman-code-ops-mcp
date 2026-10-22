//! Crate docs.
use std::collections::HashMap;
use std::fmt::{self, Display};

/// A book in the catalog.
#[derive(Debug, Clone)]
pub struct Book {
    pub id: u64,
    pub title: String,
}

pub enum Genre {
    Fiction,
    Science,
}

pub type Catalog = HashMap<u64, Book>;

pub const MAX_BOOKS: usize = 100;

/// Storage for books.
pub trait Repo {
    fn get(&self, id: u64) -> Option<Book>;

    /// Number of books.
    fn count(&self) -> usize {
        0
    }
}

impl Book {
    /// Creates a book.
    pub fn new(id: u64, title: &str) -> Self {
        Self { id, title: title.to_string() }
    }

    pub fn is_empty(&self) -> bool {
        self.title.is_empty()
    }
}

impl Display for Book {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}", self.title)
    }
}

pub struct Wrapper<T>(T);

impl<T: Clone> Wrapper<T> {
    fn inner(&self) -> T {
        self.0.clone()
    }
}

macro_rules! book {
    ($id:expr) => {
        Book::new($id, "")
    };
}

// Finds a book by title.
pub async fn find(books: &[Book], title: &str) -> Option<Book> {
    books.iter().find(|b| b.title == title).cloned()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn new_sets_title() {
        assert_eq!(Book::new(1, "a").title, "a");
    }
}
