// Package pathguard provides path-based access control for file-accessing tools.
//
// This package enforces deny-list restrictions so agents cannot read or write
// sensitive directories (vaults, config) through file tools or bash commands.
// MCP tools bypass these restrictions by design — they are the intended access
// path for protected data.
package pathguard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Guard checks filesystem paths against a deny list.
type Guard struct {
	denied []string
}

// New creates a Guard that blocks access to any path under the given denied
// directories. Each entry must be an absolute path; relative paths are
// silently ignored. Nil or empty denied is a no-op (all paths allowed).
func New(denied []string) *Guard {
	abs := make([]string, 0, len(denied))
	for _, d := range denied {
		if d == "" {
			continue
		}
		a, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		abs = append(abs, a)
	}
	return &Guard{denied: abs}
}

// Check returns an error when path resolves inside any denied directory.
// If the current working directory is itself inside a denied directory,
// the check passes (the user chose to work inside that directory).
func (g *Guard) Check(path string) error {
	if len(g.denied) == 0 {
		return nil
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil
	}

	cwd, _ := os.Getwd()
	for _, d := range g.denied {
		if cwd != "" && strings.HasPrefix(cwd, d+string(filepath.Separator)) {
			continue
		}
		if strings.HasPrefix(abs, d+string(filepath.Separator)) || abs == d {
			return fmt.Errorf("access denied: %s is a protected path (use the appropriate MCP tool)", path)
		}
	}
	return nil
}

// CheckCommand scans a bash command string for unquoted argv tokens that
// resolve under a denied directory.
//
// The implementation tokenises the command in a quote-aware way: text
// enclosed in single or double quotes (including heredoc bodies on
// separate lines) is exempt because it cannot be a path argument the
// shell will hand to a file-touching syscall. Comments (everything after
// an unquoted `#`) are stripped. The remaining tokens are tested only
// when they LOOK like a filesystem path — starting with `/`, `~`,
// `./`, `../`, or containing a `/`. Bare words like `vault` or
// `baphled` are never flagged.
//
// The cwd carve-out from Check applies here too: when the working
// directory is inside a denied root, the user is intentionally working
// in that tree and commands referring to it are allowed.
func (g *Guard) CheckCommand(command string) error {
	if len(g.denied) == 0 {
		return nil
	}

	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()

	for _, tok := range tokenize(command) {
		if !looksLikePath(tok) {
			continue
		}

		expanded := expandHome(tok, home)
		abs, err := filepath.Abs(expanded)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)

		for _, d := range g.denied {
			if cwd != "" && strings.HasPrefix(cwd, d+string(filepath.Separator)) {
				continue
			}
			if strings.HasPrefix(abs, d+string(filepath.Separator)) || abs == d {
				return fmt.Errorf("access denied: command references protected path %s (use the appropriate MCP tool)", d)
			}
		}
	}
	return nil
}

