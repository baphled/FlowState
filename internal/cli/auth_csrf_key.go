package cli

// PR5 C10 — `flowstate auth csrf-key gen` subcommand.
//
// Original PR5 behaviour (commit 4933fdaa): prints a freshly generated
// 32-byte CSRF key (base64-URL encoded, no padding) to stdout. Operators
// were then expected to copy-paste into config.yaml.
//
// Auth Track follow-up (this revision): the default invocation now ALSO
// persists the key into config.yaml's `auth.csrf_key` field via a
// surgical YAML node-level edit (preserves every other key, comment, and
// whitespace) wrapped in internal/atomicwrite.File (memory
// feedback_atomicity_awareness_uneven — any credential persistence on
// the auth surface goes through atomicwrite). The new `--print-only`
// flag preserves the old print-only behaviour for scripted pipelines
// that want to handle persistence themselves.
//
// Env-precedence refusal: FLOWSTATE_AUTH_CSRF_KEY > cfg.Auth.CSRFKey at
// runtime (resolveCSRFKey in serve.go). When the env var is set, writing
// to config would be silently overridden — the command refuses with a
// clear error directing the operator to unset the env var OR pass
// `--print-only`.
//
// Why this exists: PR5/C10 removes the ephemeral-random fallback in
// installAuthFromConfig. When auth.enabled=true and no CSRF key is
// configured (config OR env), the server refuses to start with a clear
// error message pointing at this command. The default now does the
// right thing — generate AND persist — rather than the lossy
// print-and-hope-the-operator-copies flow.

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/atomicwrite"
	"github.com/baphled/flowstate/internal/config"
)

// csrfKeyLen is the byte length of the generated key. 32 bytes (256
// bits) matches gorilla/csrf's expected AuthKey size and the
// session-token mint size (auth/session.go:317), so the same primitive
// underlies both surfaces.
const csrfKeyLen = 32

// configFilePerm is the file mode for a newly-created config.yaml. Auth
// credentials live in this file; tighten to 0600 to match the
// users.json (auth_user.go:121) and OAuth-token persistence convention.
const configFilePerm = 0o600

// configDirPerm is the directory mode used when ensuring the config
// dir exists. Matches auth_user.writeUsersFile (0o700).
const configDirPerm = 0o700

// generateCSRFKey is the indirection point for tests — allows the spec
// to swap in a deterministic source without monkey-patching crypto/rand.
// Production calls cryptoRandRead.
var generateCSRFKey = cryptoRandRead

// cryptoRandRead is the production implementation: 32 random bytes from
// crypto/rand. Pulled out so generateCSRFKey can be overridden in
// tests.
func cryptoRandRead(buf []byte) error {
	_, err := rand.Read(buf)
	return err
}

