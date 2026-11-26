package tool

// Manual symlink resolution for the links found in a protected directory (ADR-012 D20). filepath.EvalSymlinks
// fails on a link whose target does not exist yet, yet a write can create that target, so the link is followed hop
// by hop here and the result is a comparison key only. On Windows the key of the existing prefix is its one
// EvalSymlinks spelling (canonical), since the OS answers to a directory under 8.3 short names as well.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// maxLinkHops bounds the hops of one resolution, as the kernel does (ELOOP at 40).
const maxLinkHops = 40

// resolveLink follows link (an absolute path whose parent is already resolved) one hop at a time. Relative targets
// join against the resolved directory they sit in; the first missing component ends the walk, and what remains is
// appended without resolving, a ".." among it being unknowable.
func resolveLink(link string) (string, linkState) {
	return resolveFrom(filepath.Dir(link), []string{filepath.Base(link)})
}

// resolvePath resolves the whole absolute path abs component by component from its root, so a dangling link on
// any ancestor is followed too (EvalSymlinks fails on one). A path that cannot be resolved (a loop, an unreadable
// component, a ".." after a missing one) comes back lexically cleaned: it is a comparison key only. abs is never
// cleaned before resolving: "lnk/.." must step out of the link's target, not cancel the link.
func resolvePath(abs string) string {
	vol := filepath.VolumeName(abs)
	out, st := resolveFrom(vol+string(filepath.Separator), splitTarget(abs))
	if st != linkResolved {
		return filepath.Clean(abs)
	}
	return out
}

// resolveFrom walks comps from the resolved directory dir, following links hop by hop (see resolveLink). The
// existing prefix it ends on is returned in its canonical spelling.
func resolveFrom(dir string, comps []string) (string, linkState) {
	hops := 0
	for i := 0; i < len(comps); i++ {
		c := comps[i]
		switch c {
		case ".", "":
			continue
		case "..":
			dir = filepath.Dir(dir)
			continue
		}
		next := filepath.Join(dir, c)
		fi, err := os.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) {
			return appendMissing(canonical(dir), c, comps[i+1:])
		}
		if err != nil {
			return dir, linkUnknown
		}
		if fi.Mode()&fs.ModeSymlink == 0 {
			dir = next
			continue
		}
		hops++
		if hops > maxLinkHops {
			return next, linkLoop
		}
		t, err := os.Readlink(next)
		if err != nil {
			return dir, linkUnknown
		}
		if filepath.IsAbs(t) {
			dir = filepath.VolumeName(t) + string(filepath.Separator)
		}
		comps = append(splitTarget(t), comps[i+1:]...)
		i = -1
	}
	return canonical(dir), linkResolved
}

// canonical is the comparison key of an existing directory. On Windows it is the directory's one spelling from
// filepath.EvalSymlinks: the long name in its on-disk case for every component and the drive letter in upper case.
// The OS hands the same directory out under 8.3 short names too, which the environment often carries (%AppData%,
// TEMP on CI: RUNNER~1), while a child's workdir arrives spelled long (confineChildWorkdir resolves it with
// EvalSymlinks); the walk above keeps the caller's spelling, and no case folding bridges runneradmin and RUNNER~1,
// so without this an absent user hook directory protects nothing. A directory EvalSymlinks cannot resolve keeps its
// walked spelling. Elsewhere the walked path is the key as is: every symlink has been followed and within folds case.
func canonical(dir string) string {
	if runtime.GOOS != "windows" {
		return dir
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// appendMissing joins the missing component and the rest onto the resolved dir; a ".." in the rest cannot be
// resolved lexically (MkdirAll would make the missing component first).
func appendMissing(dir, missing string, rest []string) (string, linkState) {
	out := filepath.Join(dir, missing)
	for _, c := range rest {
		if c == ".." {
			return dir, linkUnknown
		}
		out = filepath.Join(out, c)
	}
	return out, linkResolved
}

// splitTarget splits a link target into components, keeping ".." and dropping empty and "." ones.
func splitTarget(t string) []string {
	return pathComponents(t[len(filepath.VolumeName(t)):])
}
