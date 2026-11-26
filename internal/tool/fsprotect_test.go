package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type protectLayout struct {
	workdir   string
	protected []string
}

type protectCase struct {
	name  string
	path  string
	build func(base string) protectLayout
}

func hookTree(base string) protectLayout {
	proj := filepath.Join(base, "proj")
	write(proj, ".flowstate/hooks.json", "orig")
	write(proj, ".flowstate/scripts/t", "orig")
	return protectLayout{workdir: proj, protected: []string{filepath.Join(proj, ".flowstate")}}
}

func withLinks(mk func(w string)) func(string) protectLayout {
	return func(base string) protectLayout {
		l := hookTree(base)
		mk(l.workdir)
		return l
	}
}

func homeLayout(base string, present bool) protectLayout {
	return homeLayoutWith(base, present, false)
}

func homeLayoutWith(base string, present, config bool) protectLayout {
	home := filepath.Join(base, "home")
	must(os.MkdirAll(home, 0o750))
	user := filepath.Join(home, ".config", "flowstate")
	if present {
		write(home, ".config/flowstate/hooks.json", "orig")
	} else if config {
		must(os.MkdirAll(filepath.Join(home, ".config"), 0o750))
	}
	return protectLayout{workdir: home, protected: []string{user}}
}

func sym(target, link string) {
	must(os.MkdirAll(filepath.Dir(link), 0o750))
	must(os.Symlink(target, link))
}

func refusedCases() []protectCase {
	cases := []protectCase{
		{"dot-dot back into .flowstate", ".flowstate/../.flowstate/x", hookTree},
		{"case variant", ".FlowState/x", hookTree},
		{"nested repo under an ancestor workdir", "repo/.flowstate/hooks.json", func(base string) protectLayout {
			home := filepath.Join(base, "home")
			write(home, "repo/.flowstate/hooks.json", "orig")
			return protectLayout{workdir: home, protected: []string{filepath.Join(home, "repo", ".flowstate")}}
		}},
		{"user file under workdir ~", ".config/flowstate/hooks.json", func(b string) protectLayout { return homeLayout(b, true) }},
		{"user dir absent under workdir ~", ".config/flowstate/hooks.json", func(b string) protectLayout { return homeLayout(b, false) }},
		{"user dir absent but ~/.config present", ".config/flowstate/hooks.json", func(b string) protectLayout { return homeLayoutWith(b, false, true) }},
		{"case variant of an absent user dir", ".config/FlowState/hooks.json", func(b string) protectLayout { return homeLayout(b, false) }},
	}
	if runtime.GOOS == "windows" {
		return cases
	}
	cases = append(cases, linkCases()...)
	cases = append(cases, dotDotAfterLinkCases()...)
	if runtime.GOOS == "linux" { // two directories whose names differ only in case need a case-sensitive filesystem
		cases = append(cases, caseSensitiveCases()...)
	}
	return cases
}

func caseSensitiveCases() []protectCase {
	return []protectCase{
		{"links to two directories differing only in case", "real.json", withLinks(func(w string) {
			sym("../d", filepath.Join(w, ".flowstate", "a"))
			sym("../D", filepath.Join(w, ".flowstate", "b"))
			must(os.MkdirAll(filepath.Join(w, "d"), 0o750))
			sym("../real.json", filepath.Join(w, "D", "f"))
			write(w, "real.json", "orig")
		})},
		{"link to a directory differing only in case from the protected one", "real.json", withLinks(func(w string) {
			sym("../.FLOWSTATE", filepath.Join(w, ".flowstate", "x"))
			sym("../real.json", filepath.Join(w, ".FLOWSTATE", "f"))
			write(w, "real.json", "orig")
		})},
	}
}

