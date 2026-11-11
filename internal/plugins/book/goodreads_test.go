package book

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/allister/allister/internal/plugin"
)

const goodreadsCSV = "\ufeffBook Id,Title,Author,ISBN,ISBN13,My Rating,Number of Pages,Year Published,Original Publication Year,Publisher,Exclusive Shelf,Date Read,My Review\n" +
	"1,\"Dune, Volume One\",Frank Herbert,0441013597,\"=\"\"9780441013593\"\"\",4,300,2005,1965,Ace,read,2024/01/02,Great\n" +
	"2,No ISBN Book,Someone,,,0,200,2020,,,currently-reading,,\n" +
	"3,,Nobody,,,0,,,,,read,,\n" +
	"4,Unknown Shelf,Someone,,,0,,,,,paused,,\n"

func TestGoodreadsCSVMapsRows(t *testing.T) {
	res, err := goodreadsImporter{}.Parse(context.Background(), strings.NewReader(goodreadsCSV))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 2 || len(res.Skipped) != 2 {
		t.Fatalf("entries=%+v skipped=%+v", res.Entries, res.Skipped)
	}
	e := res.Entries[0]
	if e.Kind != KindBook || e.Title != "Dune, Volume One" || e.Status != plugin.Completed ||
		e.ExternalIDs["isbn"] != "9780441013593" || e.ExternalIDs["goodreads"] != "1" ||
		e.Rating == nil || *e.Rating != 8 || e.Progress["pages"] != 300 ||
		e.Fields["author"].Text != "Frank Herbert" || e.Fields["publisher"].Text != "Ace" ||
		e.Fields["year"].Num == nil || *e.Fields["year"].Num != 1965 ||
		e.FinishedOn == nil || e.FinishedOn.Format("2006-01-02") != "2024-01-02" || e.Comment != "Great" {
		t.Fatalf("first = %+v", e)
	}
	e = res.Entries[1]
	if e.Title != "No ISBN Book" || e.Status != plugin.Active || e.ExternalIDs["isbn"] != "" ||
		e.ExternalIDs["goodreads"] != "2" || e.Fields["pages"].Num == nil || *e.Fields["pages"].Num != 200 {
		t.Fatalf("second = %+v", e)
	}
	if res.Skipped[0].Reason != "no title" || !strings.Contains(res.Skipped[1].Reason, "unknown shelf") {
		t.Fatalf("skipped = %+v", res.Skipped)
	}
}

func TestGoodreadsCSVBadHeader(t *testing.T) {
	_, err := goodreadsImporter{}.Parse(context.Background(), strings.NewReader("a,b\n1,2\n"))
	if !errors.Is(err, plugin.ErrBadInput) {
		t.Fatalf("err = %v", err)
	}
}

func TestISBN13Conversion(t *testing.T) {
	if got := canonicalISBN("0441013597"); got != "9780441013593" {
		t.Fatalf("isbn10 = %q", got)
	}
	if got := canonicalISBN("978-0-441-01359-3"); got != "9780441013593" {
		t.Fatalf("isbn13 = %q", got)
	}
	if got := canonicalISBN("not-an-isbn"); got != "" {
		t.Fatalf("invalid = %q", got)
	}
}
