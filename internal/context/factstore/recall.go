package factstore

import (
	"math"
	"strings"
	"unicode"
)

// stopwords is a small, deliberately conservative list. The recall
// signal is intentionally loose — better to surface a marginal fact
// than to lose a high-value one to over-aggressive filtering.
var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "the": {}, "and": {}, "or": {}, "but": {},
	"is": {}, "are": {}, "was": {}, "were": {}, "be": {}, "been": {},
	"to": {}, "of": {}, "in": {}, "on": {}, "at": {}, "for": {}, "by": {},
	"with": {}, "as": {}, "from": {}, "into": {}, "onto": {},
	"this": {}, "that": {}, "these": {}, "those": {},
	"it": {}, "its": {}, "i": {}, "you": {}, "we": {}, "they": {},
}

// tokenise lowercases s and splits it on non-letter/digit boundaries,
// dropping stopwords and tokens shorter than two characters. The
// result is the bag-of-tokens used by the overlap ranker.
func tokenise(s string) []string {
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, skip := stopwords[f]; skip {
			continue
		}
		out = append(out, f)
	}
	return out
}

// overlapCount returns the number of tokens in q that also appear in f.
// Each q token is counted once even if it appears multiple times in f
// — set semantics keep the score linear in query length.
func overlapCount(q, f []string) int {
	if len(q) == 0 || len(f) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(f))
	for _, t := range f {
		set[t] = struct{}{}
	}
	hits := 0
	seen := make(map[string]struct{}, len(q))
	for _, t := range q {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		if _, ok := set[t]; ok {
			hits++
		}
	}
	return hits
}

// sqrtAtLeastOne returns sqrt(n) clamped at a 1.0 minimum so single-
// token facts do not dominate the ranking.
func sqrtAtLeastOne(n int) float64 {
	if n <= 1 {
		return 1.0
	}
	return math.Sqrt(float64(n))
}
