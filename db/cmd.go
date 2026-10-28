// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package db

import (
	"fmt"
	"log/slog"
	"os"
	"sort"

	"github.com/brimstone/go-embedding/glove"
	"github.com/brimstone/go-vector"
	"github.com/spf13/cobra"
)

// Cmd returns the db command and its subcommands.
func Cmd() *cobra.Command {
	var cmd = &cobra.Command{
		Use:   "db",
		Short: "Search text documents with a local vector db",
		Long: `Manage a single-file vector database of text documents.

The database stores embeddings of the text files under a directory, so
you can search by meaning rather than exact words. Point load at a
directory to index it (the database is created if it does not exist),
then query it. Query can also take a directory, which is indexed on the
fly in memory and needs no database file at all.`,
	}

	var initCmd = &cobra.Command{
		Use:   "init FILE",
		Short: "Create a new vector db",
		Long: `Create a new vector db file.

The model names the embedding function: glove matches text by meaning
using bundled GloVe word vectors (300 dimensions, no network); hash
matches by shared words and letter patterns, with the dimension set by
--dims. The model is stored in the file, so load and query read it back;
load takes --model and --dims only when it has to create a missing file.`,
		Args: cobra.ExactArgs(1),
		RunE: InitRun,
	}

	initCmd.Flags().String("model", ModelGlove, "embedding model: glove (meaning) or hash (lexical)")
	initCmd.Flags().Int("dims", DefaultHashDims, "vector dimension for the hash model")

	var loadCmd = &cobra.Command{
		Use:   "load FILE DIR",
		Short: "Index the text documents in DIR",
		Long: `Index the text documents under DIR into the database at FILE.

If FILE does not exist, it is created first with the given model, so
init is not required. When FILE already exists, its stored model wins
and --model and --dims are ignored.

Every regular file is read, split into chunks of about --chunk-size
characters (neighbouring chunks share --overlap characters), embedded
and stored with its path and byte offsets. Files that contain binary
data, are empty, or are larger than --max-size bytes are skipped.
Hidden files and directories are skipped unless --hidden is set; --ext
restricts the load to the given file extensions (repeatable).

Loading a document again replaces its previous chunks, so re-running
load after editing a file keeps the database current.`,
		Args: cobra.ExactArgs(2),
		RunE: LoadRun,
	}

	loadCmd.Flags().String("model", ModelGlove, "embedding model used when FILE is created: glove (meaning) or hash (lexical)")
	loadCmd.Flags().Int("dims", DefaultHashDims, "vector dimension for the hash model when FILE is created")
	loadCmd.Flags().Int("chunk-size", DefaultChunkSize, "chunk size in characters")
	loadCmd.Flags().Int("overlap", DefaultOverlap, "characters shared between neighbouring chunks")
	loadCmd.Flags().StringSlice("ext", nil, "file extension to index, e.g. .md (default: all text files)")
	loadCmd.Flags().Bool("hidden", false, "include hidden files and directories")
	loadCmd.Flags().Int64("max-size", DefaultMaxBytes, "skip files larger than this many bytes")
	loadCmd.Flags().Int("save-every", defaultSaveEvery, "save the database every N files")

	var queryCmd = &cobra.Command{
		Use:   "query TARGET TEXT",
		Short: "Search a database or a directory for TEXT",
		Long: `Search the database at TARGET for the chunks closest in meaning to TEXT.

If TARGET is a directory, it is searched without a database file: the
text documents under it are indexed on the fly into a memory-only
database, so init and load happen automatically and nothing is written
to disk. For a database that persists between runs, use load.

Prints the --k best matches with their similarity, source file, chunk
number and byte offsets.`,
		Args: cobra.ExactArgs(2),
		RunE: QueryRun,
	}

	queryCmd.Flags().Int("k", 5, "number of results")

	var statsCmd = &cobra.Command{
		Use:   "stats FILE",
		Short: "Show database statistics",
		Long:  `Show the size and contents of the database at FILE.`,
		Args:  cobra.ExactArgs(1),
		RunE:  StatsRun,
	}

	cmd.AddCommand(initCmd, loadCmd, queryCmd, statsCmd)

	return cmd
}

func InitRun(cmd *cobra.Command, args []string) error {
	model, err := cmd.Flags().GetString("model")
	if err != nil {
		return err
	}

	dims, err := cmd.Flags().GetInt("dims")
	if err != nil {
		return err
	}

	dim, err := dimFor(model, dims)
	if err != nil {
		return err
	}

	if model == ModelGlove && cmd.Flags().Changed("dims") {
		slog.Warn("Ignoring --dims", "reason", "the glove model has a fixed dimension", "dims", glove.Dimension)
	}

	if err := Init(args[0], model, dims); err != nil {
		slog.Error("Unable to initialize db", "err", err)

		return err
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "initialized %s (model=%s, dims=%d)\n", args[0], model, dim); err != nil {
		return err
	}

	return nil
}