func lockedLink(base string) (string, string) {
	l := hookTree(base)
	locked := filepath.Join(l.workdir, "locked")
	must(os.MkdirAll(locked, 0o750))
	link := filepath.Join(l.workdir, ".flowstate", "h")
	sym("../locked/x", link)
	must(os.Chmod(locked, 0))
	DeferCleanup(func() { must(os.Chmod(locked, 0o750)) })
	return link, locked
}

func transitive(dest string, existing bool) func(string) protectLayout {
	return withLinks(func(w string) {
		sym("../d", filepath.Join(w, ".flowstate", "L"))
		sym(dest, filepath.Join(w, "d", "f"))
		if existing {
			write(w, "real.json", "orig")
		}
	})
}

func ancestorLink(exists, chain, deeper bool) func(string) protectLayout {
	return func(b string) protectLayout {
		l := homeLayout(b, false)
		first := "dotcfg"
		if chain {
			first = "mid"
			sym("dotcfg", filepath.Join(l.workdir, "mid"))
		}
		if exists {
			must(os.MkdirAll(filepath.Join(l.workdir, "dotcfg"), 0o750))
		}
		sym(first, filepath.Join(l.workdir, ".config"))
		if deeper {
			l.protected = []string{filepath.Join(l.workdir, ".config", "x", "flowstate")}
		}
		return l
	}
}