// newAuthCSRFKeyCmd returns the `flowstate auth csrf-key` parent and
// its `gen` subcommand. The parent is a no-op (prints help) so future
// subcommands (verify, rotate) can land here without breaking the CLI
// shape.
//
// Expected:
//   - Caller wires the result through newAuthCmd's AddCommand.
//
// Returns:
//   - A configured *cobra.Command for `flowstate auth csrf-key`.
//
// Side effects:
//   - On `gen`: writes the encoded key to cmd.OutOrStdout followed by a
//     trailing newline; persists into config.yaml unless --print-only.
func newAuthCSRFKeyCmd() *cobra.Command {
	parent := &cobra.Command{
		Use:   "csrf-key",
		Short: "Generate and manage the FlowState API CSRF signing key",
		Long: `Manage the CSRF signing key used by the FlowState API auth track.

The CSRF key is a 32-byte HMAC secret consumed by gorilla/csrf to sign
the _csrf cookie. It MUST be stable across server restarts — a fresh
key invalidates every active session, forcing users to re-authenticate.

Resolution order (highest precedence first):
  1. FLOWSTATE_AUTH_CSRF_KEY environment variable
  2. auth.csrf_key in config.yaml
  3. (PR5/C10) FAIL — the server refuses to start with auth.enabled=true
     and no key configured. Run "flowstate auth csrf-key gen" to mint
     one — by default the command persists into config.yaml so the next
     server boot reads the key automatically.

The previous ephemeral-random fallback (PR4/C9) is removed in PR5/C10 —
operators must configure the key explicitly. Dev / smoke runs that don't
want auth can set auth.enabled=false.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	parent.AddCommand(newAuthCSRFKeyGenCmd())
	return parent
}

// newAuthCSRFKeyGenCmd returns the `gen` leaf subcommand. Mints a
// 32-byte random key, encodes as URL-safe base64 (RawURLEncoding — no
// padding, matches auth/session.go's mintToken idiom), prints to
// stdout, and (by default) persists into config.yaml's
// `auth.csrf_key` field.
//
// Flags:
//   - --print-only: skip the write; print the key and exit (PR5 original
//     behaviour).
//   - --config <path>: override the config file path. Defaults to the
//     root --config flag, which itself defaults to
//     ${XDG_CONFIG_HOME}/flowstate/config.yaml.
//
// Returns:
//   - A configured *cobra.Command.
//
// Side effects:
//   - Writes the encoded key + newline to cmd.OutOrStdout.
//   - Unless --print-only: opens config.yaml, surgically merges
//     `auth.csrf_key: <key>` (preserving every other key + comment),
//     and writes atomically via atomicwrite.File.
//   - Refuses (returns error) when FLOWSTATE_AUTH_CSRF_KEY is set and
//     --print-only is NOT set — runtime env precedence would silently
//     override the just-written value.
func newAuthCSRFKeyGenCmd() *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate a CSRF signing key and persist it to config.yaml",
		Long: `Generate a 32-byte random CSRF signing key, base64-URL encoded.

Default behaviour:
  The key is printed to stdout AND persisted into config.yaml's
  auth.csrf_key field via an atomic write. Existing keys / sections
  in config.yaml are preserved (surgical YAML edit, not a round-trip).

  When config.yaml does not exist, it is created with just the
  auth.csrf_key field set. The parent directory must already exist —
  the command fails with a clear error otherwise (run a server command
  once or "mkdir -p ~/.config/flowstate" first).

  When FLOWSTATE_AUTH_CSRF_KEY is set in the environment, the command
  refuses with an error: env-var precedence over config would silently
  override the just-written key at runtime. Unset the env var first OR
  pass --print-only to skip the write.

  --print-only: the original PR5 behaviour. Prints the key, skips the
  config write entirely. Useful when piping into a secrets manager or
  composing a shell expression:
    export FLOWSTATE_AUTH_CSRF_KEY=$(flowstate auth csrf-key gen --print-only)

  The write target is the root --config flag, which itself defaults to
  ${XDG_CONFIG_HOME}/flowstate/config.yaml. Use
  "flowstate --config /custom/path auth csrf-key gen" to override.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthCSRFKeyGen(cmd, printOnly)
		},
	}
	cmd.Flags().BoolVar(&printOnly, "print-only", false,
		"Print the key only; do NOT persist it into config.yaml (PR5 original behaviour).")
	return cmd
}

