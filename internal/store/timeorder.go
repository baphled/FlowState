package store

import (
	"fmt"
	"sort"
	"time"
)

// timestampLayout is the fixed-width UTC layout for stored timestamps: exactly
// nine fractional digits and a literal Z, so the strings sort chronologically.
// time.RFC3339Nano trims trailing zeros and does not.
const timestampLayout = "2006-01-02T15:04:05.000000000Z"

// sortChronological stably orders items by the parsed time, then by id.
// Rows written before the fixed-width layout carry variable-width strings
// whose SQL text order is not chronological, so reads re-sort after the query.
func sortChronological[T any](items []T, key func(T) (time.Time, string)) {
	sort.SliceStable(items, func(i, j int) bool {
		ti, idi := key(items[i])
		tj, idj := key(items[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return idi < idj
	})
}

// parseStored parses a stored timestamp column. An unparseable value is an
// error naming the row and column, never a silent zero time that would sort
// ahead of every real row.
func parseStored(id, column, raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("row %s: parse %s: %w", id, column, err)
	}
	return t, nil
}