func linkCases() []protectCase {
	return []protectCase{
		{"a dangling link on an ancestor of the protected directory", "dotcfg/flowstate/hooks.json", ancestorLink(false, false, false)},
		{"a dangling link two levels above the protected directory", "dotcfg/x/flowstate/hooks.json", ancestorLink(false, false, true)},
		{"a chain of dangling links on an ancestor of the protected directory", "dotcfg/flowstate/hooks.json", ancestorLink(false, true, false)},
		{"a link to an existing directory on an ancestor of the protected directory", "dotcfg/flowstate/hooks.json", ancestorLink(true, false, false)},
		{"file linked from a symlinked directory", "real.json", transitive("../real.json", true)},
		{"file linked through two symlinked directories", "real.json", withLinks(func(w string) {
			sym("../d", filepath.Join(w, ".flowstate", "L"))
			sym("../e", filepath.Join(w, "d", "g"))
			sym("../real.json", filepath.Join(w, "e", "f"))
			write(w, "real.json", "orig")
		})},
		{"dangling link in a symlinked directory", "later.json", transitive("../later.json", false)},
		{"lnk dot-dot into .flowstate", "lnk/../.flowstate/x", linkTo(".flowstate", "lnk")},
		{"lnk to scripts then .. to hooks.json", "lnk/../hooks.json", linkTo(".flowstate/scripts", "lnk")},
		{"outward file symlink in the protected dir", "real.json", outwardFile},
		{"stow-style outward file symlink under workdir ~", "dotfiles/flowstate/hooks.json", stowLayout},
		{"outward directory symlink in the protected dir", "scr/t", outwardDir},
		{"hard link to the target of an outward file symlink", "hl2", func(b string) protectLayout { return hardLinkTo(outwardFile(b), "real.json", "hl2") }},
		{"symlinked directory not named .flowstate", "alias/hooks.json", linkTo(".flowstate", "alias")},
		{"symlinked directory to a subdirectory", "alias/scripts/new", linkTo(".flowstate", "alias")},
		{"reviewer layout d/l", "d/l", withLinks(func(w string) {
			sym("../.flowstate", filepath.Join(w, "sub", "lnkdir"))
			sym("../lnkdir/hooks.json", filepath.Join(w, "sub", "deep", "l"))
			sym("sub/deep", filepath.Join(w, "d"))
		})},
		{"file symlink", "flink.json", linkTo(".flowstate/hooks.json", "flink.json")},
		{"dangling symlink", "dang.json", linkTo(".flowstate/new.json", "dang.json")},
		{"dangling directory symlink", "dd/x", linkTo(".flowstate/newdir", "dd")},
		{"hard link to the hook file", "hl.json", hardLink(".flowstate/hooks.json", "hl.json")},
		{"hard link to a script", "hs", hardLink(".flowstate/scripts/t", "hs")},
		{"user dir absent, named through a symlinked ancestor", ".config/flowstate/hooks.json", func(base string) protectLayout {
			l := homeLayout(base, false)
			sym(l.workdir, filepath.Join(base, "viahome"))
			return protectLayout{workdir: l.workdir, protected: []string{filepath.Join(base, "viahome", ".config", "flowstate")}}
		}},
		{"hard link beyond the listing cap", "hc", withLinks(func(w string) { manyFiles(w, maxProtectedFiles+1, true) })},
		{"missing directory then .. then a symlink to .flowstate", "new/../lnk/hooks.json", linkTo(".flowstate", "lnk")},
		{"two missing directories then .. .. then a symlink", "a/b/../../lnk/x", linkTo(".flowstate", "lnk")},
		{"missing directory then .. then a symlink to the user config, user dir present", "new/../cfg/flowstate/hooks.json", func(b string) protectLayout { return cfgLink(homeLayout(b, true)) }},
		{"missing directory then .. then a symlink to the user config, user dir absent", "new/../cfg/flowstate/hooks.json", func(b string) protectLayout { return cfgLink(homeLayout(b, false)) }},
		{"dangling outward file symlink", "later.json", danglingAt("../later.json")},
		{"dangling symlink after more files than the listing cap", "later.json", pastCap("../later.json", false)},
		{"existing symlink target after more files than the listing cap", "real.json", pastCap("../real.json", true)},
		{"dangling outward symlink to a new directory", "newdir/x.json", danglingAt("../newdir/x.json")},
		{"dangling outward symlink into an existing directory", "dir/later.json", func(b string) protectLayout {
			l := danglingAt("../dir/later.json")(b)
			must(os.MkdirAll(filepath.Join(l.workdir, "dir"), 0o750))
			return l
		}},
		{"chain of dangling symlinks", "later.json", func(b string) protectLayout {
			l := danglingAt("../mid")(b)
			sym("later.json", filepath.Join(l.workdir, "mid"))
			return l
		}},
		{"dangling symlink through a symlinked directory", "deep/later.json", func(b string) protectLayout {
			l := danglingAt("../lnkd/m")(b)
			must(os.MkdirAll(filepath.Join(l.workdir, "deep", "x"), 0o750))
			sym("deep/x", filepath.Join(l.workdir, "lnkd"))
			sym("../later.json", filepath.Join(l.workdir, "deep", "x", "m"))
			return l
		}},
		{"dangling absolute symlink", "later.json", func(b string) protectLayout {
			l := danglingAt("unused")(b)
			must(os.Remove(filepath.Join(l.workdir, ".flowstate", "hooks2.json")))
			sym(filepath.Join(l.workdir, "later.json"), filepath.Join(l.workdir, ".flowstate", "hooks2.json"))
			return l
		}},
		{"dangling symlink with .. after a missing directory", "y.json", danglingAt("../newdir/../x.json")},
		{"hard link to a file under the target of an outward directory symlink", "hl", func(b string) protectLayout { return hardLinkTo(outwardDir(b), "scr/t", "hl") }},
		{"hard link to a deep file under the target of an outward directory symlink", "hl", func(b string) protectLayout {
			l := outwardDir(b)
			write(l.workdir, "scr/a/b/t", "orig")
			return hardLinkTo(l, "scr/a/b/t", "hl")
		}},
		{"hard link to a file of a symlinked protected directory", "hl", func(b string) protectLayout {
			home := filepath.Join(b, "home")
			write(home, "dotfiles/flowstate/hooks.json", "orig")
			sym(filepath.Join("..", "dotfiles", "flowstate"), filepath.Join(home, ".config", "flowstate"))
			must(os.Link(filepath.Join(home, "dotfiles", "flowstate", "hooks.json"), filepath.Join(home, "hl")))
			return protectLayout{workdir: home, protected: []string{filepath.Join(home, ".config", "flowstate")}}
		}},
	}
}

