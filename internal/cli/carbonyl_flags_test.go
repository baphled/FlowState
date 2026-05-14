package cli

import (
	"testing"

	"github.com/spf13/cobra"
)

func TestDefaultCarbonylOptions(t *testing.T) {
	opts := defaultCarbonylOptions()

	if opts.FPS != 15 {
		t.Errorf("FPS = %d, want 15", opts.FPS)
	}
	if opts.Zoom != 100 {
		t.Errorf("Zoom = %d, want 100", opts.Zoom)
	}
	if opts.Web {
		t.Error("Web should be false by default")
	}
	if opts.NoCarbonyl {
		t.Error("NoCarbonyl should be false by default")
	}
	if opts.LegacyTUI {
		t.Error("LegacyTUI should be false by default")
	}
}

func TestAddCarbonylFlags(t *testing.T) {
	opts := &CarbonylOptions{}
	cmd := newTestCobraCommand(opts)
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !cmd.Flags().HasFlags() {
		t.Error("command should have flags registered")
	}
}

func TestCarbonylFlagsParsing(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantWeb    bool
		wantFPS    int
		wantZoom   int
		wantNoCarb bool
		wantLegacy bool
	}{
		{
			name:    "defaults",
			args:    []string{},
			wantFPS: 15, wantZoom: 100,
		},
		{
			name:    "web flag",
			args:    []string{"--web"},
			wantWeb: true,
			wantFPS: 15, wantZoom: 100,
		},
		{
			name:    "custom fps and zoom",
			args:    []string{"--fps", "30", "--zoom", "150"},
			wantFPS: 30, wantZoom: 150,
		},
		{
			name:       "no-carbonyl",
			args:       []string{"--no-carbonyl"},
			wantNoCarb: true,
			wantFPS:    15, wantZoom: 100,
		},
		{
			name:       "legacy-tui",
			args:       []string{"--legacy-tui"},
			wantLegacy: true,
			wantFPS:    15, wantZoom: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &CarbonylOptions{}
			cmd := newTestCobraCommand(opts)
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}

			if opts.Web != tt.wantWeb {
				t.Errorf("Web = %v, want %v", opts.Web, tt.wantWeb)
			}
			if opts.FPS != tt.wantFPS {
				t.Errorf("FPS = %d, want %d", opts.FPS, tt.wantFPS)
			}
			if opts.Zoom != tt.wantZoom {
				t.Errorf("Zoom = %d, want %d", opts.Zoom, tt.wantZoom)
			}
			if opts.NoCarbonyl != tt.wantNoCarb {
				t.Errorf("NoCarbonyl = %v, want %v", opts.NoCarbonyl, tt.wantNoCarb)
			}
			if opts.LegacyTUI != tt.wantLegacy {
				t.Errorf("LegacyTUI = %v, want %v", opts.LegacyTUI, tt.wantLegacy)
			}
		})
	}
}

func newTestCobraCommand(opts *CarbonylOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "test",
		RunE: func(_ *cobra.Command, _ []string) error { return nil },
	}
	addCarbonylFlags(cmd, opts)
	return cmd
}
