// Copyright (c) 2026 Matt Robinson brimstone@the.narro.ws

package prompt

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/brimstone/clank/db"
	embedding "github.com/brimstone/go-embedding"
	vector "github.com/brimstone/go-vector"
)

// PrefixWithDBSearch searches the vector db at dbPath for the chunks closest
// in meaning to prompt and returns them prepended in front of prompt as
// background context. A directory dbPath is indexed on the fly into a
// memory-only database; anything else must be a database file. An empty
// dbPath or prompt, or a search with no hits, returns prompt unchanged.
func PrefixWithDBSearch(ctx context.Context, dbPath, prompt string) (string, error) {
	if dbPath == "" || prompt == "" {
		return prompt, nil
	}

	var (
		hits []vector.Result
		err  error
	)

	if fi, serr := os.Stat(dbPath); serr == nil && fi.IsDir() {
		hits, err = db.QueryDir(ctx, dbPath, prompt, 0)
	} else {
		hits, err = db.Query(ctx, dbPath, prompt, 0)
	}

	if err != nil {
		return "", err
	}

	if len(hits) == 0 {
		return prompt, nil
	}

	var b strings.Builder

	b.WriteString("Relevant context from the vector database, most relevant first. Use it as background for the request that follows.\n\n")

	for i, h := range hits {
		b.WriteString(fmt.Sprintf("%d. %s chunk %s (%4.1f%% similar)\n\n", i+1, h.Metadata[embedding.MetaDocID], h.Metadata[embedding.MetaChunk], float64(h.Similarity())*100))

		b.WriteString(h.Content)
		b.WriteString("\n\n")
	}

	b.WriteString(prompt)

	return b.String(), nil
}