func linkTo(target, name string) func(string) protectLayout {
	return withLinks(func(w string) { sym(target, filepath.Join(w, name)) })
}

func hardLink(src, dst string) func(string) protectLayout {
	return func(b string) protectLayout { return hardLinkTo(hookTree(b), src, dst) }
}

func hardLinkTo(l protectLayout, src, dst string) protectLayout {
	must(os.Link(filepath.Join(l.workdir, src), filepath.Join(l.workdir, dst)))
	return l
}

func danglingAt(target string) func(string) protectLayout {
	return withLinks(func(w string) { sym(target, filepath.Join(w, ".flowstate", "hooks2.json")) })
}

func outwardFile(b string) protectLayout {
	l := hookTree(b)
	write(l.workdir, "real.json", "orig")
	sym("../real.json", filepath.Join(l.workdir, ".flowstate", "link.json"))
	return l
}

func outwardDir(b string) protectLayout {
	l := hookTree(b)
	write(l.workdir, "scr/t", "orig")
	sym("../scr", filepath.Join(l.workdir, ".flowstate", "scripts2"))
	return l
}

func stowLayout(b string) protectLayout {
	home := filepath.Join(b, "home")
	write(home, "dotfiles/flowstate/hooks.json", "orig")
	sym("../../dotfiles/flowstate/hooks.json", filepath.Join(home, ".config", "flowstate", "hooks.json"))
	return protectLayout{workdir: home, protected: []string{filepath.Join(home, ".config", "flowstate")}}
}

func allowedLinkCases() []protectCase {
	cases := allowedUnixCases()
	if runtime.GOOS == "linux" {
		cases = append(cases, protectCase{"unrelated file beside a loop between directories differing in case", "other.json",
			withLinks(func(w string) {
				write(w, "other.json", "orig")
				sym("../d", filepath.Join(w, ".flowstate", "a"))
				sym("../D", filepath.Join(w, "d", "x"))
				sym("../d", filepath.Join(w, "D", "y"))
			})})
	}
	return cases
}

func beside(mk func(string) protectLayout, file string) func(string) protectLayout {
	return func(b string) protectLayout {
		l := mk(b)
		write(l.workdir, file, "orig")
		return l
	}
}

func allowedUnixCases() []protectCase {
	return []protectCase{
		{"unrelated file beside transitive links", "other.json", beside(transitive("../real.json", true), "other.json")},
		{"unrelated file beside a loop through a symlinked directory", "other.json", withLinks(func(w string) {
			write(w, "other.json", "orig")
			sym("../d", filepath.Join(w, ".flowstate", "L"))
			sym("../.flowstate", filepath.Join(w, "d", "back"))
		})},
		{"hard link when the listing holds exactly the cap", "hc", withLinks(func(w string) { manyFiles(w, maxProtectedFiles, true) })},
		{"unrelated file beside an outward symlink", "other.json", beside(outwardFile, "other.json")},
		{"unrelated file beside a symlink loop", "later.json", withLinks(func(w string) {
			write(w, "later.json", "orig")
			sym("loop2", filepath.Join(w, ".flowstate", "loop"))
			sym("loop", filepath.Join(w, ".flowstate", "loop2"))
		})},
		{"unrelated file beside a chain longer than the hop bound", "later.json", withLinks(func(w string) {
			write(w, "later.json", "orig")
			for i := 0; i <= maxLinkHops; i++ {
				sym(fmt.Sprintf("h%d", i+1), filepath.Join(w, ".flowstate", fmt.Sprintf("h%d", i)))
			}
		})},
		{"unrelated file beside a dangling ancestor link", "notes.txt", beside(ancestorLink(false, false, false), "notes.txt")},
		{"unrelated file beside a dangling symlink", "other.json", beside(danglingAt("../later.json"), "other.json")},
		{"single-link file past the listing cap", "hc", withLinks(func(w string) { manyFiles(w, maxProtectedFiles+1, false) })},
	}
}

