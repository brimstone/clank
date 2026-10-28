// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package db_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brimstone/clank/db"
	embedding "github.com/brimstone/go-embedding"
	vector "github.com/brimstone/go-vector"
)

const (
	alphaName = "alpha.md"
	betaName  = "beta.txt"

	alphaText = `The vector database stores every document as a set of chunks.
Each chunk is embedded into a fixed length vector of floating point
numbers. Search finds the chunks whose vectors are closest to the
vector of the query, which is how the tool can match a question like
how does retrieval work against text that says nearest neighbour search
returns the most similar passages.`

	betaText = `Pasta carbonara is a Roman dish. Bring a large pan of salted
water to a boil and cook the spaghetti for one minute short of the
packet time. In a bowl beat the eggs with pecorino cheese and black
pepper. Fry the guanciale in a little oil until crisp, then drain the
pasta and toss it with the fat, the eggs and most of the cheese.
Finish with extra pepper and serve at once, off the heat, so the eggs
make a sauce instead of scrambling.`
)

func writeDocs(t *testing.T, dir string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, alphaName), []byte(alphaText), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, betaName), []byte(betaText), 0o600); err != nil {
		t.Fatal(err)
	}
}

// topHit returns the doc_id and record ID of the best match for query.
func topHit(t *testing.T, path, query string) (string, string) {
	t.Helper()

	hits, err := db.Query(t.Context(), path, query, 3)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) == 0 {
		t.Fatalf("no hits for %q", query)
	}

	return hits[0].Metadata[embedding.MetaDocID], hits[0].ID
}

func TestInitLoadQuery(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDocs(t, dir)
	dbPath := filepath.Join(dir, "test.vdb")

	if err := db.Init(dbPath, db.ModelHash, 512); err != nil {
		t.Fatal(err)
	}

	var progress int

	rep, err := db.Load(t.Context(), dbPath, dir, db.LoadOptions{
		ChunkSize: 128,
		Overlap:   16,
		Progress: func(string, int) error {
			progress++
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if rep.Model != db.ModelHash || rep.Dims != 512 {
		t.Errorf("report model/dims = %q/%d, want %q/512", rep.Model, rep.Dims, db.ModelHash)
	}
	// The db file itself sits inside dir and must be skipped as binary.
	if rep.Files != 2 || progress != 2 {
		t.Errorf("indexed %d files (progress %d), want 2/2", rep.Files, progress)
	}

	if rep.Chunks < 2 {
		t.Errorf("got %d chunks, want at least 2", rep.Chunks)
	}

	if rep.SkippedBinary != 1 {
		t.Errorf("skipped %d binary, want 1 (the db file itself)", rep.SkippedBinary)
	}

	doc, id := topHit(t, dbPath, "how does retrieval work with nearest neighbour search")
	if doc != alphaName {
		t.Errorf("top hit for retrieval query = %q (%s), want %s", doc, id, alphaName)
	}

	doc, _ = topHit(t, dbPath, "how do I make pasta carbonara with eggs")
	if doc != betaName {
		t.Errorf("top hit for pasta query = %q, want %s", doc, betaName)
	}

	st, err := db.Stats(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if st.Documents != 2 || st.Chunks != rep.Chunks {
		t.Errorf("stats = %d docs / %d chunks, want 2 / %d", st.Documents, st.Chunks, rep.Chunks)
	}
}

func TestReloadReplacesChunks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDocs(t, dir)
	dbPath := filepath.Join(dir, "test.vdb")

	if err := db.Init(dbPath, db.ModelHash, 512); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Load(t.Context(), dbPath, dir, db.LoadOptions{ChunkSize: 128, Overlap: 16}); err != nil {
		t.Fatal(err)
	}

	st, err := db.Stats(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	before := st.Chunks

	// An unchanged re-load must not duplicate anything.
	if _, err := db.Load(t.Context(), dbPath, dir, db.LoadOptions{ChunkSize: 128, Overlap: 16}); err != nil {
		t.Fatal(err)
	}

	st, err = db.Stats(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if st.Chunks != before {
		t.Errorf("chunks after unchanged reload = %d, want %d (no duplicates)", st.Chunks, before)
	}

	// Rewrite one document, re-load, and check its old chunks are gone
	// while the new text is searchable.
	newText := `The refund window is thirty days from the delivery date.
To request one, write to the support desk with the order number and
the reason for the return.`
	if err := os.WriteFile(filepath.Join(dir, alphaName), []byte(newText), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Load(t.Context(), dbPath, dir, db.LoadOptions{ChunkSize: 128, Overlap: 16}); err != nil {
		t.Fatal(err)
	}

	vdb, err := vector.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := vdb.Close(); err != nil {
			t.Error(err)
		}
	}()
	// "docs" is the collection clank db stores chunks in.
	docs, ok := vdb.Collection("docs")
	if !ok {
		t.Fatal("missing docs collection")
	}

	var old, fresh int

	docs.Range(func(r vector.Record) bool {
		if r.Metadata[embedding.MetaDocID] == alphaName {
			if strings.Contains(r.Content, "nearest neighbour") {
				old++
			}

			if strings.Contains(r.Content, "refund") {
				fresh++
			}
		}

		return true
	})

	if old > 0 {
		t.Errorf("%d old alpha chunks survived the reload", old)
	}

	if fresh == 0 {
		t.Error("new alpha content missing after reload")
	}

	doc, _ := topHit(t, dbPath, "what is the refund policy for returns")
	if doc != alphaName {
		t.Errorf("top hit for refund query = %q, want %s", doc, alphaName)
	}
}

func TestInitDimensionMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.vdb")

	if err := db.Init(dbPath, db.ModelHash, 512); err != nil {
		t.Fatal(err)
	}

	if err := db.Init(dbPath, db.ModelHash, 256); err == nil {
		t.Fatal("re-init with a different dimension should fail")
	}
	// The failed re-init must not have broken the database.
	hits, err := db.Query(t.Context(), dbPath, "vector database", 1)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) != 0 {
		t.Errorf("empty database returned %d hits", len(hits))
	}
}

// TestGloveDimensionMismatch builds a database whose docs collection has
// the wrong dimension for the glove model, then expects Query to refuse.
func TestGloveDimensionMismatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.vdb")

	vdb, err := vector.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := vdb.CreateCollection("docs", vector.Options{Dimension: 512}); err != nil {
		t.Fatal(err)
	}

	cfg, err := vdb.CreateCollection("config", vector.Options{Dimension: 1, Index: vector.Flat})
	if err != nil {
		t.Fatal(err)
	}

	if err := cfg.Upsert("model", []float32{0}, db.ModelGlove, nil); err != nil {
		t.Fatal(err)
	}

	if err := vdb.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Query(t.Context(), dbPath, "anything", 1); err == nil {
		t.Fatal("query against a glove db with the wrong dimension should fail")
	}
}

func TestUnknownModel(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "test.vdb")
	if err := db.Init(dbPath, "nope", 512); err == nil {
		t.Fatal("init with an unknown model should fail")
	}
}

func TestMissingFile(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "absent.vdb")
	if _, err := db.Query(t.Context(), dbPath, "anything", 1); err == nil {
		t.Fatal("query on a missing file should fail")
	}
}

