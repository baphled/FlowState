package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/spf13/cobra"
)

// newHealthCmd creates the health command group for provider health management.
//
// Expected:
//   - getApp is a non-nil function that returns the application instance.
//
// Returns:
//   - A configured cobra.Command with health subcommands.
//
// Side effects:
//   - Registers status, reset, and path subcommands.
func newHealthCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "health",
		Short: "Provider health status and management",
		Long:  "View and manage provider/model health state, cooldowns, and consecutive failures.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(
		newHealthStatusCmd(),
		newHealthResetCmd(),
		newHealthPathCmd(),
	)

	return cmd
}

// newHealthStatusCmd creates the `flowstate health status` command.
//
// Returns:
//   - A cobra.Command that prints current health state as a table.
//
// Side effects:
//   - Reads provider-health.json from the user's cache directory.
func newHealthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show provider health state",
		Long:  "Display current provider/model health state including cooldowns and consecutive failures.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			hm := failover.NewHealthManager()
			if err := hm.LoadState(hm.PersistPath()); err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No health state found (file does not exist yet)")
					return nil
				}
				return fmt.Errorf("loading health state: %w", err)
			}

			entries := hm.GetHealthStateEntries()
			if len(entries) == 0 {
				fmt.Println("No providers are currently rate-limited.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
			fmt.Fprintln(w, "PROVIDER\tMODEL\tCOOLDOWN UNTIL\tCONSECUTIVE FAILS\tLAST COOLDOWN")
			fmt.Fprintln(w, "--------\t-----\t--------------\t------------------\t--------------")

			now := time.Now()
			for _, entry := range entries {
				remaining := entry.ExpiresAt.Sub(now).Round(time.Second)
				cooldownStr := entry.ExpiresAt.Local().Format(time.RFC3339)
				if remaining < 0 {
					cooldownStr = "expired"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n",
					entry.Provider,
					entry.Model,
					cooldownStr,
					entry.ConsecutiveFails,
					entry.LastCooldown.Round(time.Second).String(),
				)
			}
			return w.Flush()
		},
	}
}

// newHealthResetCmd creates the `flowstate health reset` command.
//
// Accepts an optional [provider/model] argument. When provided, only that
// pair is cleared. When omitted, ALL entries are cleared.
//
// Returns:
//   - A cobra.Command that resets health state.
//
// Side effects:
//   - Writes an empty (or filtered) state to provider-health.json.
func newHealthResetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "reset [provider/model]",
		Short: "Reset provider health state",
		Long:  "Clear cooldown state for a specific provider/model pair, or all pairs when no argument is given.",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			hm := failover.NewHealthManager()
			// Load existing state so we can do targeted resets.
			_ = hm.LoadState(hm.PersistPath())

			var provider, model string
			if len(args) == 1 {
				parts := strings.SplitN(args[0], "/", 2)
				provider = parts[0]
				if len(parts) > 1 {
					model = parts[1]
				}
			}

			if err := hm.ResetProviderHealth(provider, model); err != nil {
				return fmt.Errorf("resetting health: %w", err)
			}

			if provider == "" && model == "" {
				fmt.Println("Cleared all provider health state.")
			} else {
				fmt.Printf("Cleared health state for %s/%s.\n", provider, model)
			}
			return nil
		},
	}
}

// newHealthPathCmd creates the `flowstate health path` command.
//
// Returns:
//   - A cobra.Command that prints the persist file path.
//
// Side effects:
//   - None.
func newHealthPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Show health state file path",
		Long:  "Print the path to the provider-health.json persist file.",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			hm := failover.NewHealthManager()
			fmt.Println(hm.PersistPath())
			return nil
		},
	}
}
