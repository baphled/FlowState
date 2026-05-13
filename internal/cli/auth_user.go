package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/bcrypt"

	"github.com/baphled/flowstate/internal/atomicwrite"
)

// usersJSONPath returns the canonical users.json path for multi-user mode
// (plan §OD-F): ${XDG_CONFIG_HOME}/flowstate/users.json, with the standard
// ~/.config fallback when XDG_CONFIG_HOME is unset.
//
// Centralised here so add/list/remove/reset all read and write the same
// path; tests override XDG_CONFIG_HOME to land at a tmp dir.
//
// Expected:
//   - HOME or XDG_CONFIG_HOME is set in the environment.
//
// Returns:
//   - The absolute path to users.json.
//
// Side effects:
//   - None.
func usersJSONPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "flowstate", "users.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Last-resort fallback: cwd. Logging is the caller's responsibility.
		return filepath.Join(".", ".config", "flowstate", "users.json")
	}
	return filepath.Join(home, ".config", "flowstate", "users.json")
}

// userJSON mirrors the on-disk users.json row shape (see
// internal/auth/identity/source.go). Re-declared in this package because
// the cli package owns the provisioning write path; we do not import
// identity for the struct to keep the engine boundary clean (memory
// project_flowstate_engine_boundary — internal/auth/identity stays a
// leaf, no inbound deps from cli except for Mode constants if needed).
type userJSON struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	DisplayName  string    `json:"display_name,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

type usersFileJSON struct {
	Users []userJSON `json:"users"`
}

// loadUsersFile reads path and returns the parsed container. Missing file
// returns an empty container with err == nil so callers can append to a
// fresh users.json on first add.
//
// Expected:
//   - path is a non-empty absolute path.
//
// Returns:
//   - The parsed users file (zero value if missing).
//   - Error on read or parse failure; missing-file is NOT an error.
//
// Side effects:
//   - Reads the file at path.
func loadUsersFile(path string) (usersFileJSON, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return usersFileJSON{}, nil
		}
		return usersFileJSON{}, fmt.Errorf("reading users file %q: %w", path, err)
	}
	if len(data) == 0 {
		return usersFileJSON{}, nil
	}
	var parsed usersFileJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		return usersFileJSON{}, fmt.Errorf("parsing users file %q: %w", path, err)
	}
	return parsed, nil
}

// writeUsersFile serialises file and writes it to path atomically (per
// memory feedback_atomicity_awareness_uneven — atomicwrite.File for any
// persisted credential surface). The parent directory is created with
// 0o700 if absent.
//
// Expected:
//   - path is a non-empty absolute path.
//   - file may have zero users (e.g. after a remove).
//
// Returns:
//   - nil on success.
//   - Error from mkdir, marshal, or atomicwrite.File.
//
// Side effects:
//   - Creates filepath.Dir(path) with 0o700 if missing.
//   - Replaces path atomically with the serialised body.
func writeUsersFile(path string, file usersFileJSON) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating users-file directory: %w", err)
	}
	body, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling users file: %w", err)
	}
	// Append trailing newline for human-readable diffs.
	body = append(body, '\n')
	return atomicwrite.File(path, body, 0o600)
}

// newAuthUserCmd creates the `flowstate auth user` command group with
// add / list / remove subcommands (plan §"Rollout Plan" PR4/C9 line 555).
//
// Expected:
//   - None (cli package's auth.go already wires the parent `auth` group
//     under the root command).
//
// Returns:
//   - A configured cobra.Command exposing `add`, `list`, `remove`.
//
// Side effects:
//   - None.
func newAuthUserCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage multi-user mode user accounts",
		Long: "Provision, list, and remove user accounts for multi-user mode.\n\n" +
			"Users are persisted as bcrypt-hashed entries in users.json at\n" +
			"${XDG_CONFIG_HOME}/flowstate/users.json (plan §OD-F). There is\n" +
			"no self-signup endpoint — operators provision users out-of-band\n" +
			"via these commands (plan §OD-G).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newAuthUserAddCmd(),
		newAuthUserListCmd(),
		newAuthUserRemoveCmd(),
	)
	return cmd
}

// newAuthUserAddCmd creates the `flowstate auth user add <username>` subcommand.
//
// Expected:
//   - None.
//
// Returns:
//   - A configured cobra.Command.
//
// Side effects:
//   - None at construction time.
func newAuthUserAddCmd() *cobra.Command {
	var (
		password    string
		displayName string
		force       bool
	)
	cmd := &cobra.Command{
		Use:   "add <username>",
		Short: "Provision a new user (bcrypt-hashed password)",
		Long: "Add a user to multi-user mode's users.json. Prompts for a\n" +
			"password unless --password is supplied. Fails on duplicate\n" +
			"username unless --force is set (rotates the entry's hash).",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthUserAdd(cmd, args[0], password, displayName, force)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&password, "password", "", "Password for the new user (skips interactive prompt)")
	flags.StringVar(&displayName, "display-name", "", "Human-readable display name (defaults to username)")
	flags.BoolVar(&force, "force", false, "Rotate the password hash on an existing username")
	return cmd
}

// runAuthUserAdd hashes password with bcrypt cost 12 and writes the row
// to users.json atomically.
//
// Expected:
//   - cmd is non-nil; cmd.OutOrStdout / cmd.InOrStdin drive IO.
//   - username is non-empty.
//   - When password is empty, an interactive prompt reads stdin.
//
// Returns:
//   - nil on success.
//   - Error on missing password, duplicate username (without --force),
//     bcrypt failure, or atomic-write failure.
//
// Side effects:
//   - Reads users.json (creates parent dir if absent).
//   - Reads stdin when password is empty.
//   - Writes users.json atomically.
//   - Prints a confirmation line to cmd.OutOrStdout.
func runAuthUserAdd(cmd *cobra.Command, username, password, displayName string, force bool) error {
	if username == "" {
		return errors.New("username is required")
	}
	if password == "" {
		prompt := fmt.Sprintf("Enter password for %q: ", username)
		password = promptLineFrom(cmd, prompt)
		if password == "" {
			return errors.New("reading password from stdin")
		}
	}

	path := usersJSONPath()
	file, err := loadUsersFile(path)
	if err != nil {
		return err
	}

	for i, u := range file.Users {
		if u.Username == username {
			if !force {
				return fmt.Errorf("user %q already exists; use --force to rotate the password", username)
			}
			// --force path: rotate the hash + (optionally) display name.
			hash, hashErr := bcrypt.GenerateFromPassword([]byte(password), 12)
			if hashErr != nil {
				return fmt.Errorf("bcrypt hash: %w", hashErr)
			}
			file.Users[i].PasswordHash = string(hash)
			if displayName != "" {
				file.Users[i].DisplayName = displayName
			}
			if err := writeUsersFile(path, file); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Rotated password for %q\n", username)
			return nil
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return fmt.Errorf("bcrypt hash: %w", err)
	}
	file.Users = append(file.Users, userJSON{
		Username:     username,
		PasswordHash: string(hash),
		DisplayName:  displayName,
		CreatedAt:    time.Now().UTC(),
	})
	if err := writeUsersFile(path, file); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Added user %q\n", username)
	return nil
}

// newAuthUserListCmd creates the `flowstate auth user list` subcommand.
//
// Expected:
//   - None.
//
// Returns:
//   - A configured cobra.Command.
//
// Side effects:
//   - None at construction time.
func newAuthUserListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured users (redacts password hashes)",
		Long: "Enumerate users from users.json. Tab-separated output:\n" +
			"<username>\\t<display_name>\\t<created_at>. Password hashes\n" +
			"are NEVER printed (plan §Test Strategy line 644).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAuthUserList(cmd)
		},
	}
}

// runAuthUserList prints the user roster as tab-separated rows.
//
// Expected:
//   - cmd is non-nil.
//
// Returns:
//   - nil on success.
//   - Error from loadUsersFile.
//
// Side effects:
//   - Reads users.json (missing file is OK — prints an empty roster).
//   - Writes to cmd.OutOrStdout.
func runAuthUserList(cmd *cobra.Command) error {
	path := usersJSONPath()
	file, err := loadUsersFile(path)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	for _, u := range file.Users {
		display := u.DisplayName
		if display == "" {
			display = u.Username
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", u.Username, display, u.CreatedAt.UTC().Format(time.RFC3339))
	}
	return w.Flush()
}

// newAuthUserRemoveCmd creates the `flowstate auth user remove <username>`
// subcommand. --force makes removal of a non-existent user a no-op (per
// plan §Test Strategy line 645: "idempotent on missing user with --force").
//
// Expected:
//   - None.
//
// Returns:
//   - A configured cobra.Command.
//
// Side effects:
//   - None at construction time.
func newAuthUserRemoveCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "remove <username>",
		Short: "Remove a user from users.json",
		Long: "Remove a user account. Fails on missing username unless\n" +
			"--force is set (idempotent under --force per plan §Test\n" +
			"Strategy line 645). Writes are atomic via internal/atomicwrite.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAuthUserRemove(cmd, args[0], force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Treat missing user as a no-op")
	return cmd
}

// runAuthUserRemove drops the named user from users.json atomically.
//
// Expected:
//   - cmd is non-nil.
//   - username is non-empty.
//
// Returns:
//   - nil on success or no-op-under-force.
//   - Error when username is missing and --force is not set, or on
//     atomic-write failure.
//
// Side effects:
//   - Reads + writes users.json atomically.
//   - Prints a confirmation line.
func runAuthUserRemove(cmd *cobra.Command, username string, force bool) error {
	if username == "" {
		return errors.New("username is required")
	}
	path := usersJSONPath()
	file, err := loadUsersFile(path)
	if err != nil {
		return err
	}
	idx := -1
	for i, u := range file.Users {
		if u.Username == username {
			idx = i
			break
		}
	}
	if idx < 0 {
		if force {
			fmt.Fprintf(cmd.OutOrStdout(), "User %q not present; --force makes this a no-op\n", username)
			return nil
		}
		return fmt.Errorf("user %q not found; use --force to ignore missing", username)
	}
	file.Users = append(file.Users[:idx], file.Users[idx+1:]...)
	if err := writeUsersFile(path, file); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Removed user %q\n", username)
	return nil
}

// promptLineFrom reads a trimmed line from cmd.InOrStdin if it's been
// overridden via SetIn (tests), otherwise from os.Stdin. The existing
// auth.go::promptLine reads os.Stdin directly, which makes it impossible
// to mock under Ginkgo without temporarily redirecting os.Stdin. This
// helper supports both paths so the auth_user_test.go specs can drive
// stdin via cobra's SetIn idiom.
//
// Expected:
//   - cmd is non-nil.
//   - prompt ends in a separator (": ").
//
// Returns:
//   - The trimmed line, or empty on read failure / EOF.
//
// Side effects:
//   - Writes prompt to cmd.OutOrStdout.
//   - Reads a single line from cmd.InOrStdin() or os.Stdin.
func promptLineFrom(cmd *cobra.Command, prompt string) string {
	fmt.Fprint(cmd.OutOrStdout(), prompt)
	in := cmd.InOrStdin()
	if in == nil {
		in = os.Stdin
	}
	line, err := readLine(in)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

// readLine reads from r until '\n' or EOF, returning the bytes (without
// the trailing newline). Used by promptLineFrom; pulled out so tests can
// drive it directly if they need finer control than SetIn provides.
//
// Expected:
//   - r is a non-nil io.Reader.
//
// Returns:
//   - The bytes read up to (but not including) the first newline.
//   - io.EOF if the reader produced no bytes; nil on a delimited line.
//
// Side effects:
//   - Reads from r byte-by-byte to avoid over-consuming on bufio Scanner's
//     fixed-buffer semantics.
func readLine(r io.Reader) (string, error) {
	var b []byte
	buf := make([]byte, 1)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return string(b), nil
			}
			b = append(b, buf[0])
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(b) > 0 {
				return string(b), nil
			}
			return string(b), err
		}
	}
}

// withTimeout is a tiny helper for tests / future hooks that want to
// bound auth-cli operations. Not used in the v1 paths; provided so the
// auth-reset command (next commit) can compose without a fresh wrapper.
//
// Expected:
//   - parent is a non-nil context.
//   - d > 0.
//
// Returns:
//   - A derived context with timeout d, plus its cancel func.
//
// Side effects:
//   - None until the caller invokes the cancel func.
func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

// ensure unused-helper warnings don't fire when only auth-user lands.
var _ = withTimeout