// tokenize splits a bash-ish command into argv-like tokens. It is
// deliberately simpler than a real shell parser:
//
//   - text inside single or double quotes is treated as one opaque
//     chunk that DOES NOT contribute a candidate path (the shell will
//     hand the string to the command verbatim — it is not interpreted
//     as a filesystem reference by the shell itself);
//   - everything after an unquoted `#` to end-of-line is a comment and
//     dropped;
//   - tokens are separated by ASCII whitespace plus the shell
//     redirection metacharacters `<`, `>`, `|`, `&`, `;`, `(`, `)`,
//     backtick, equals — splitting at these lets `cmd >foo` and
//     `K=foo` surface `foo` as its own token without the operator
//     glued to it.
//
// The tokeniser does not attempt to expand variables, command
// substitution, or globs; the goal is "produce candidate path
// arguments" not "produce a faithful argv". Quoted heredoc bodies
// (cat <<EOF … EOF) inherit the quote-skip rule via the leading `"`
// or `'` of the delimiter or via the absence of a path-shaped first
// character — in the common `<<EOF\nbody\nEOF` shape there is no
// quote, so we instead skip any line that follows a heredoc marker
// until we see the marker again. That heredoc handling is sufficient
// to kill the most common false-positive case; rare exotic
// constructs may still leak a token, which is acceptable given the
// fallback Check on the actual filesystem call.
func tokenize(command string) []string {
	var tokens []string
	var current strings.Builder
	inSingle := false
	inDouble := false
	skipHeredoc := false
	heredocMarker := ""

	lines := strings.Split(command, "\n")
	for lineIdx, line := range lines {
		if skipHeredoc {
			if strings.TrimSpace(line) == heredocMarker {
				skipHeredoc = false
				heredocMarker = ""
			}
			continue
		}

		flush := func() {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
		}

		i := 0
		for i < len(line) {
			c := line[i]
			switch {
			case inSingle:
				if c == '\'' {
					inSingle = false
					current.Reset() // discard the contents of a single-quoted run entirely
				}
				i++
			case inDouble:
				if c == '"' {
					inDouble = false
					current.Reset() // discard double-quoted run too
				}
				i++
			case c == '\'':
				flush()
				inSingle = true
				i++
			case c == '"':
				flush()
				inDouble = true
				i++
			case c == '#':
				// unquoted comment to end of line
				flush()
				i = len(line)
			case c == '<' && i+1 < len(line) && line[i+1] == '<':
				// heredoc — consume the marker word on this line, then
				// skip subsequent lines until the marker re-appears.
				flush()
				i += 2
				// skip leading `-` (<<-EOF strips tabs but the marker text is the same)
				if i < len(line) && line[i] == '-' {
					i++
				}
				// skip whitespace
				for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
					i++
				}
				marker := strings.Builder{}
				for i < len(line) {
					mc := line[i]
					if mc == ' ' || mc == '\t' || mc == ';' || mc == '|' || mc == '&' {
						break
					}
					if mc == '\'' || mc == '"' || mc == '\\' {
						i++
						continue
					}
					marker.WriteByte(mc)
					i++
				}
				heredocMarker = marker.String()
				if heredocMarker != "" {
					skipHeredoc = true
				}
			case isSeparator(c):
				flush()
				i++
			default:
				current.WriteByte(c)
				i++
			}
		}
		flush()
		_ = lineIdx
	}

	return tokens
}

// isSeparator reports whether a byte ends the current token. This is a
// superset of POSIX shell whitespace plus the redirection /
// command-grouping metacharacters that the shell uses to delimit words.
func isSeparator(c byte) bool {
	switch c {
	case ' ', '\t', '\r':
		return true
	case '|', '&', ';', '(', ')', '<', '>', '`', '=':
		return true
	}
	return false
}

// looksLikePath returns true when tok looks like a filesystem path the
// shell would hand to a file-touching syscall. A bare word with no `/`
// and no leading `.` or `~` is treated as a command name or argument
// value, not a path.
func looksLikePath(tok string) bool {
	if tok == "" {
		return false
	}
	if strings.HasPrefix(tok, "/") {
		return true
	}
	if strings.HasPrefix(tok, "~") {
		return true
	}
	if strings.HasPrefix(tok, "./") || strings.HasPrefix(tok, "../") {
		return true
	}
	if tok == "." || tok == ".." {
		return true
	}
	if strings.Contains(tok, "/") {
		return true
	}
	return false
}

// expandHome replaces a leading `~` or `$HOME` with the supplied home
// directory. It is intentionally narrow — only the leading form is
// expanded so `~user` or mid-token `$HOME` references are left alone.
func expandHome(tok, home string) string {
	if home == "" {
		return tok
	}
	if tok == "~" {
		return home
	}
	if strings.HasPrefix(tok, "~/") {
		return home + tok[1:]
	}
	if strings.HasPrefix(tok, "$HOME/") {
		return home + tok[len("$HOME"):]
	}
	if tok == "$HOME" {
		return home
	}
	return tok
}
