import { MemRepo } from "../src/repo/book";
import { BookService } from "../src/service/book";

describe("BookService", () => {
  it("counts books", async () => {
    const svc = new BookService(new MemRepo([{ id: "1", title: "Dune" }, { id: "2", title: "Emma" }]));
    expect(await svc.count()).toBe(2);
  });
});