func LoadRun(cmd *cobra.Command, args []string) error {
	model, err := cmd.Flags().GetString("model")
	if err != nil {
		return err
	}

	dims, err := cmd.Flags().GetInt("dims")
	if err != nil {
		return err
	}

	if model == ModelGlove && cmd.Flags().Changed("dims") {
		slog.Warn("Ignoring --dims", "reason", "the glove model has a fixed dimension", "dims", glove.Dimension)
	}

	chunkSize, err := cmd.Flags().GetInt("chunk-size")
	if err != nil {
		return err
	}

	overlap, err := cmd.Flags().GetInt("overlap")
	if err != nil {
		return err
	}

	exts, err := cmd.Flags().GetStringSlice("ext")
	if err != nil {
		return err
	}

	hidden, err := cmd.Flags().GetBool("hidden")
	if err != nil {
		return err
	}

	maxSize, err := cmd.Flags().GetInt64("max-size")
	if err != nil {
		return err
	}

	saveEvery, err := cmd.Flags().GetInt("save-every")
	if err != nil {
		return err
	}

	rep, err := Load(cmd.Context(), args[0], args[1], LoadOptions{
		Model:         model,
		Dims:          dims,
		ChunkSize:     chunkSize,
		Overlap:       overlap,
		Exts:          exts,
		IncludeHidden: hidden,
		MaxBytes:      maxSize,
		SaveEvery:     saveEvery,
		Progress: func(rel string, n int) error {
			_, err := fmt.Fprintf(cmd.ErrOrStderr(), "%s: %d chunks\n", rel, n)

			return err
		},
	})
	if err != nil {
		slog.Error("Unable to load directory", "err", err)

		return err
	}

	w := cmd.OutOrStdout()

	if rep.Created {
		if _, err := fmt.Fprintf(w, "created %s (model=%s, dims=%d)\n", args[0], rep.Model, rep.Dims); err != nil {
			return err
		}
	}

	if _, err := fmt.Fprintf(w, "indexed %d files, %d chunks into %s (model=%s, dims=%d)\n", rep.Files, rep.Chunks, args[0], rep.Model, rep.Dims); err != nil {
		return err
	}

	var skipped []string

	if rep.SkippedBinary > 0 {
		skipped = append(skipped, fmt.Sprintf("%d binary", rep.SkippedBinary))
	}

	if rep.SkippedLarge > 0 {
		skipped = append(skipped, fmt.Sprintf("%d too large", rep.SkippedLarge))
	}

	if rep.SkippedEmpty > 0 {
		skipped = append(skipped, fmt.Sprintf("%d empty", rep.SkippedEmpty))
	}

	if len(skipped) > 0 {
		if _, err := fmt.Fprintf(w, "skipped %s\n", join(skipped)); err != nil {
			return err
		}
	}

	if st, err := Stats(args[0]); err == nil {
		if _, err := fmt.Fprintf(w, "database %s\n", humanBytes(st.FileSize)); err != nil {
			return err
		}
	}

	return nil
}

func QueryRun(cmd *cobra.Command, args []string) error {
	k, err := cmd.Flags().GetInt("k")
	if err != nil {
		return err
	}

	var hits []vector.Result

	// A directory target is indexed on the fly into a memory-only
	// database; anything else is looked up as a database file.
	if fi, serr := os.Stat(args[0]); serr == nil && fi.IsDir() {
		hits, err = QueryDir(cmd.Context(), args[0], args[1], k)
	} else {
		hits, err = Query(cmd.Context(), args[0], args[1], k)
	}

	if err != nil {
		slog.Error("Unable to query db", "err", err)

		return err
	}

	w := cmd.OutOrStdout()

	if len(hits) == 0 {
		_, err := fmt.Fprintln(w, "no results")

		return err
	}

	for i, h := range hits {
		chunk := h.Metadata["chunk"]
		start := h.Metadata["start"]
		end := h.Metadata["end"]

		if _, err := fmt.Fprintf(w, "%d. %4.1f%%  %s  chunk %s  [%s:%s]\n", i+1, float64(h.Similarity())*100, h.Metadata["doc_id"], chunk, start, end); err != nil {
			return err
		}

		if _, err := fmt.Fprintln(w, "   "+h.Content); err != nil {
			return err
		}
	}

	return nil
}

func StatsRun(cmd *cobra.Command, args []string) error {
	st, err := Stats(args[0])
	if err != nil {
		slog.Error("Unable to read db", "err", err)

		return err
	}

	w := cmd.OutOrStdout()

	if _, err := fmt.Fprintf(w, "database %s (%s)\n", st.Path, humanBytes(st.FileSize)); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "model    %s\n", st.Model); err != nil {
		return err
	}

	if _, err := fmt.Fprintf(w, "documents %d (%d chunks)\n", st.Documents, st.Chunks); err != nil {
		return err
	}

	if len(st.Cols) > 1 {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}

		for i, c := range st.Cols {
			if i == 0 {
				if _, err := fmt.Fprintf(w, "%-10s %8s %10s %6s %6s %10s\n", "collection", "records", "tombstones", "dims", "index", "vectors"); err != nil {
					return err
				}
			}

			if _, err := fmt.Fprintf(w, "%-10s %8d %10d %6d %6s %10s\n", c.Name, c.Live, c.Tombstones, c.Dimension, c.Index, humanBytes(int64(c.VectorBytes))); err != nil {
				return err
			}
		}
	}

	return nil
}

// humanBytes renders a byte count in the nearest binary unit.
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}

	const unit = 1024

	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// join renders a short list as "a, b and c".
func join(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		sorted := append([]string(nil), items...)
		sort.Strings(sorted)

		return sorted[0] + ", " + sorted[1] + " and " + sorted[2]
	}
}
