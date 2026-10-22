// Catalog types.
using System;
using System.Collections.Generic;

namespace Books.Catalog;

/// <summary>A book.</summary>
public record Book(int Id, string Title);

public enum Genre { Fiction, Science }

/// <summary>Storage for books.</summary>
public interface IBookRepo
{
    Book? Get(int id);
    int Count { get; }
}

[Serializable]
public class BookService : IBookRepo
{
    private readonly Dictionary<int, Book> _books = new();

    /// <summary>Creates the service.</summary>
    public BookService(IEnumerable<Book> books)
    {
        foreach (var b in books) _books[b.Id] = b;
    }

    public int Count => _books.Count;

    /// <summary>Finds a book.</summary>
    [Obsolete("use Find")]
    public Book? Get(int id)
    {
        return _books.TryGetValue(id, out var b) ? b : null;
    }

    public async Task<List<Book>> ListAsync() => await Task.FromResult(new List<Book>(_books.Values));

    private class Cache
    {
        public void Clear() { }
    }
}

public struct Point
{
    public int X;
}

public delegate void BookAdded(Book book);
