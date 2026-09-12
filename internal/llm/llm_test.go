package llm

import "testing"

func TestDecodeString(t *testing.T) {
	got, err := decode[string]("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %q, want raw text passed through verbatim", got)
	}
}

func TestDecodeStruct(t *testing.T) {
	type doc struct {
		Title string `json:"title"`
	}
	got, err := decode[doc](`{"title":"Carbonara"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Title != "Carbonara" {
		t.Fatalf("got %+v, want Title=Carbonara", got)
	}
}

func TestDecodeStructInvalidJSON(t *testing.T) {
	type doc struct {
		Title string `json:"title"`
	}
	if _, err := decode[doc]("not json"); err == nil {
		t.Fatal("expected an error decoding invalid JSON into a struct, got nil")
	}
}
