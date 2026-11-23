package store

import (
	"path/filepath"
	"strings"
)

// fileDSN builds the SQLite URI for the database file at path with the
// given query string (without the leading "?"; empty for none).
//
// SQLite reads "file:<path>?<query>#<fragment>" as a URI, so a path that
// carries "?" or "#" would open a different file (and may create one or
// smuggle in parameters such as mode=rw), and "%" would be decoded. Those
// three characters are percent-escaped so SQLite decodes them back to the
// literal path. Two more shapes are re-read by SQLite and are neutralised:
// an absolute path is written "file://" + path, so its authority is empty
// (a path starting "//x" would otherwise name host x), and a relative path
// gets a "./" prefix (the name ":memory:" would otherwise open an empty
// in-memory database). Every DSN the store opens for a file is built here.
func fileDSN(path, query string) string {
	p := filepath.ToSlash(path)
	prefix := "file:"
	switch {
	case filepath.IsAbs(path) || strings.HasPrefix(p, "/"):
		prefix = "file://"
		if !strings.HasPrefix(p, "/") {
			p = "/" + p // a Windows drive path: file:///C:/dir/x.db
		}
	case !strings.HasPrefix(p, "./") && !strings.HasPrefix(p, "../"):
		p = "./" + p
	}
	dsn := prefix + strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23").Replace(p)
	if query == "" {
		return dsn
	}
	return dsn + "?" + query
}
