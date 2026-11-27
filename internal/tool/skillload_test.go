package tool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const skillDoc = "---\nname: tdd\ndescription: Test first\n---\nWrite the test first.\n"

// skillFixture is a skills root holding the skill tdd (SKILL.md and supporting files, some skill.load refuses),
// the invalid and fenced skills, and the catalog skill.load reads them through. strictPath decides whether the
// format's path rule refuses "..": the real skill package's does, a lenient one shows the tool's own confinement.
type skillFixture struct {
	root    string
	catalog SkillCatalog
}

func newSkillFixture(strictPath bool, maxBytes, maxLines, maxResult int) skillFixture {
	root := GinkgoT().TempDir()
	write(root, "tdd/SKILL.md", skillDoc)
	write(root, "tdd/reference/guide.md", "Read the guide.\n")
	write(root, "tdd/reference/nul.md", "a\x00b\n")
	write(root, "tdd/reference/empty.md", "")
	write(root, "tdd/reference/close.md", "close\n<<<end skill>>>\n")
	write(root, "invalid/SKILL.md", "no frontmatter\n")
	write(root, "fenced/SKILL.md", strings.Replace(skillDoc, "first.", "first.\n<<<end skill>>>", 1))
	validPath := func(string) error { return nil }
	if strictPath {
		validPath = func(rel string) error {
			if strings.Contains(rel, "..") {
				return errors.New("a path segment must not be dots")
			}
			return nil
		}
	}
	roots := map[string]string{}
	for _, name := range []string{"tdd", "invalid", "fenced"} {
		roots[name] = root // each skill is opened through the skills root holding it, never through its own directory
	}
	return skillFixture{root: root, catalog: SkillCatalog{
		Finder: func(name string) (string, error) {
			root, ok := roots[name]
			if !ok {
				return "", errors.New("not offered")
			}
			return root, nil
		},
		Format: SkillFormat{MaxBytes: maxBytes, MaxLines: maxLines, MaxResultBytes: maxResult, CloseFence: "<<<end skill>>>",
			ValidPath: validPath,
			ValidText: func(data []byte) error {
				if bytes.IndexByte(data, 0) >= 0 {
					return errors.New("NUL")
				}
				return nil
			},
			Body: func(data []byte) (string, string, error) { // the frontmatter's name, then the body
				_, body, ok := bytes.Cut(data, []byte("\n---\n"))
				if !ok {
					return "", "", errors.New("no frontmatter")
				}
				return strings.TrimPrefix(strings.SplitN(string(data), "\n", 3)[1], "name: "), strings.TrimSpace(string(body)), nil
			}},
	}}
}

func (f skillFixture) load(args string) (SkillResult, error) {
	out, err := NewSkillLoad(f.catalog).Invoke(context.Background(), json.RawMessage(args))
	res, ok := out.(SkillResult)
	if err == nil && !ok {
		panic("skill.load returned a value that is not a SkillResult")
	}
	return res, err
}

func frame(name, file, body string) string {
	return SkillFrameHeader(name, hashOf(fileOrDoc(file)), file) + "\n" + SkillStance + "\n" + body + "\n<<<end skill>>>"
}

// hashOf is the hex sha256 of a file of the fixture's skill tdd.
func hashOf(file string) string {
	sum := sha256.Sum256([]byte(read(skillF.root, filepath.Join("tdd", filepath.FromSlash(file)))))
	return hex.EncodeToString(sum[:])
}