func pastCap(target string, existing bool) func(string) protectLayout {
	return withLinks(func(w string) {
		filler(w, ".flowstate/a/", maxProtectedFiles+76)
		sym(target, filepath.Join(w, ".flowstate", "zz.json"))
		if existing {
			write(w, "real.json", "orig")
		}
	})
}

func filler(w, prefix string, n int) {
	for i := 0; i < n; i++ {
		write(w, fmt.Sprintf("%sf%d", prefix, i), "x")
	}
}

func lowerEntryBound(n int) {
	old := maxProtectedEntries
	maxProtectedEntries = n
	DeferCleanup(func() { maxProtectedEntries = old })
}

func manyFiles(w string, total int, linked bool) {
	filler(w, ".flowstate/many/", total-2)
	write(w, "hc", "orig")
	if linked {
		must(os.Link(filepath.Join(w, "hc"), filepath.Join(w, "hc2")))
	}
}

func cfgLink(l protectLayout) protectLayout {
	must(os.MkdirAll(filepath.Join(l.workdir, ".config"), 0o750))
	sym(".config", filepath.Join(l.workdir, "cfg"))
	return l
}

type protectTool struct {
	name string
	mk   func(string, ...FSOption) Tool
}

func protectTools() []protectTool {
	return []protectTool{{FSWriteName, NewFSWrite}, {FSEditName, NewFSEdit}}
}

func invokeTool(t Tool, rel string) error {
	body := map[string]string{"path": rel, "content": "new"}
	if t.Name() == FSEditName {
		body = map[string]string{"path": rel, "old_text": "orig", "new_text": "new"}
	}
	args, err := json.Marshal(body)
	must(err)
	_, ierr := t.Invoke(GinkgoT().Context(), args)
	return ierr
}

