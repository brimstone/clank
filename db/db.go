// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

// Package db implements clank db: a single-file vector database of text
// documents. Storage and search come from github.com/brimstone/go-vector;
// chunking and embedding come from github.com/brimstone/go-embedding.
//
// A database file holds two collections. The "docs" collection stores one
// record per text chunk, with the chunk text as content and the source
// file (relative to the loaded directory), chunk index and byte offsets as
// metadata. The "config" collection stores database settings as
// single-component records; today it holds only the embedding model name.
package db

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	embedding "github.com/brimstone/go-embedding"
	"github.com/brimstone/go-embedding/glove"
	vector "github.com/brimstone/go-vector"
)

const (
	// collectionDocs holds the embedded document chunks.
	collectionDocs = "docs"
	// collectionConfig holds database settings.
	collectionConfig = "config"
	// ConfigKeyModel is the config record that names the embedding model.
	ConfigKeyModel = "model"

	ModelGlove = "glove"
	ModelHash  = "hash"

	// Default chunk size in runes, with this much shared between neighbours.
	DefaultChunkSize = 512
	DefaultOverlap   = 64
	// DefaultHashDims is the hash model dimension when --dims is not given.
	DefaultHashDims = 512
	// DefaultMaxBytes skips files larger than this.
	DefaultMaxBytes = int64(10 << 20)
	// defaultSaveEvery is how many files are indexed between saves, so an
	// interrupted load loses at most that many files of work.
	defaultSaveEvery = 100
)

// LoadOptions configures a Load run. Zero values take sensible defaults.
type LoadOptions struct {
	ChunkSize     int
	Overlap       int
	Exts          []string // file extensions to keep; empty means all non-binary files
	IncludeHidden bool
	MaxBytes      int64
	SaveEvery     int
	// Model and Dims name the embedding model to create a missing database
	// with; zero values take the defaults (glove, DefaultHashDims). They are
	// ignored when the database already exists, which keeps its stored model.
	Model string
	Dims  int
	// Progress, if non-nil, is called once per indexed file with the
	// file's path relative to the loaded directory and its chunk count.
	Progress func(rel string, chunks int) error
}

// LoadReport summarizes a Load run.
type LoadReport struct {
	Model         string
	Dims          int
	Files         int
	Chunks        int
	SkippedBinary int
	SkippedLarge  int
	SkippedEmpty  int
	// Created is true when the database did not exist and Load created it.
	Created bool
}

// Init creates the database file at path, or re-initializes an existing
// one. model is stored in the config collection and used by Load and
// Query; dims sets the hash model dimension and is ignored for glove,
// whose dimension is fixed.
func Init(path, model string, dims int) error {
	dim, err := dimFor(model, dims)
	if err != nil {
		return err
	}

	vdb, err := vector.Open(path)
	if err != nil {
		return err
	}

	if err := initCollections(vdb, model, dim); err != nil {
		return closeVDB(vdb, fmt.Errorf("%s: %w", path, err))
	}

	return closeVDB(vdb, nil)
}

// initCollections creates the docs and config collections and stores the
// model name.
func initCollections(vdb *vector.DB, model string, dim int) error {
	// Verify the docs collection before touching config: a dimension
	// mismatch fails here, so a failed re-init never leaves the stored
	// model disagreeing with the stored vectors.
	if _, err := vdb.GetOrCreateCollection(collectionDocs, vector.Options{Dimension: dim}); err != nil {
		return err
	}

	cfg, err := vdb.GetOrCreateCollection(collectionConfig, vector.Options{Dimension: 1, Index: vector.Flat})
	if err != nil {
		return err
	}

	if err := cfg.Upsert(ConfigKeyModel, []float32{0}, model, nil); err != nil {
		return err
	}

	return vdb.Save()
}

// closeVDB closes the database, surfacing a close failure only when the
// operation it guards succeeded.
func closeVDB(vdb *vector.DB, err error) error {
	if cerr := vdb.Close(); cerr != nil && err == nil {
		return cerr
	}

	return err
}