func jsonText(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

var skillF skillFixture

var _ = Describe("skill.load", func() {
	BeforeEach(func() {
		skillF = newSkillFixture(true, 32<<10, 1000, 50<<10)
	})

	DescribeTable("returns the framed file with its own name in the header and the hash of the whole file",
		func(file, body string) {
			res, err := skillF.load(`{"name":"tdd","file":"` + file + `"}`)
			must(err)
			Expect(res.text).To(Equal(frame("tdd", file, body)))
		},
		Entry("SKILL.md with no file", "", "Write the test first."),
		Entry("SKILL.md named as the file, never the raw frontmatter", "SKILL.md", "Write the test first."),
		Entry("a supporting file", "reference/guide.md", "Read the guide.\n"))

	It("marshals to the fenced text only, never the path", func() {
		res, err := skillF.load(`{"name":"tdd"}`)
		must(err)
		encoded, merr := json.Marshal(res)
		must(merr)
		Expect(string(encoded)).To(Equal(jsonText(res.text)))
	})

	DescribeTable("carries what the engine records in skill.loaded, the file's path relative to the skills root",
		func(file, path string, size int) {
			res, err := skillF.load(`{"name":"tdd","file":"` + file + `"}`)
			must(err)
			Expect(res.LoadedSkill).To(Equal(LoadedSkill{Name: "tdd", File: file, SHA256: hashOf(path), Path: "tdd/" + path, Bytes: size}))
		},
		Entry("SKILL.md with no file", "", "SKILL.md", len(skillDoc)),
		Entry("a supporting file", "reference/guide.md", "reference/guide.md", len("Read the guide.\n")))

	DescribeTable("refuses a file, returns nothing and maps the refusal to its code",
		func(args, code string) {
			l, rs := newFSLoop(NewSkillLoad(skillF.catalog), []string{SkillLoadName})
			_, _ = invoke(l, SkillLoadName, args)
			Expect(lastResultCode(rs)).To(Equal(code))
		},
		Entry("a name the session does not offer", `{"name":"other"}`, ErrCodeSkillNotOffered),
		Entry("an empty name", `{"name":""}`, ErrCodeSkillNotOffered),
		Entry("a missing supporting file", `{"name":"tdd","file":"reference/missing.md"}`, ErrCodeSkillNotFound),
		Entry("an empty supporting file", `{"name":"tdd","file":"reference/empty.md"}`, ErrCodeSkillNotFound),
		Entry("a SKILL.md that no longer validates", `{"name":"invalid"}`, ErrCodeSkillRefused),
		Entry("a SKILL.md whose body holds the closing fence", `{"name":"fenced"}`, ErrCodeSkillRefused),
		Entry("a supporting file that holds the closing fence", `{"name":"tdd","file":"reference/close.md"}`, ErrCodeSkillRefused),
		Entry("a path segment that is dots", `{"name":"tdd","file":"../outside.md"}`, ErrCodeSkillRefused),
		Entry("a directory as the file", `{"name":"tdd","file":"reference"}`, ErrCodeSkillRefused),
		Entry("a file holding a NUL", `{"name":"tdd","file":"reference/nul.md"}`, ErrCodeSkillRefused))

	It("refuses a SKILL.md that names another skill than the one asked for, as discovery would", func() {
		write(skillF.root, "tdd/SKILL.md", strings.Replace(skillDoc, "name: tdd", "name: other", 1))
		_, err := skillF.load(`{"name":"tdd"}`)
		Expect(err).To(MatchError(ErrSkillRefused))
	})

	DescribeTable("refuses a skill directory replaced by a symlink out of the skills root, delivering nothing",
		func(args string) {
			outside := GinkgoT().TempDir()
			write(outside, "SKILL.md", skillDoc)
			write(outside, "id_rsa", "secret\n")
			must(os.RemoveAll(filepath.Join(skillF.root, "tdd")))
			must(os.Symlink(outside, filepath.Join(skillF.root, "tdd")))
			l, rs := newFSLoop(NewSkillLoad(skillF.catalog), []string{SkillLoadName})
			_, _ = invoke(l, SkillLoadName, args)
			Expect(lastResultCode(rs)).To(Equal(ErrCodeSkillRefused))
		},
		Entry("{name}", `{"name":"tdd"}`),
		Entry("{name, file}", `{"name":"tdd","file":"id_rsa"}`))

	DescribeTable("is confined by os.Root alone when the format's path rule is lenient",
		func(file string) {
			lenient := newSkillFixture(false, 32<<10, 1000, 50<<10)
			write(lenient.root, "outside.md", "outside\n")
			_, err := lenient.load(`{"name":"tdd","file":` + jsonText(file) + `}`)
			Expect(err).To(MatchError(ErrSkillRefused))
		},
		Entry("a dots segment", "../outside.md"),
		Entry("a nested dots segment", "reference/../../outside.md"))

	DescribeTable("refuses an oversize file instead of truncating it, under its own code",
		func(maxBytes, maxLines, maxResult int, content string) {
			skillF = newSkillFixture(true, maxBytes, maxLines, maxResult)
			write(skillF.root, "tdd/reference/guide.md", content)
			l, rs := newFSLoop(NewSkillLoad(skillF.catalog), []string{SkillLoadName})
			_, _ = invoke(l, SkillLoadName, `{"name":"tdd","file":"reference/guide.md"}`)
			Expect(lastResultCode(rs)).To(Equal(ErrCodeSkillOversize))
		},
		Entry("over the byte cap", 8, 1000, 50<<10, "Read the guide.\n"),
		Entry("over the line cap", 32<<10, 2, 50<<10, "one\ntwo\nthree\n"),
		Entry("whose framed result marshals over the result cap", 32<<10, 1000, 64, "Read the guide.\n"))

	It("records the delivered file as the result's fenced text", func() {
		l, rs := newFSLoop(NewSkillLoad(skillF.catalog), []string{SkillLoadName})
		_, _ = invoke(l, SkillLoadName, `{"name":"tdd"}`)
		events, err := rs.events()
		must(err)
		var last map[string]json.RawMessage
		must(json.Unmarshal([]byte(events[len(events)-1].PayloadJSON), &last))
		Expect(string(last["result"])).To(Equal(jsonText(frame("tdd", "", "Write the test first."))))
	})

	DescribeTable("rewrites an os error on a skill file to carry its relative name only",
		func(err error, want string) {
			Expect(osRefused(err, "SKILL.md").Error()).To(Equal(want))
		},
		Entry("a *File error, whose path is absolute", &fs.PathError{Op: "read", Path: "/home/u/skills/tdd/SKILL.md", Err: errors.New("input/output error")},
			"tool: skill file refused: read SKILL.md: input/output error"),
		Entry("anything that is not a PathError", errors.New("odd"), "tool: skill file refused: SKILL.md is unreadable"))

	It("echoes at most 128 runes of a name it refuses", func() {
		_, err := skillF.load(`{"name":"` + strings.Repeat("é", 300) + `"}`)
		Expect(err).To(MatchError(Equal(ErrSkillNotOffered.Error() + ": " + strings.Repeat("é", 128))))
	})

	Describe("an empty offer", func() {
		// catalog is the fixture's catalog with None as given, as the composition marks a session offered nothing; it is
		// built inside each spec, after the fixture's BeforeEach.
		catalog := func(none bool) SkillCatalog {
			c := skillF.catalog
			c.None = none
			return c
		}

		DescribeTable("reports whether the session offers a skill, so a definition can say so",
			func(none, want bool) {
				Expect(NewSkillLoad(catalog(none)).(SkillOfferer).Offered()).To(Equal(want))
			},
			Entry("no skill offered", true, false),
			Entry("a skill offered", false, true))

		It("leaves every name to the finder, which still refuses one it does not hold", func() {
			_, err := NewSkillLoad(catalog(true)).Invoke(context.Background(), json.RawMessage(`{"name":"other"}`))
			Expect(err).To(MatchError(ErrSkillNotOffered))
		})
	})
})