var _ = Describe("protected trees (ADR-012 D20)", func() {
	var base string

	BeforeEach(func() {
		base = GinkgoT().TempDir()
	})

	for _, pt := range protectTools() {
		name, mk := pt.name, pt.mk
		for _, c := range refusedCases() {
			It(name+" refuses "+c.name, func() {
				l := c.build(base)
				Expect(invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), c.path)).To(MatchError(ErrPathProtected))
			})
		}

		It(name+" with no option writes under .flowstate", func() {
			l := hookTree(base)
			write(l.workdir, ".flowstate/x", "orig")
			Expect(invokeTool(mk(l.workdir), ".flowstate/x")).To(Succeed())
		})
		for _, dir := range []string{".flowstate", ".FlowState"} {
			It(name+" refuses "+dir+" when the option lists no directory", func() {
				l := hookTree(base)
				Expect(invokeTool(mk(l.workdir, WithProtectedDirs()), dir+"/x")).To(MatchError(ErrPathProtected))
			})
		}
		It(name+" writes an ordinary path", func() {
			l := hookTree(base)
			write(l.workdir, "ok.txt", "orig")
			Expect(invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), "ok.txt")).To(Succeed())
		})
		It(name+" maps a refusal to fs_path_protected", func() {
			l := hookTree(base)
			err := invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), ".flowstate/hooks.json")
			Expect(errorCodeFor(err)).To(Equal("fs_path_protected"))
		})
	}

	for _, c := range []struct {
		name, target, dir string
		want              bool
	}{
		{"accepts a target equal to the directory", "d", "d", true},
		{"rejects a sibling sharing a name prefix", "d2/x", "d", false},
		{"rejects a target that is the parent of the directory", ".", "d", false},
		{"rejects a target shorter than the directory", "x", "d/e", false},
	} {
		It("within "+c.name, func() {
			Expect(within(filepath.Join(base, c.target), filepath.Join(base, c.dir))).To(Equal(c.want))
		})
	}
	It("fs.write result event carries the fs_path_protected code through the registry", func() {
		l := hookTree(base)
		lp, rs := newFSLoop(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), []string{FSWriteName})
		_, _ = invoke(lp, FSWriteName, `{"path":".flowstate/hooks.json","content":"x"}`)
		Expect(lastResultCode(rs)).To(Equal(ErrCodePathProtected))
	})

	It("fs.read still reads a hook file", func() {
		l := hookTree(base)
		res, err := NewFSRead(l.workdir).Invoke(GinkgoT().Context(), json.RawMessage(`{"path":".flowstate/hooks.json"}`))
		must(err)
		Expect(res).To(HaveKeyWithValue("content", "orig"))
	})
	It("fs.write leaves the hook file unchanged on a refusal", func() {
		l := hookTree(base)
		_ = invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), ".flowstate/../.flowstate/hooks.json")
		Expect(read(l.workdir, ".flowstate/hooks.json")).To(Equal("orig"))
	})
	It("fs.write creates no directory for a refused absent user directory", func() {
		l := homeLayout(base, false)
		_ = invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), ".config/flowstate/hooks.json")
		Expect(l.protected[0]).NotTo(BeADirectory())
	})

	if runtime.GOOS == "windows" {
		return
	}
	for _, pt := range protectTools() {
		name, mk := pt.name, pt.mk
		for _, c := range allowedLinkCases() {
			It(name+" allows "+c.name, func() {
				l := c.build(base)
				Expect(invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), c.path)).To(Succeed())
			})
		}
	}
	for _, pt := range protectTools() {
		name, mk := pt.name, pt.mk
		It(name+" refuses every write when the protected directory holds more entries than the symlink bound", func() {
			lowerEntryBound(20)
			l := hookTree(base)
			filler(l.workdir, ".flowstate/a/", 30)
			write(l.workdir, "ok.txt", "orig")
			Expect(invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), "ok.txt")).To(MatchError(ErrPathProtected))
		})
		It(name+" refuses every write when the entries behind a symlinked directory pass the symlink bound", func() {
			lowerEntryBound(35) // each directory alone fits; the two together do not
			l := transitive("../real.json", true)(base)
			filler(l.workdir, "d/a/", 30)
			write(l.workdir, "ok.txt", "orig")
			Expect(invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), "ok.txt")).To(MatchError(ErrPathProtected))
		})
		It(name+" allows an ordinary write when the protected directory holds exactly the symlink bound", func() {
			lowerEntryBound(32)
			l := hookTree(base)
			filler(l.workdir, ".flowstate/a/", 27)
			write(l.workdir, "ok.txt", "orig")
			Expect(invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), "ok.txt")).To(Succeed())
		})
	}
	It("fs.write leaves no directory behind a refused missing-directory dot-dot path", func() {
		l := linkTo(".flowstate", "lnk")(base)
		_ = invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), "new/../lnk/hooks.json")
		Expect(filepath.Join(l.workdir, "new")).NotTo(BeADirectory())
	})
	It("fs.write refuses a protected directory given relative to the process working directory", func() {
		l := hookTree(base)
		cwd, err := os.Getwd()
		must(err)
		rel, err := filepath.Rel(cwd, l.protected[0])
		must(err)
		sym(".flowstate", filepath.Join(l.workdir, "alias2"))
		Expect(invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(rel)), "alias2/x")).To(MatchError(ErrPathProtected))
	})
	It("WithProtectedDirs keeps an absolute entry and drops an empty one", func() {
		var o fsOptions
		WithProtectedDirs("", filepath.Join(base, "p"))(&o)
		Expect(o.protect.dirs).To(Equal([]string{filepath.Join(base, "p")}))
	})
	It("WithProtectedDirs makes a relative entry absolute", func() {
		var o fsOptions
		WithProtectedDirs(filepath.Join("rel", "dir"))(&o)
		Expect(filepath.IsAbs(o.protect.dirs[0])).To(BeTrue())
	})
	It("sameAsProtected identifies a directory by identity when no name relates it to the protected path", func() {
		l := hookTree(base)
		alias := filepath.Join(base, "elsewhere")
		sym(filepath.Join(l.workdir, ".flowstate"), alias)
		root, err := os.OpenRoot(l.workdir)
		must(err)
		defer root.Close()
		Expect(sameAsProtected(root, []string{".flowstate"}, &fsProtect{dirs: []string{alias}})).To(MatchError(ErrPathProtected))
	})
	if os.Geteuid() != 0 { // root reads a mode 0 directory, so these fixtures only bite for other users
		It("resolveLink reports the state linkUnknown when a component cannot be read", func() {
			link, _ := lockedLink(base)
			_, state := resolveLink(link)
			Expect(state).To(Equal(linkUnknown))
		})
		It("resolveLink reports the directory reached when a component cannot be read", func() {
			link, locked := lockedLink(base)
			res, _ := resolveLink(link)
			Expect(res).To(Equal(locked))
		})
		It("fs.write refuses every write when a protected directory holds an unreadable subdirectory", func() {
			l := hookTree(base)
			locked := filepath.Join(l.workdir, ".flowstate", "locked")
			must(os.MkdirAll(locked, 0o750))
			must(os.Chmod(locked, 0))
			defer func() { must(os.Chmod(locked, 0o750)) }()
			Expect(invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), "ok.txt")).To(MatchError(ErrPathProtected))
		})
	}
	for _, c := range []struct{ name, target, link, path, created string }{
		{"dangling outward symlink", "../later.json", "later.json", "later.json", "later.json"},
		{"dangling symlink into .flowstate", ".flowstate/new.json", "dang.json", "dang.json", ".flowstate/new.json"},
	} {
		It("fs.write creates no file through a refused "+c.name, func() {
			l := linkTo(c.target, c.link)(base)
			_ = invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), c.path)
			Expect(filepath.Join(l.workdir, c.created)).NotTo(BeAnExistingFile())
		})
	}
	It("fs.write leaves the hook file unchanged when written through a refused symlink", func() {
		l := linkTo(".flowstate/hooks.json", "flink.json")(base)
		_ = invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), "flink.json")
		Expect(read(l.workdir, ".flowstate/hooks.json")).To(Equal("orig"))
	})
	It("fs.edit of a missing file behind a dangling outward symlink is not a success", func() {
		l := danglingAt("../later.json")(base)
		Expect(invokeTool(NewFSEdit(l.workdir, WithProtectedDirs(l.protected...)), "later.json")).NotTo(Succeed())
	})
	It("fs.write leaves the hook file unchanged on a refused missing-directory dot-dot path", func() {
		l := linkTo(".flowstate", "lnk")(base)
		_ = invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), "new/../lnk/hooks.json")
		Expect(read(l.workdir, ".flowstate/hooks.json")).To(Equal("orig"))
	})
	It("fs.write refuses an absolute symlink target through os.Root without the option", func() {
		l := hookTree(base)
		sym(filepath.Join(l.workdir, ".flowstate", "hooks.json"), filepath.Join(l.workdir, "abs.json"))
		Expect(invokeTool(NewFSWrite(l.workdir), "abs.json")).To(MatchError(ErrPathOutsideWorkdir))
	})
	It("fs.write writes a multiply-linked file that is not in a protected directory", func() {
		l := hookTree(base)
		write(l.workdir, "a.txt", "orig")
		must(os.Link(filepath.Join(l.workdir, "a.txt"), filepath.Join(l.workdir, "b.txt")))
		Expect(invokeTool(NewFSWrite(l.workdir, WithProtectedDirs(l.protected...)), "a.txt")).To(Succeed())
	})
})

