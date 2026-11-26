package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var (
	hookIDRE     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	hookMatchRE  = regexp.MustCompile(`^[A-Za-z0-9._*-]{1,64}$`)
	hookEnvRE    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	hookSecretRE = regexp.MustCompile(`(?i)key|token|secret|password`)
)

// hookPointKnown reports whether p is one of the eight version-1 points.
func hookPointKnown(p HookPoint) bool {
	return slices.Contains([]HookPoint{HookSessionStart, HookUserPrompt, HookToolBefore, HookToolAfter,
		HookTurnAfter, HookCompactionBefore, HookCompactionAfter, HookSessionEnd}, p)
}

// strictHookDecode scans for secret-looking keys, then decodes with unknown
// fields refused; json.Unmarshal already refuses trailing content.
func strictHookDecode(data []byte, v any) error {
	var raw any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	if err := rejectHookSecretKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// rejectHookSecretKeys refuses any object key that looks like a credential, at
// any depth. Only keys are tested, never values.
func rejectHookSecretKeys(v any) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if hookSecretRE.MatchString(k) {
				return fmt.Errorf("key %q looks like a secret; secrets must come from the environment", k)
			}
			if err := rejectHookSecretKeys(child); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range t {
			if err := rejectHookSecretKeys(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseHookDocument validates one hook file's bytes.
func parseHookDocument(data []byte, c hookContext) ([]Hook, error) {
	var doc hookDocument
	if err := strictHookDecode(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrConfigInvalid, c.origin, err)
	}
	if doc.Version != HooksFileVersion {
		return nil, fmt.Errorf("%w: %s: version must be %d", ErrConfigInvalid, c.origin, HooksFileVersion)
	}
	return decodeHookList(doc.Hooks, c)
}

// decodeHookList decodes and validates a hooks array.
func decodeHookList(raw json.RawMessage, c hookContext) ([]Hook, error) {
	var specs []hookSpec
	if len(raw) > 0 {
		if err := strictHookDecode(raw, &specs); err != nil {
			return nil, fmt.Errorf("%w: %s: hooks: %w", ErrConfigInvalid, c.origin, err)
		}
	}
	hooks := make([]Hook, 0, len(specs))
	seen := map[string]bool{}
	for i, s := range specs {
		h, err := validateHook(s, c)
		if err != nil {
			return nil, err
		}
		if seen[h.ID] {
			return nil, fmt.Errorf("%w: %s: hook id %q is declared twice (hook %d)", ErrConfigInvalid, c.origin, h.ID, i)
		}
		seen[h.ID] = true
		hooks = append(hooks, h)
	}
	return hooks, nil
}

// validateHook applies the per-hook rules and defaults.
func validateHook(s hookSpec, c hookContext) (Hook, error) {
	h := Hook{ID: s.ID, Point: HookPoint(s.Point), Kind: HookKind(s.Kind), Transform: s.Transform,
		Origin: c.origin, Source: c.source}
	fail := func(format string, args ...any) (Hook, error) {
		return Hook{}, fmt.Errorf("%w: %s: hook %q: %s", ErrConfigInvalid, c.origin, s.ID, fmt.Sprintf(format, args...))
	}
	if !hookIDRE.MatchString(s.ID) {
		return fail("id must match %s", hookIDRE)
	}
	if !hookPointKnown(h.Point) {
		return fail("unknown point %q", s.Point)
	}
	if err := checkPointSupported(h, c); err != nil {
		return Hook{}, fmt.Errorf("%w: %s: hook %q: %w", ErrConfigInvalid, c.origin, s.ID, err)
	}
	if err := checkMatch(s.Match, h.Point); err != nil {
		return fail("%v", err)
	}
	h.Match = s.Match
	var err error
	if h.OnFailure, err = onFailure(s.OnFailure, h.Point); err != nil {
		return fail("%v", err)
	}
	if h.TimeoutMS, err = hookTimeout(s.TimeoutMS); err != nil {
		return fail("%v", err)
	}
	if err = validateKind(&h, s, c); err != nil {
		return fail("%v", err)
	}
	return h, nil
}

// checkPointSupported refuses a point this build cannot fire where the hook
// sits, and transform until it is accepted.
func checkPointSupported(h Hook, c hookContext) error {
	if !slices.Contains(c.accepted.Points, h.Point) {
		return fmt.Errorf("%w: %q cannot be fired by this build", ErrHookPointUnsupported, h.Point)
	}
	if h.Transform && h.Point != HookToolBefore {
		return fmt.Errorf("transform is only allowed on %s", HookToolBefore)
	}
	if h.Transform && !c.accepted.Transform {
		return fmt.Errorf("%w: transform is not supported by this build", ErrHookPointUnsupported)
	}
	return nil
}

// checkMatch validates matchers: only tool points take them.
func checkMatch(match []string, p HookPoint) error {
	if match == nil {
		return nil
	}
	if p != HookToolBefore && p != HookToolAfter {
		return fmt.Errorf("match only applies to %s and %s, not %s", HookToolBefore, HookToolAfter, p)
	}
	if len(match) == 0 {
		return errors.New("match is empty; omit it to match every tool")
	}
	for _, m := range match {
		if !hookMatchRE.MatchString(m) {
			return fmt.Errorf("match %q must be a tool name or a * glob over it", m)
		}
	}
	return nil
}

// onFailure applies the per-point default and refuses deny where there is
// nothing to deny.
func onFailure(v string, p HookPoint) (string, error) {
	gate := p == HookToolBefore || p == HookUserPrompt || p == HookCompactionBefore
	switch v {
	case "":
		if gate {
			return HookOnFailureDeny, nil
		}
		return HookOnFailureAllow, nil
	case HookOnFailureAllow:
		return v, nil
	case HookOnFailureDeny:
		if !gate {
			return "", fmt.Errorf("on_failure deny is not allowed on %s: it has nothing to deny", p)
		}
		return v, nil
	}
	return "", fmt.Errorf("on_failure %q must be deny or allow", v)
}

// hookTimeout applies the default and the 1..MaxHookTimeoutMS range.
func hookTimeout(v *int) (int, error) {
	if v == nil {
		return DefaultHookTimeoutMS, nil
	}
	if *v < 1 || *v > MaxHookTimeoutMS {
		return 0, fmt.Errorf("timeout_ms must be 1..%d", MaxHookTimeoutMS)
	}
	return *v, nil
}

// validateKind checks the command fields; a decision hook (System One) is
// recognised but not loadable until its own packet lands.
func validateKind(h *Hook, s hookSpec, c hookContext) error {
	switch h.Kind {
	case HookKindCommand:
		return validateCommand(h, s, c)
	case HookKindDecision:
		return errors.New("decision hooks are not supported by this build yet")
	}
	return fmt.Errorf("kind %q must be command or decision", s.Kind)
}

// validateCommand checks argv, env and the executable.
func validateCommand(h *Hook, s hookSpec, c hookContext) error {
	if len(s.Argv) == 0 || s.Argv[0] == "" {
		return errors.New("a command hook needs a non-empty argv")
	}
	for _, a := range s.Argv {
		if strings.ContainsRune(a, 0) {
			return errors.New("argv holds a NUL byte")
		}
	}
	for _, name := range s.Env {
		if !hookEnvRE.MatchString(name) || hookSecretRE.MatchString(name) {
			return fmt.Errorf("env name %q is not allowed (names only; secret-looking names are refused)", name)
		}
	}
	h.Argv, h.Env = s.Argv, s.Env
	exe, err := checkExecutable(s.Argv[0], c.dir)
	if err != nil {
		return err
	}
	h.Executable = exe
	return nil
}
