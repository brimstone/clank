// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brimstone/clank/db"
	"github.com/brimstone/clank/prompt"
)

const (
	testDocA = `The vector database stores every document as a set of chunks.
Each chunk is embedded into a fixed length vector of floating point
numbers. Search finds the chunks whose vectors are closest to the
vector of the query, which is how the tool can match a question like
how does retrieval work against text that says nearest neighbour search
returns the most similar passages.`

	testDocB = `Pasta carbonara is a Roman dish. Bring a large pan of salted
water to a boil and cook the spaghetti for one minute short of the
packet time. In a bowl beat the eggs with pecorino cheese and black
pepper. Fry the guanciale in a little oil until crisp, then drain the
pasta and toss it with the fat, the eggs and most of the cheese.`
)

func writeTestDocs(t *testing.T, dir string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, "alpha.md"), []byte(testDocA), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "beta.txt"), []byte(testDocB), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrefixWithDBSearchEmpty(t *testing.T) {
	t.Parallel()

	got, err := prompt.PrefixWithDBSearch(t.Context(), "", "a question")
	if err != nil {
		t.Fatal(err)
	}

	if got != "a question" {
		t.Errorf("no db = %q, want the prompt unchanged", got)
	}

	got, err = prompt.PrefixWithDBSearch(t.Context(), t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}

	if got != "" {
		t.Errorf("empty prompt = %q, want it unchanged", got)
	}
}

func TestPrefixWithDBSearchDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestDocs(t, dir)

	const q = "how does retrieval work with nearest neighbour search"

	got, err := prompt.PrefixWithDBSearch(t.Context(), dir, q)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(got, "Relevant context from the vector database") {
		t.Errorf("prefixed prompt = %q, want the context header first", got)
	}

	if !strings.Contains(got, "vector database stores every document") {
		t.Error("prefixed prompt is missing context from the directory")
	}

	if !strings.HasSuffix(got, q) {
		t.Error("prefixed prompt must end with the original prompt")
	}
}

func TestPrefixWithDBSearchFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeTestDocs(t, dir)
	dbPath := filepath.Join(t.TempDir(), "test.vdb")

	if _, err := db.Load(t.Context(), dbPath, dir, db.LoadOptions{ChunkSize: 128, Overlap: 16}); err != nil {
		t.Fatal(err)
	}

	const q = "how do I make pasta carbonara with eggs"

	got, err := prompt.PrefixWithDBSearch(t.Context(), dbPath, q)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(got, "Relevant context from the vector database") {
		t.Errorf("prefixed prompt = %q, want the context header first", got)
	}

	if !strings.Contains(got, "carbonara") {
		t.Error("prefixed prompt is missing context from the database")
	}

	if !strings.HasSuffix(got, q) {
		t.Error("prefixed prompt must end with the original prompt")
	}
}

func TestPrefixWithDBSearchMissing(t *testing.T) {
	t.Parallel()

	if _, err := prompt.PrefixWithDBSearch(t.Context(), filepath.Join(t.TempDir(), "absent.vdb"), "a question"); err == nil {
		t.Fatal("search of a missing database should fail")
	}
}

func TestCmdHasDBFlag(t *testing.T) {
	t.Parallel()

	f := prompt.Cmd().Flags().Lookup("db")
	if f == nil || f.DefValue != "" {
		t.Fatalf("--db flag = %#v, want a string flag with an empty default", f)
	}
}