// openForUse opens an initialized database and returns its model name and
// docs collection.
func openForUse(path string) (*vector.DB, string, *vector.Collection, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil, "", nil, fmt.Errorf("%s: no such file, create it with 'clank db init' or 'clank db load'", path)
	}

	vdb, err := vector.Open(path)
	if err != nil {
		return nil, "", nil, err
	}

	model, err := configModel(vdb)
	if err != nil {
		return nil, "", nil, closeVDB(vdb, err)
	}

	docs, ok := vdb.Collection(collectionDocs)
	if !ok {
		err := fmt.Errorf("%s: missing %s collection, run 'clank db init %s' first", path, collectionDocs, path)

		return nil, "", nil, closeVDB(vdb, err)
	}

	return vdb, model, docs, nil
}

// openOrCreate opens an existing database, or creates and initializes one
// with model and dims when the file does not exist yet, returning its model
// name and docs collection. An empty path opens a memory-only database,
// which is always created. The created result reports whether a new
// database was initialized.
func openOrCreate(path, model string, dims int) (*vector.DB, string, *vector.Collection, bool, error) {
	fi, err := os.Stat(path)
	if err == nil {
		if fi.IsDir() {
			return nil, "", nil, false, fmt.Errorf("%s: is a directory", path)
		}

		vdb, model, docs, err := openForUse(path)

		return vdb, model, docs, false, err
	}

	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", nil, false, err
	}

	if model == "" {
		model = ModelGlove
	}

	dim, err := dimFor(model, dims)
	if err != nil {
		return nil, "", nil, false, err
	}

	vdb, err := vector.Open(path)
	if err != nil {
		return nil, "", nil, false, err
	}

	if err := initCollections(vdb, model, dim); err != nil {
		if path != "" {
			err = fmt.Errorf("%s: %w", path, err)
		}

		return nil, "", nil, false, closeVDB(vdb, err)
	}

	docs, ok := vdb.Collection(collectionDocs)
	if !ok {
		return nil, "", nil, false, closeVDB(vdb, fmt.Errorf("%s: missing %s collection", path, collectionDocs))
	}

	return vdb, model, docs, true, nil
}

// errNotInitialized is returned when a database file exists but was not
// created with Init.
var errNotInitialized = errors.New("not initialized, run 'clank db init' first")

// configModel reads the embedding model name from the config collection.
func configModel(vdb *vector.DB) (string, error) {
	cfg, ok := vdb.Collection(collectionConfig)
	if !ok {
		return "", errNotInitialized
	}

	rec, ok := cfg.Get(ConfigKeyModel)
	if !ok {
		return "", errNotInitialized
	}

	return rec.Content, nil
}

// dimFor resolves the vector dimension a model implies.
func dimFor(model string, dims int) (int, error) {
	switch model {
	case ModelGlove:
		return glove.Dimension, nil
	case ModelHash:
		if dims <= 0 {
			return 0, errors.New("--dims must be positive")
		}

		return dims, nil
	default:
		return 0, fmt.Errorf("unknown model %q, want %q or %q", model, ModelGlove, ModelHash)
	}
}

// embedderFor builds the embedder a model and dimension imply.
//
//nolint:ireturn // the concrete type depends on the model chosen at init
func embedderFor(model string, dim int) (embedding.Embedder, error) {
	switch model {
	case ModelGlove:
		e, err := glove.Embedder()
		if err != nil {
			return nil, err
		}

		if dim != glove.Dimension {
			return nil, fmt.Errorf("database dimension %d does not match glove's %d", dim, glove.Dimension)
		}

		return e, nil
	case ModelHash:
		opts := embedding.DefaultHashOptions()
		opts.Dimension = dim

		return embedding.NewHashEmbedder(opts)
	default:
		return nil, fmt.Errorf("unknown model %q, want %q or %q", model, ModelGlove, ModelHash)
	}
}

