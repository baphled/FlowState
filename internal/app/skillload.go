package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/tool"
)

// TypeSkillLoaded is written after the tool.result of a skill.load that delivered a file (ADR-013 D9): the record that
// the exact bytes, by hash, were handed to the model whole, which LoadSessionHistory checks against that result's frame.
const TypeSkillLoaded = "skill.loaded"

// offeredSkills is the catalog of a root run: the loaded skills that skills_default names, exactly or by a `*`-suffixed
// prefix; nothing offered means no skill.load. The roots are the request's scope and paths, the project directory under the
// project root and never the workdir (as for hooks). An unset scope with a skills_default is config_invalid (the zero scope
// never loads the user directory); an invalid skill is not offered.
func offeredSkills(req SessionExecLoopRequest) (tool.SkillCatalog, error) {
	if len(req.SkillsDefault) == 0 {
		return tool.SkillCatalog{}, nil
	}
	roots, err := SkillsRoots(SkillsRequest{Scope: req.SkillsScope, ProjectRoot: HooksProjectRoot(req), Paths: req.SkillsPaths})
	if err != nil {
		return tool.SkillCatalog{}, err
	}
	held := map[string]string{} // each offered skill's skills root: the tool opens its directory through it, never directly
	for _, s := range skill.Load(roots).Skills {
		if skillOffered(s.Name, req.SkillsDefault) {
			held[s.Name] = filepath.Dir(filepath.Dir(s.Path)) // the directory above the skill's own: os.OpenRoot follows a symlink in its argument
		}
	}
	if len(held) == 0 {
		return tool.SkillCatalog{}, nil
	}
	return tool.SkillCatalog{Finder: skillFinder(held), Format: skillFormat()}, nil
}

// skillOffered reports whether an entry of skills_default names the skill: exactly, or as a `*`-suffixed prefix.
func skillOffered(name string, entries []string) bool {
	for _, e := range entries {
		if e == name {
			return true
		}
		if prefix, ok := strings.CutSuffix(e, "*"); ok && strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// skillFinder resolves an offered name to the skills root holding it, so each skill is opened through its own root;
// any other is not offered, and no message carries a path.
func skillFinder(roots map[string]string) tool.SkillFinder {
	return func(name string) (string, error) {
		root, ok := roots[name]
		if !ok {
			return "", fmt.Errorf("skill %q is not offered", name)
		}
		return root, nil
	}
}

// skillFormat is the skill package's rules, as the tool reads them (the tool domain imports no adapter).
func skillFormat() tool.SkillFormat {
	return tool.SkillFormat{MaxBytes: skill.MaxFileBytes, MaxLines: skill.MaxFileLines, MaxResultBytes: skill.MaxResultBytes,
		CloseFence: skill.CloseFence, ValidPath: skill.ValidSupportPath, ValidText: skillFileText, Body: skill.Body}
}

// skillFileText is the skill package's text rule for a supporting file, with the name the error uses.
func skillFileText(data []byte) error { return skill.ValidText(data, "the file") }

// recordSkillLoaded appends skill.loaded for a delivered skill file through the call's writer, after its tool.result
// and before the model sees the result: a crash between the two leaves no record (fail safe); a refused load never gets here.
func recordSkillLoaded(ctx context.Context, w tool.EventSink, callID string, loaded tool.SkillResult) error {
	payload := map[string]any{"name": loaded.Name, "file": loaded.File, "sha256": loaded.SHA256, "path": loaded.Path,
		"bytes": loaded.Bytes, "tool_call_id": callID}
	if _, err := w.Append(ctx, session.ActorEngine, TypeSkillLoaded, payload); err != nil {
		return fmt.Errorf("%w: skill.loaded: %w", tool.ErrEventAppend, err)
	}
	return nil
}
