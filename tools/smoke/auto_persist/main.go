// Smoke test for the auto-persist on APPROVE wiring.
//
// Builds an App with the standard wiring, gets a wrapped coordination
// store, writes a plan + APPROVE review for a fresh chainID, and waits
// for the post-Set callback to flush the plan to disk via
// PersistApprovedPlan. Verifies the file landed.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/plan"
)

func main() {
	// Use a real Store with the user's data dir so the smoke matches
	// production wiring. The chainID is timestamped to avoid collisions
	// with prior runs.
	chainID := fmt.Sprintf("auto-persist-smoke-%d", time.Now().Unix())
	dataDir := os.ExpandEnv("$HOME/.local/share/flowstate")
	planDir := filepath.Join(dataDir, "plans")

	// Drop any prior file so the post-test check is unambiguous.
	planFile := filepath.Join(planDir, chainID+".md")
	_ = os.Remove(planFile)

	// Construct an App without going through the full New() — we only
	// need the .Store wired so PersistApprovedPlan can write.
	store, err := plan.NewStore(planDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: plan.NewStore: %v\n", err)
		os.Exit(2)
	}
	a := &app.App{Store: store}

	// Build a real file-backed coord store and wrap it the same way
	// app.go does. The wrapper's callback calls PersistApprovedPlan
	// asynchronously — the goroutine does the disk write.
	innerPath := filepath.Join(dataDir, "coordination.json")
	inner, err := coordination.NewFileStore(innerPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: coordination.NewFileStore: %v\n", err)
		os.Exit(2)
	}
	wrapped := coordination.NewPersistingStore(inner, func(cid string, s coordination.Store) {
		if err := a.PersistApprovedPlan(cid, s); err != nil {
			fmt.Fprintf(os.Stderr, "DEBUG callback err: %v\n", err)
		}
	})

	// 1. Write the plan body. Frontmatter is the bare minimum the loader
	//    accepts — id is what becomes the filename.
	planBody := "---\nid: " + chainID + "\ntitle: Auto-persist smoke plan\nstatus: draft\n---\n\n" +
		"# Auto-persist smoke plan\n\n## Tasks\n\n- Task A: prove the wrapper fires\n"
	if err := wrapped.Set(chainID+"/plan", []byte(planBody)); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: writing plan: %v\n", err)
		os.Exit(2)
	}

	// 2. Write the review verdict. The wrapper detects "APPROVE" in the
	//    payload and fires PersistApprovedPlan in a goroutine.
	if err := wrapped.Set(chainID+"/review", []byte("Verdict: APPROVE — looks good")); err != nil {
		fmt.Fprintf(os.Stderr, "FAIL: writing review: %v\n", err)
		os.Exit(2)
	}

	// 3. Wait briefly for the goroutine. The unit test polls up to 500ms;
	//    1s here gives ample headroom for filesystem latency.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		info, err := os.Stat(planFile)
		if err == nil {
			fmt.Fprintln(os.Stderr, "PASS: auto-persist landed at", planFile)
			fmt.Fprintf(os.Stderr, "       size=%d bytes\n", info.Size())
			os.Exit(0)
		}
		time.Sleep(20 * time.Millisecond)
	}

	fmt.Fprintf(os.Stderr, "FAIL: plan never appeared at %s within 1s\n", planFile)
	// Diagnose: did the review actually land?
	if v, err := wrapped.Get(chainID + "/review"); err == nil {
		fmt.Fprintf(os.Stderr, "       coord-store has the review: %s\n", string(v))
	}
	if v, err := wrapped.Get(chainID + "/plan"); err == nil {
		fmt.Fprintf(os.Stderr, "       coord-store has the plan body (%d bytes)\n", len(v))
	}
	os.Exit(1)
}