// Load indexes the text documents under dir into the database at path. If
// the database does not exist yet, it is created first with the model from
// opts (glove by default), so init is not required. Each document's
// previous chunks are removed before the new ones are stored, so re-running
// Load after editing a file keeps the database current.
func Load(ctx context.Context, path, dir string, opts LoadOptions) (*LoadReport, error) {
	vdb, model, docs, created, err := openOrCreate(path, opts.Model, opts.Dims)
	if err != nil {
		return nil, err
	}

	emb, err := embedderFor(model, docs.Dimension())
	if err != nil {
		return nil, closeVDB(vdb, err)
	}

	rep, err := ingestDir(ctx, vdb, docs, emb, model, dir, opts)
	if rep != nil {
		rep.Created = created
	}

	return rep, closeVDB(vdb, err)
}

// QueryDir searches the text documents under dir for the k chunks closest in
// meaning to q. The documents are indexed on the fly into a memory-only
// database, so nothing is written to disk; use Load for a database that
// persists between runs.
func QueryDir(ctx context.Context, dir, q string, k int) ([]vector.Result, error) {
	if k <= 0 {
		k = 5
	}

	vdb, model, docs, _, err := openOrCreate("", ModelGlove, 0)
	if err != nil {
		return nil, err
	}

	emb, err := embedderFor(model, docs.Dimension())
	if err != nil {
		return nil, closeVDB(vdb, err)
	}

	if _, err := ingestDir(ctx, vdb, docs, emb, model, dir, LoadOptions{}); err != nil {
		return nil, closeVDB(vdb, err)
	}

	hits, err := docs.SearchText(ctx, emb, q, k)

	return hits, closeVDB(vdb, err)
}

// ingestDir indexes the text documents under dir into the docs collection,
// saving every SaveEvery files and once more at the end.
func ingestDir(ctx context.Context, vdb *vector.DB, docs *vector.Collection, emb embedding.Embedder, model, dir string, opts LoadOptions) (*LoadReport, error) {
	chunkSize, overlap := opts.ChunkSize, opts.Overlap
	if chunkSize <= 0 {
		chunkSize = DefaultChunkSize
	}

	if overlap <= 0 {
		overlap = DefaultOverlap
	}

	chunker, err := embedding.NewRecursiveChunker(chunkSize, overlap)
	if err != nil {
		return nil, err
	}

	pipeline, err := embedding.NewPipeline(chunker, emb, embedding.PipelineOptions{
		Store: &collectionStore{docs},
	})
	if err != nil {
		return nil, err
	}

	files, skips, err := findTextFiles(dir, opts)
	if err != nil {
		return nil, err
	}

	saveEvery := opts.SaveEvery
	if saveEvery <= 0 {
		saveEvery = defaultSaveEvery
	}

	rep := &LoadReport{
		Model:         model,
		Dims:          docs.Dimension(),
		SkippedBinary: skips.binary,
		SkippedLarge:  skips.large,
		SkippedEmpty:  skips.empty,
	}

	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return rep, err
		}

		rel, err := filepath.Rel(dir, f)
		if err != nil {
			return rep, err
		}

		rel = filepath.ToSlash(rel)

		//nolint:gosec // path comes from walking the directory the user named
		content, err := os.ReadFile(f)
		if err != nil {
			return rep, err
		}

		docs.DeleteWhere(vector.Where(embedding.MetaDocID, rel))

		recs, err := pipeline.Ingest(ctx, rel, string(content), nil)
		if err != nil {
			return rep, fmt.Errorf("%s: %w", rel, err)
		}

		rep.Files++
		rep.Chunks += len(recs)

		if opts.Progress != nil {
			if err := opts.Progress(rel, len(recs)); err != nil {
				return rep, err
			}
		}

		if (i+1)%saveEvery == 0 {
			if err := vdb.Save(); err != nil {
				return rep, err
			}
		}
	}

	return rep, vdb.Save()
}

// Query searches the database at path for the k chunks closest in meaning
// to q, using the model stored in the database.
func Query(ctx context.Context, path, q string, k int) ([]vector.Result, error) {
	if k <= 0 {
		k = 5
	}

	vdb, model, docs, err := openForUse(path)
	if err != nil {
		return nil, err
	}

	emb, err := embedderFor(model, docs.Dimension())
	if err != nil {
		return nil, closeVDB(vdb, err)
	}

	hits, err := docs.SearchText(ctx, emb, q, k)

	return hits, closeVDB(vdb, err)
}