func dotDotLayout(present bool, links map[string]string) func(string) protectLayout {
	return func(base string) protectLayout {
		l := homeLayoutWith(base, present, true)
		for name, target := range links {
			sym(target, filepath.Join(l.workdir, name))
		}
		return l
	}
}

func dotDotAfterLinkCases() []protectCase {
	var cases []protectCase
	for _, present := range []bool{false, true} {
		state := map[bool]string{false: "absent", true: "present"}[present]
		add := func(name, path string, links map[string]string) {
			cases = append(cases, protectCase{name + ", user dir " + state, path, dotDotLayout(present, links)})
		}
		add("link then .. into the user config", "lnk/../.config/flowstate/h", map[string]string{"lnk": ".config"})
		add("nested link then .. into the user config", "s/lnk/../.config/flowstate/h", map[string]string{"s/lnk": "../.config"})
		add("nested link then .. then the link again", "s/lnk/../s/lnk/flowstate/h", map[string]string{"s/lnk": "../.config"})
		add("link then .. twice", "lnk/../lnk/../.config/flowstate/h", map[string]string{"lnk": ".config"})
		add("chain of links then ..", "s/a/../.config/flowstate/h", map[string]string{"s/a": "../b", "b": ".config"})
	}
	return cases
}

// danglingLinks: layouts with a dangling link lk that a MkdirAll through it would create, and the path that must stay absent.
var danglingLinks = []struct{ name, target, absent string }{
	{"an absent .flowstate", ".flowstate", ".flowstate"},
	{"a new subdirectory of a present user directory", ".config/flowstate/newdir", ".config/flowstate/newdir"},
	{"an absent user directory", ".config/flowstate", ".config/flowstate"},
}