func TestLoadAutoInit(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDocs(t, dir)
	dbPath := filepath.Join(t.TempDir(), "auto.vdb")

	rep, err := db.Load(t.Context(), dbPath, dir, db.LoadOptions{ChunkSize: 128, Overlap: 16})
	if err != nil {
		t.Fatal(err)
	}

	if !rep.Created {
		t.Error("load into a missing database should create it")
	}

	if rep.Model != db.ModelGlove {
		t.Errorf("auto-created model = %q, want %q", rep.Model, db.ModelGlove)
	}

	if rep.Files != 2 {
		t.Errorf("indexed %d files, want 2", rep.Files)
	}

	if _, err := os.Stat(dbPath); err != nil {
		t.Fatal(err)
	}

	doc, _ := topHit(t, dbPath, "how does retrieval work with nearest neighbour search")
	if doc != alphaName {
		t.Errorf("top hit for retrieval query = %q, want %s", doc, alphaName)
	}

	// A second load must reuse the existing database.
	rep, err = db.Load(t.Context(), dbPath, dir, db.LoadOptions{ChunkSize: 128, Overlap: 16})
	if err != nil {
		t.Fatal(err)
	}

	if rep.Created {
		t.Error("load into an existing database must not report Created")
	}

	// Model and Dims name the embedding model of a database Load creates.
	other := filepath.Join(t.TempDir(), "hash.vdb")

	rep, err = db.Load(t.Context(), other, dir, db.LoadOptions{Model: db.ModelHash, Dims: 256, ChunkSize: 128, Overlap: 16})
	if err != nil {
		t.Fatal(err)
	}

	if !rep.Created || rep.Model != db.ModelHash || rep.Dims != 256 {
		t.Errorf("hash load report = created:%v model:%q dims:%d, want true/%s/256", rep.Created, rep.Model, rep.Dims, db.ModelHash)
	}

	// An existing database keeps its stored model over opts.
	kept := filepath.Join(t.TempDir(), "keep.vdb")
	if err := db.Init(kept, db.ModelHash, 512); err != nil {
		t.Fatal(err)
	}

	rep, err = db.Load(t.Context(), kept, dir, db.LoadOptions{Model: db.ModelGlove, ChunkSize: 128, Overlap: 16})
	if err != nil {
		t.Fatal(err)
	}

	if rep.Created || rep.Model != db.ModelHash {
		t.Errorf("load into an existing db = created:%v model:%q, want false/%s", rep.Created, rep.Model, db.ModelHash)
	}
}

func TestQueryDir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDocs(t, dir)

	hits, err := db.QueryDir(t.Context(), dir, "how does retrieval work with nearest neighbour search", 3)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) == 0 {
		t.Fatal("no hits")
	}

	if hits[0].Metadata[embedding.MetaDocID] != alphaName {
		t.Errorf("top hit for retrieval query = %q, want %s", hits[0].Metadata[embedding.MetaDocID], alphaName)
	}

	hits, err = db.QueryDir(t.Context(), dir, "how do I make pasta carbonara with eggs", 3)
	if err != nil {
		t.Fatal(err)
	}

	if len(hits) == 0 || hits[0].Metadata[embedding.MetaDocID] != betaName {
		t.Errorf("top hit for pasta query = %q, want %s", metaDoc(t, hits), betaName)
	}

	// The memory-only database must not leave anything in the directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 2 {
		t.Errorf("directory holds %d entries after QueryDir, want 2 (only the documents)", len(entries))
	}

	if _, err := db.QueryDir(t.Context(), filepath.Join(dir, "absent"), "anything", 1); err == nil {
		t.Error("QueryDir on a missing directory should fail")
	}
}

// metaDoc renders the top hit's doc id for an error message.
func metaDoc(t *testing.T, hits []vector.Result) string {
	t.Helper()

	if len(hits) == 0 {
		return "no hits"
	}

	return hits[0].Metadata[embedding.MetaDocID]
}