// ColStats is one collection's storage statistics.
type ColStats struct {
	Name        string
	Live        int
	Tombstones  int
	Dimension   int
	Index       string
	Metric      string
	VectorBytes int
}

// DBStats describes the whole database.
type DBStats struct {
	Path      string
	Model     string
	Documents int // distinct source files in the docs collection
	Chunks    int // live records in the docs collection
	FileSize  int64
	Cols      []ColStats
}

// Stats reports the size and contents of the database at path.
func Stats(path string) (*DBStats, error) {
	vdb, model, docs, err := openForUse(path)
	if err != nil {
		return nil, err
	}

	st := &DBStats{Path: path, Model: model, Chunks: docs.Len()}
	if fi, err := os.Stat(path); err == nil {
		st.FileSize = fi.Size()
	}

	seen := map[string]struct{}{}

	docs.Range(func(r vector.Record) bool {
		id := r.Metadata[embedding.MetaDocID]
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			st.Documents++
		}

		return true
	})

	for _, name := range vdb.Collections() {
		c, _ := vdb.Collection(name)
		s := c.Stats()
		o := c.Options()
		st.Cols = append(st.Cols, ColStats{
			Name:        name,
			Live:        s.Live,
			Tombstones:  s.Tombstones,
			Dimension:   s.Dimension,
			Index:       o.Index.String(),
			Metric:      o.Metric.String(),
			VectorBytes: s.VectorBytes,
		})
	}

	return st, closeVDB(vdb, nil)
}

// collectionStore adapts a vector.Collection to the embedding.Store
// interface so an embedding.Pipeline can write chunks straight into the
// database.
type collectionStore struct {
	c *vector.Collection
}

func (s *collectionStore) UpsertBatch(recs []embedding.Record) error {
	vrs := make([]vector.Record, len(recs))
	for i, r := range recs {
		vrs[i] = vector.Record{
			ID:       r.ID,
			Vector:   r.Vector,
			Content:  r.Metadata[embedding.MetaText],
			Metadata: vector.Metadata(r.Metadata),
		}
	}

	return s.c.UpsertBatch(vrs)
}

type fileSkips struct{ binary, large, empty int }

// findTextFiles walks dir and returns the files Load should index, in
// path order, plus how many were skipped and why. The root directory is
// always walked, even when it is hidden, because the user named it.
func findTextFiles(dir string, opts LoadOptions) ([]string, fileSkips, error) {
	fi, err := os.Stat(dir)
	if err != nil {
		return nil, fileSkips{}, err
	}

	if !fi.IsDir() {
		return nil, fileSkips{}, fmt.Errorf("%s: not a directory", dir)
	}

	exts := map[string]bool{}

	for _, e := range opts.Exts {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}

		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}

		exts[e] = true
	}

	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}

	var (
		files []string
		skips fileSkips
	)

	first := true

	walkErr := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if first {
			first = false

			return nil
		}

		hidden := strings.HasPrefix(d.Name(), ".")
		if d.IsDir() {
			if hidden && !opts.IncludeHidden {
				return filepath.SkipDir
			}

			return nil
		}

		if !d.Type().IsRegular() || (hidden && !opts.IncludeHidden) {
			return nil
		}

		if len(exts) > 0 && !exts[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch {
		case info.Size() == 0:
			skips.empty++
		case info.Size() > maxBytes:
			skips.large++
		case isBinary(p):
			skips.binary++
		default:
			files = append(files, p)
		}

		return nil
	})
	if walkErr != nil {
		return nil, skips, walkErr
	}

	sort.Strings(files)

	return files, skips, nil
}

// isBinary reports whether the file starts with binary data, using the
// standard "NUL byte in the first 8 KiB" test. It catches compiled files,
// images and archives; UTF-16 text is also caught and skipped.
func isBinary(path string) bool {
	//nolint:gosec // path comes from walking the directory the user named
	f, err := os.Open(path)
	if err != nil {
		return true
	}

	defer func() {
		// Read-only probe; a failed close means nothing for the result.
		_ = f.Close()
	}()

	buf := make([]byte, 8192)
	n, _ := f.Read(buf)

	return bytes.IndexByte(buf[:n], 0) >= 0
}