// runAuthCSRFKeyGen mints a key, prints it, and (unless print-only)
// persists it into the config file. The write path is fail-closed when
// FLOWSTATE_AUTH_CSRF_KEY is set in the env — runtime precedence would
// silently override the just-written value.
//
// Expected:
//   - cmd is non-nil; cmd.OutOrStdout / cmd.OutOrErr drive IO.
//   - printOnly: when true, skip the disk write entirely.
//
// Returns:
//   - nil on success.
//   - Error from RNG failure, env-precedence refusal, missing parent
//     directory, or atomicwrite failure.
//
// Side effects:
//   - Writes the encoded key + newline to cmd.OutOrStdout.
//   - Unless printOnly: reads config.yaml (or proceeds with an empty
//     YAML doc if absent), merges auth.csrf_key, writes atomically.
//   - Prints "Saved CSRF key to <path>" to cmd.OutOrStdout on success.
func runAuthCSRFKeyGen(cmd *cobra.Command, printOnly bool) error {
	buf := make([]byte, csrfKeyLen)
	if err := generateCSRFKey(buf); err != nil {
		return fmt.Errorf("generating csrf key: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(buf)

	// Always print first so scripted pipelines can capture the key even
	// when the persistence path returns an error (e.g. operator wants
	// the key in their secrets manager, and the local config write was
	// going to be a belt-and-braces nicety).
	fmt.Fprintln(cmd.OutOrStdout(), encoded)

	if printOnly {
		return nil
	}

	// Env-precedence refusal: FLOWSTATE_AUTH_CSRF_KEY > cfg.Auth.CSRFKey
	// at runtime (resolveCSRFKey:468). Writing to config under an env
	// override would be silently overridden on the next server boot.
	if strings.TrimSpace(os.Getenv("FLOWSTATE_AUTH_CSRF_KEY")) != "" {
		return errors.New(
			"FLOWSTATE_AUTH_CSRF_KEY is set in the environment; " +
				"runtime precedence (env > config) would override the value " +
				"written to config.yaml on the next server boot. " +
				"Either unset FLOWSTATE_AUTH_CSRF_KEY and re-run, " +
				"or pass --print-only to skip the config write.")
	}

	path, err := resolveConfigWritePath(cmd)
	if err != nil {
		return err
	}

	if err := persistCSRFKeyToConfig(path, encoded); err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Saved CSRF key to %s\n", path)
	return nil
}

// resolveConfigWritePath returns the config.yaml path the gen command
// should write to. Precedence:
//
//  1. --config flag on the root command (when the operator passed
//     `flowstate --config /custom/path auth csrf-key gen`).
//  2. Default: ${XDG_CONFIG_HOME}/flowstate/config.yaml (or the
//     ~/.config fallback when XDG_CONFIG_HOME is unset) — same path
//     LoadConfig() resolves first (config.go:1267).
//
// Expected:
//   - cmd is non-nil so cmd.Root() can be walked for the persistent flag.
//
// Returns:
//   - The resolved absolute path.
//   - Error only when the path cannot be cleaned (effectively never).
//
// Side effects:
//   - Reads XDG_CONFIG_HOME and HOME from the environment via
//     config.Dir().
func resolveConfigWritePath(cmd *cobra.Command) (string, error) {
	// Honour the root persistent --config flag when set. cmd.Root() is
	// safe — every gen invocation is rooted under flowstate root.
	if root := cmd.Root(); root != nil {
		if rootFlag := root.PersistentFlags().Lookup("config"); rootFlag != nil &&
			rootFlag.Changed && strings.TrimSpace(rootFlag.Value.String()) != "" {
			return filepath.Clean(rootFlag.Value.String()), nil
		}
	}
	return filepath.Join(config.Dir(), "config.yaml"), nil
}

// persistCSRFKeyToConfig opens path (or proceeds with an empty YAML doc
// if the file is absent), surgically sets auth.csrf_key to encoded
// while preserving every other key, comment, and ordering, and writes
// the result atomically via internal/atomicwrite.File.
//
// Why a node-level edit (not a struct round-trip): a full unmarshal +
// re-marshal would (a) drop unknown YAML keys not modelled on
// AppConfig, (b) inject default values for every un-set field
// (applyDefaults), and (c) lose comments / blank lines. The yaml.v3
// Node API preserves all three. Memory feedback_close_latent_surfaces_too —
// fixing the print-only gap should not introduce a config-clobber
// regression.
//
// Expected:
//   - path is an absolute (or relative) target config file path. The
//     parent directory MUST already exist; the function does NOT
//     create it (the operator should run a server command once or
//     `mkdir -p` themselves — silent mkdir-AnyDir surprises ops).
//   - encoded is the base64url-encoded key (non-empty).
//
// Returns:
//   - nil on success.
//   - Error when the parent directory is missing, the file cannot be
//     parsed, the mutation fails, or the atomic write fails.
//
// Side effects:
//   - Reads path if it exists.
//   - On success, atomically replaces path with the mutated YAML.
func persistCSRFKeyToConfig(path, encoded string) error {
	dir := filepath.Dir(path)
	if _, statErr := os.Stat(dir); statErr != nil {
		if os.IsNotExist(statErr) {
			return fmt.Errorf(
				"config directory %q does not exist; "+
					"create it first (e.g. `mkdir -p %s`) "+
					"or use --config to point at an existing path",
				dir, dir)
		}
		return fmt.Errorf("stat config directory %q: %w", dir, statErr)
	}

	var root yaml.Node
	if data, err := os.ReadFile(path); err == nil {
		if len(strings.TrimSpace(string(data))) > 0 {
			if err := yaml.Unmarshal(data, &root); err != nil {
				return fmt.Errorf("parsing existing config file %q: %w", path, err)
			}
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading config file %q: %w", path, err)
	}

	if err := setYAMLString(&root, []string{"auth", "csrf_key"}, encoded); err != nil {
		return fmt.Errorf("updating auth.csrf_key in %q: %w", path, err)
	}

	body, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("serialising config: %w", err)
	}

	// atomicwrite handles temp-file + fsync + rename + parent-dir fsync
	// (memory feedback_atomicity_awareness_uneven). On failure no
	// .atomicwrite-* temp file is left behind (errors trigger cleanup
	// in writeTempFile).
	if err := atomicwrite.File(path, body, configFilePerm); err != nil {
		return fmt.Errorf("atomic write of %q: %w", path, err)
	}
	return nil
}

// setYAMLString writes value to the nested key path under the document
// root, creating intermediate mapping nodes as needed. The yaml.v3 Node
// type makes this nontrivial: a DocumentNode wraps a single child, and
// scalar / mapping kinds must be filled in explicitly.
//
// Path semantics: ["auth", "csrf_key"] selects root.auth.csrf_key,
// creating an "auth:" block if absent.
//
// Expected:
//   - root is a *yaml.Node (DocumentNode or zero value — zero value is
//     promoted to a Document wrapping an empty Mapping).
//   - path has at least one element.
//   - value is the scalar to write.
//
// Returns:
//   - nil on success.
//   - Error when a node along the path exists but is not a Mapping
//     (e.g. operator hand-wrote `auth: "hello"`).
//
// Side effects:
//   - Mutates *root in place.
func setYAMLString(root *yaml.Node, path []string, value string) error {
	if len(path) == 0 {
		return errors.New("setYAMLString: empty path")
	}

	// Promote a zero-value Node into a Document wrapping an empty
	// Mapping so the rest of the function can assume the standard
	// shape.
	if root.Kind == 0 {
		root.Kind = yaml.DocumentNode
		root.Content = []*yaml.Node{{
			Kind: yaml.MappingNode,
			Tag:  "!!map",
		}}
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return fmt.Errorf("setYAMLString: root is not a Document (kind=%d, content=%d)",
			root.Kind, len(root.Content))
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("setYAMLString: top-level node is not a mapping (kind=%d)", mapping.Kind)
	}

	// Walk the path, creating intermediate Mapping nodes as needed.
	cursor := mapping
	for i, key := range path {
		if cursor.Kind != yaml.MappingNode {
			return fmt.Errorf("setYAMLString: node at path %v is not a mapping (kind=%d)",
				path[:i], cursor.Kind)
		}
		keyNode, valNode := findMappingChild(cursor, key)
		isLast := i == len(path)-1
		if isLast {
			if valNode != nil {
				valNode.Kind = yaml.ScalarNode
				valNode.Tag = "!!str"
				valNode.Value = value
				valNode.Style = 0 // plain scalar; gopkg.in/yaml.v3 will quote if required.
				return nil
			}
			cursor.Content = append(cursor.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value},
			)
			return nil
		}
		if valNode == nil {
			newMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			cursor.Content = append(cursor.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
				newMap,
			)
			cursor = newMap
			continue
		}
		_ = keyNode // unused but kept for symmetry / future expansion.
		cursor = valNode
	}
	return nil
}

// findMappingChild returns the (key, value) node pair for the named key
// within a Mapping node, or (nil, nil) if absent. yaml.v3 stores mapping
// Content as a flat [key, value, key, value, ...] slice.
//
// Expected:
//   - parent is a MappingNode.
//   - key is the YAML key to find.
//
// Returns:
//   - The key node and value node when present; nil pair when absent.
//
// Side effects:
//   - None.
func findMappingChild(parent *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return k, parent.Content[i+1]
		}
	}
	return nil, nil
}