func danglingLayout(base, target string) protectLayout {
	l := homeLayout(base, target == ".config/flowstate/newdir")
	sym(target, filepath.Join(l.workdir, "lk"))
	if target == ".flowstate" {
		l.protected = []string{filepath.Join(l.workdir, ".flowstate")}
	}
	return l
}

var _ = Describe("protected trees: raw path suffixes", func() {
	if runtime.GOOS == "windows" {
		return
	}
	for _, pt := range protectTools() {
		name, mk := pt.name, pt.mk
		for _, d := range danglingLinks {
			for _, suffix := range []string{"/./.", "/.", "/", "/..", "//"} {
				It(name+" refuses lk"+suffix+" for a dangling link to "+d.name, func() {
					l := danglingLayout(GinkgoT().TempDir(), d.target)
					Expect(invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), "lk"+suffix)).To(MatchError(ErrPathProtected))
				})
				It(name+" creates nothing for lk"+suffix+" with a dangling link to "+d.name, func() {
					l := danglingLayout(GinkgoT().TempDir(), d.target)
					_ = invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), "lk"+suffix)
					Expect(filepath.Join(l.workdir, d.absent)).NotTo(BeADirectory())
				})
			}
		}
		It(name+" leaves protected trees untouched for every refused case with a raw suffix", func() {
			for i, c := range refusedCases() {
				for _, suffix := range []string{"/", "/.", "/./.", "/..", "//", " ", "\\"} {
					l := c.build(filepath.Join(GinkgoT().TempDir(), fmt.Sprint(i)))
					before := snapshotTrees(l.protected)
					_ = invokeTool(mk(l.workdir, WithProtectedDirs(l.protected...)), c.path+suffix)
					Expect(snapshotTrees(l.protected)).To(Equal(before), c.name+suffix)
				}
			}
		})
	}
})

// snapshotTrees records every path and file content under dirs without following links; an absent dir is empty.
func snapshotTrees(dirs []string) map[string]string {
	out := map[string]string{}
	for _, d := range dirs {
		_ = filepath.WalkDir(d, func(p string, _ os.DirEntry, err error) error {
			b, _ := os.ReadFile(p)
			out[p] = string(b)
			return nil
		})
	}
	return out
}
