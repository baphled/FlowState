package permissionrequest_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/permissionrequest"
)

// Slice 2 (Permission Mode ModeAskUser Extension plan, May 2026) §17.3
// commits to the turn.Registry.Start pattern: locked pre-check + insert
// + sentinel ErrTurnConflict on duplicate. PermissionRequest registry
// mirrors that shape; these specs pin the contract.
var _ = Describe("permissionrequest.Registry", func() {
	mkReq := func(id, session string) permissionrequest.PermissionRequest {
		return permissionrequest.PermissionRequest{
			RequestID:    id,
			ToolName:     "read",
			AgentName:    "coordinator",
			Resource:     "/tmp/x",
			DenialReason: "access denied",
			SessionID:    session,
			Mode:         "ask",
		}
	}

	Describe("Register then Resolve — happy path", func() {
		It("delivers the grant via Wait", func() {
			reg := permissionrequest.NewRegistry()
			req := mkReq("req-1", "sess-A")
			Expect(reg.Register(req)).To(Succeed())

			var (
				gotGrant permissionrequest.PermissionGrant
				gotErr   error
				wg       sync.WaitGroup
			)
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				gotGrant, gotErr = reg.Wait(ctx, "req-1")
			}()

			// Brief settle so the Wait goroutine is parked on the receive
			// before Resolve fires. Without this we still pass (the
			// buffered send wins regardless of receive ordering) but the
			// "Wait parked → Resolve wakes" path is the production flow
			// and should be exercised explicitly.
			time.Sleep(10 * time.Millisecond)
			Expect(reg.Resolve("req-1", permissionrequest.PermissionGrant{
				Scope: permissionrequest.ScopeOnce,
			})).To(Succeed())

			wg.Wait()
			Expect(gotErr).NotTo(HaveOccurred())
			Expect(gotGrant.Scope).To(Equal(permissionrequest.ScopeOnce))
			Expect(gotGrant.RequestID).To(Equal("req-1"),
				"Resolve must stamp the request_id on the grant so loggers/metrics correlate without consulting the original PermissionRequest")
		})

		It("removes the request from PendingForSession after Resolve", func() {
			reg := permissionrequest.NewRegistry()
			Expect(reg.Register(mkReq("req-1", "sess-A"))).To(Succeed())
			Expect(reg.PendingForSession("sess-A")).To(ConsistOf("req-1"))

			// Drain the grant so Resolve can deliver and clean up.
			done := make(chan struct{})
			go func() {
				defer close(done)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_, _ = reg.Wait(ctx, "req-1")
			}()
			time.Sleep(10 * time.Millisecond)
			Expect(reg.Resolve("req-1", permissionrequest.PermissionGrant{
				Scope: permissionrequest.ScopeOnce,
			})).To(Succeed())
			<-done

			Expect(reg.PendingForSession("sess-A")).To(BeEmpty(),
				"Resolve must drop the request from the per-session index so PendingForSession reflects only in-flight requests")
			Expect(reg.PendingCount()).To(Equal(0))
		})
	})

	Describe("Register duplicate request_id (plan §15.3 reviewer condition)", func() {
		It("returns ErrPermissionRequestExists on the second insert", func() {
			reg := permissionrequest.NewRegistry()
			Expect(reg.Register(mkReq("dup-id", "sess-A"))).To(Succeed())

			err := reg.Register(mkReq("dup-id", "sess-A"))
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, permissionrequest.ErrPermissionRequestExists)).To(BeTrue(),
				"the second Register on the same request_id MUST return the sentinel so two goroutines racing on the same (tool, resource) pair fail closed instead of silently overwriting the earlier grant channel")
		})

		It("does not mutate the existing entry on duplicate Register", func() {
			reg := permissionrequest.NewRegistry()
			Expect(reg.Register(mkReq("dup-id", "sess-A"))).To(Succeed())
			_ = reg.Register(mkReq("dup-id", "sess-A"))

			// PendingForSession must still report exactly one entry —
			// the duplicate Register must not append a second id to
			// the per-session index (which would leak on Resolve).
			Expect(reg.PendingForSession("sess-A")).To(ConsistOf("dup-id"))
			Expect(reg.PendingCount()).To(Equal(1))
		})
	})

	Describe("Wait with ctx cancel", func() {
		It("returns ctx.Err() — NOT a synthetic ScopeDeny — so callers can distinguish 'operator denied' from 'request withdrawn'", func() {
			reg := permissionrequest.NewRegistry()
			Expect(reg.Register(mkReq("req-1", "sess-A"))).To(Succeed())

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // pre-cancel; Wait must short-circuit immediately

			grant, err := reg.Wait(ctx, "req-1")
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(context.Canceled),
				"plan §5: cancellation MUST return ctx.Err() so the suspension timeout path can distinguish itself from the operator-Deny path")
			Expect(grant).To(BeZero(),
				"on cancellation Wait MUST NOT fabricate a grant; the caller branches on err == nil to apply the grant effect")
		})

		It("cleans up the registry entry when Wait exits via ctx.Done", func() {
			reg := permissionrequest.NewRegistry()
			Expect(reg.Register(mkReq("req-1", "sess-A"))).To(Succeed())

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			_, err := reg.Wait(ctx, "req-1")
			Expect(err).To(HaveOccurred())

			Expect(reg.PendingForSession("sess-A")).To(BeEmpty(),
				"a Wait that exits via ctx.Done MUST remove the pending entry so the registry does not accumulate zombie requests; plan §5 R4 budgets the operator-timeout path on this cleanup")
		})
	})

	Describe("Resolve on unknown requestID", func() {
		It("returns ErrPermissionRequestNotFound", func() {
			reg := permissionrequest.NewRegistry()
			err := reg.Resolve("nope", permissionrequest.PermissionGrant{Scope: permissionrequest.ScopeOnce})
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, permissionrequest.ErrPermissionRequestNotFound)).To(BeTrue(),
				"a late grant from a stale tab is benign on the wire surface but the registry MUST distinguish it from a duplicate-Register collision so observability can track 'grant-after-timeout' separately from 'two prompts on the same call'")
		})
	})

	// Plan §5 + memory:project_flowstate_streamer_request_lifetime_coupling.
	// The suspension goroutine must NOT be cancelled by the HTTP request
	// context (a tab-close would otherwise resolve every in-flight
	// permission prompt before the operator can grant). The
	// PermissionPrompter implementation in app.go calls Wait with a
	// context.WithoutCancel-derived ctx; this spec pins that the registry
	// honours that contract — a parent cancel does NOT propagate.
	Describe("Wait suspension survives parent-context cancel via WithoutCancel", func() {
		It("does not return when the parent context is cancelled but the Wait ctx is WithoutCancel-derived", func() {
			reg := permissionrequest.NewRegistry()
			Expect(reg.Register(mkReq("req-1", "sess-A"))).To(Succeed())

			parent, cancelParent := context.WithCancel(context.Background())
			// Mimic the app.go boundary: the prompter passes a ctx that
			// drops the parent cancel chain. Wait's only termination
			// path is now a Resolve or a timeout on the inner ctx.
			waitCtx, cancelWait := context.WithTimeout(context.WithoutCancel(parent), 200*time.Millisecond)
			defer cancelWait()

			waitDone := make(chan struct{})
			var gotErr error
			var gotGrant permissionrequest.PermissionGrant
			go func() {
				defer close(waitDone)
				gotGrant, gotErr = reg.Wait(waitCtx, "req-1")
			}()

			// Cancel the PARENT — the streamer ctx in production. The
			// suspended Wait MUST NOT observe this cancellation.
			cancelParent()

			// Brief settle. If the WithoutCancel barrier leaked, waitDone
			// closes here; the assertion below would fail.
			select {
			case <-waitDone:
				Fail("Wait MUST NOT terminate when the parent ctx is cancelled — context.WithoutCancel at the suspension boundary is the load-bearing invariant from memory:project_flowstate_streamer_request_lifetime_coupling")
			case <-time.After(30 * time.Millisecond):
				// Expected — Wait still parked on the receive.
			}

			// Now grant the request via the proper Resolve path. Wait
			// returns the grant immediately.
			Expect(reg.Resolve("req-1", permissionrequest.PermissionGrant{
				Scope: permissionrequest.ScopeSession,
			})).To(Succeed())

			Eventually(waitDone, 500*time.Millisecond).Should(BeClosed(),
				"once Resolve fires the grant Wait MUST wake immediately; the WithoutCancel barrier blocks the parent cancel, not the grant signal")
			Expect(gotErr).NotTo(HaveOccurred())
			Expect(gotGrant.Scope).To(Equal(permissionrequest.ScopeSession))
		})
	})

	// R4 observability guard — the PendingCount surface is the same
	// number the permission_pending gauge tracks, so dashboard consumers
	// and ops alerts have a single source of truth.
	Describe("PendingCount", func() {
		It("counts active suspensions across sessions", func() {
			reg := permissionrequest.NewRegistry()
			Expect(reg.Register(mkReq("a", "sess-1"))).To(Succeed())
			Expect(reg.Register(mkReq("b", "sess-1"))).To(Succeed())
			Expect(reg.Register(mkReq("c", "sess-2"))).To(Succeed())

			Expect(reg.PendingCount()).To(Equal(3))

			done := make(chan struct{})
			go func() {
				defer close(done)
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_, _ = reg.Wait(ctx, "b")
			}()
			time.Sleep(10 * time.Millisecond)
			Expect(reg.Resolve("b", permissionrequest.PermissionGrant{
				Scope: permissionrequest.ScopeOnce,
			})).To(Succeed())
			<-done

			Expect(reg.PendingCount()).To(Equal(2))
			Expect(reg.PendingForSession("sess-1")).To(ConsistOf("a"))
			Expect(reg.PendingForSession("sess-2")).To(ConsistOf("c"))
		})
	})
})
