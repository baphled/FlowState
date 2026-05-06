package session_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("Manager", func() {
	var (
		mgr        *session.Manager
		mockStream *mockStreamer
	)

	BeforeEach(func() {
		mockStream = newMockStreamer()
		mgr = session.NewManager(mockStream)
	})

	Describe("CreateSession", func() {
		Context("with a valid agent ID", func() {
			It("returns a new session with a valid UUID", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess).NotTo(BeNil())
				Expect(sess.ID).NotTo(BeEmpty())
				Expect(len(sess.ID)).To(BeNumerically(">", 10))
			})

			It("sets the agent ID on the session", func() {
				sess, err := mgr.CreateSession("my-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.AgentID).To(Equal("my-agent"))
			})

			It("sets the status to active", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Status).To(Equal("active"))
			})

			It("creates an isolated coordination store for the session", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.CoordinationStore).NotTo(BeNil())
			})

			It("initialises an empty messages slice", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Messages).To(BeEmpty())
			})

			It("sets created and updated timestamps", func() {
				before := time.Now().Add(-time.Second)
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.CreatedAt).To(BeTemporally(">=", before))
				Expect(sess.UpdatedAt).To(BeTemporally(">=", before))
			})

			It("leaves CurrentProviderID and CurrentModelID empty when no defaults are seeded", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.CurrentProviderID).To(BeEmpty())
				Expect(sess.CurrentModelID).To(BeEmpty())
			})
		})
	})

	Describe("CreateSessionWithDefaults", func() {
		// Companion to the May 2026 chip-not-rendering fix on the API side.
		// The session manager owns the in-memory CurrentProviderID and
		// CurrentModelID fields the API handler seeds at create time. These
		// specs pin the contract so a future refactor of CreateSession
		// cannot silently drop the seed.
		It("seeds CurrentProviderID and CurrentModelID when both defaults are non-empty", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "anthropic", "claude-sonnet-4-6")
			Expect(err).NotTo(HaveOccurred())
			Expect(sess.CurrentProviderID).To(Equal("anthropic"))
			Expect(sess.CurrentModelID).To(Equal("claude-sonnet-4-6"))
		})

		It("makes the seeded pair visible via GetSession", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "zai", "glm-4.6")
			Expect(err).NotTo(HaveOccurred())
			loaded, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.CurrentProviderID).To(Equal("zai"))
			Expect(loaded.CurrentModelID).To(Equal("glm-4.6"))
		})

		It("accepts empty defaults (degraded path) with the same shape as CreateSession", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(sess.CurrentProviderID).To(BeEmpty())
			Expect(sess.CurrentModelID).To(BeEmpty())
			Expect(sess.AgentID).To(Equal("agent-x"))
			Expect(sess.Status).To(Equal("active"))
		})
	})

	Describe("appendSessionMessage promotes assistant model+provider", func() {
		// When the engine streams an assistant turn carrying a (model,
		// provider) pair stamped by the engine — the typical hot path —
		// the session metadata must be promoted to match. Without this,
		// a session that started with empty defaults (manifest had no
		// PreferredModels, or a legacy session pre-dating the seed) would
		// keep showing nothing on the chip even though we now know which
		// model produced the answer.
		It("promotes the assistant message's ModelName and ProviderName onto the session", func() {
			sess, err := mgr.CreateSession("agent-x")
			Expect(err).NotTo(HaveOccurred())

			mgr.AppendMessage(sess.ID, session.Message{
				Role:         "assistant",
				Content:      "answer text",
				ModelName:    "glm-4.6",
				ProviderName: "zai",
			})

			loaded, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.CurrentModelID).To(Equal("glm-4.6"))
			Expect(loaded.CurrentProviderID).To(Equal("zai"))
		})

		It("does not clobber session model+provider when the assistant message has empty fields", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "anthropic", "claude-sonnet-4-6")
			Expect(err).NotTo(HaveOccurred())

			// Engine forgot to stamp (legacy provider, mock streamer in a test).
			mgr.AppendMessage(sess.ID, session.Message{
				Role:    "assistant",
				Content: "hi",
			})

			loaded, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.CurrentProviderID).To(Equal("anthropic"),
				"empty ModelName/ProviderName must not erase a previously-seeded pair")
			Expect(loaded.CurrentModelID).To(Equal("claude-sonnet-4-6"))
		})

		It("does not promote model/provider on non-assistant message roles", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "anthropic", "claude-sonnet-4-6")
			Expect(err).NotTo(HaveOccurred())

			// Tool calls / results don't carry these fields, but defend
			// against a future refactor that accidentally populates them.
			mgr.AppendMessage(sess.ID, session.Message{
				Role:         "tool_call",
				Content:      "bash",
				ModelName:    "should-be-ignored",
				ProviderName: "should-be-ignored",
			})

			loaded, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.CurrentModelID).To(Equal("claude-sonnet-4-6"))
			Expect(loaded.CurrentProviderID).To(Equal("anthropic"))
		})
	})

	Describe("Session hierarchy types", func() {
		It("exposes parent identifiers on Session", func() {
			typ := reflect.TypeOf(session.Session{})
			parentID, ok := typ.FieldByName("ParentID")
			Expect(ok).To(BeTrue())
			Expect(parentID.Tag.Get("json")).To(Equal("parent_id"))

			parentSessionID, ok := typ.FieldByName("ParentSessionID")
			Expect(ok).To(BeTrue())
			Expect(parentSessionID.Tag.Get("json")).To(Equal("parent_session_id"))
		})

		It("exposes hierarchy methods on Manager", func() {
			typ := reflect.TypeOf(&session.Manager{})
			childSessions, ok := typ.MethodByName("ChildSessions")
			Expect(ok).To(BeTrue())
			Expect(childSessions.Type.NumIn()).To(Equal(2))
			Expect(childSessions.Type.NumOut()).To(Equal(2))

			sessionTree, ok := typ.MethodByName("SessionTree")
			Expect(ok).To(BeTrue())
			Expect(sessionTree.Type.NumIn()).To(Equal(2))
			Expect(sessionTree.Type.NumOut()).To(Equal(2))
		})

		It("tracks session depth through parent links", func() {
			sessions := map[string]*session.Session{
				"root": {
					ID: "root",
				},
				"child": {
					ID:       "child",
					ParentID: "root",
				},
				"grandchild": {
					ID:       "grandchild",
					ParentID: "child",
				},
			}

			Expect(session.Depth(sessions, "root")).To(Equal(0))
			Expect(session.Depth(sessions, "child")).To(Equal(1))
			Expect(session.Depth(sessions, "grandchild")).To(Equal(2))
		})

		It("creates a child session with correct ParentID and Depth", func() {
			parent, err := mgr.CreateSession("parent-agent")
			Expect(err).NotTo(HaveOccurred())
			Expect(parent).NotTo(BeNil())

			child, err := mgr.CreateWithParent(parent.ID, "child-agent")
			Expect(err).NotTo(HaveOccurred())
			Expect(child).NotTo(BeNil())
			Expect(child.ParentID).To(Equal(parent.ID))
			Expect(child.Depth).To(Equal(parent.Depth + 1))
		})

		It("returns the root session for any descendant", func() {
			root, err := mgr.CreateSession("root-agent")
			Expect(err).NotTo(HaveOccurred())
			child, err := mgr.CreateWithParent(root.ID, "child-agent")
			Expect(err).NotTo(HaveOccurred())
			grandchild, err := mgr.CreateWithParent(child.ID, "grandchild-agent")
			Expect(err).NotTo(HaveOccurred())

			foundRoot, err := mgr.GetRootSession(grandchild.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(foundRoot.ID).To(Equal(root.ID))
		})

		It("returns direct child sessions for a parent", func() {
			root, err := mgr.CreateSession("root-agent")
			Expect(err).NotTo(HaveOccurred())
			child, err := mgr.CreateSession("child-agent")
			Expect(err).NotTo(HaveOccurred())
			grandchild, err := mgr.CreateSession("grandchild-agent")
			Expect(err).NotTo(HaveOccurred())

			child.ParentID = root.ID
			grandchild.ParentID = child.ID

			children, err := mgr.ChildSessions(root.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(children).To(HaveLen(1))
			Expect(children[0].ID).To(Equal(child.ID))
		})

		It("returns a session tree rooted at the requested session", func() {
			root, err := mgr.CreateSession("root-agent")
			Expect(err).NotTo(HaveOccurred())
			child, err := mgr.CreateSession("child-agent")
			Expect(err).NotTo(HaveOccurred())
			grandchild, err := mgr.CreateSession("grandchild-agent")
			Expect(err).NotTo(HaveOccurred())

			child.ParentID = root.ID
			grandchild.ParentID = child.ID

			tree, err := mgr.SessionTree(root.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(tree).To(HaveLen(3))
			Expect(tree[0].ID).To(Equal(root.ID))
			Expect(tree[1].ID).To(Equal(child.ID))
			Expect(tree[2].ID).To(Equal(grandchild.ID))
		})
	})

	Describe("AllSessions", func() {
		Context("when no child sessions exist", func() {
			It("returns an empty slice", func() {
				result, err := mgr.AllSessions()

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeEmpty())
			})
		})

		Context("when sessions with parent IDs exist from multiple runs", func() {
			It("returns only sessions that have a parent ID", func() {
				root, err := mgr.CreateSession("root-agent")
				Expect(err).NotTo(HaveOccurred())

				child1, err := mgr.CreateWithParent(root.ID, "librarian")
				Expect(err).NotTo(HaveOccurred())

				child2, err := mgr.CreateWithParent(root.ID, "explorer")
				Expect(err).NotTo(HaveOccurred())

				result, err := mgr.AllSessions()

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))
				ids := []string{result[0].ID, result[1].ID}
				Expect(ids).To(ContainElement(child1.ID))
				Expect(ids).To(ContainElement(child2.ID))
			})

			It("does not include root sessions without a parent ID", func() {
				_, err := mgr.CreateSession("root-agent")
				Expect(err).NotTo(HaveOccurred())

				result, err := mgr.AllSessions()

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(BeEmpty())
			})

			It("returns children from different parent sessions", func() {
				parent1, err := mgr.CreateSession("parent-agent-1")
				Expect(err).NotTo(HaveOccurred())

				parent2, err := mgr.CreateSession("parent-agent-2")
				Expect(err).NotTo(HaveOccurred())

				child1, err := mgr.CreateWithParent(parent1.ID, "librarian")
				Expect(err).NotTo(HaveOccurred())

				child2, err := mgr.CreateWithParent(parent2.ID, "explorer")
				Expect(err).NotTo(HaveOccurred())

				result, err := mgr.AllSessions()

				Expect(err).NotTo(HaveOccurred())
				Expect(result).To(HaveLen(2))
				ids := []string{result[0].ID, result[1].ID}
				Expect(ids).To(ContainElement(child1.ID))
				Expect(ids).To(ContainElement(child2.ID))
			})
		})
	})

	Describe("GetSession", func() {
		Context("when the session exists", func() {
			It("returns the session", func() {
				created, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := mgr.GetSession(created.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(retrieved.ID).To(Equal(created.ID))
				Expect(retrieved.AgentID).To(Equal(created.AgentID))
			})
		})

		Context("when the session does not exist", func() {
			It("returns an error", func() {
				_, err := mgr.GetSession("nonexistent-id")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("ListSessions", func() {
		Context("when no sessions exist", func() {
			It("returns an empty slice", func() {
				summaries := mgr.ListSessions()
				Expect(summaries).To(BeEmpty())
			})
		})

		Context("when sessions exist", func() {
			It("returns all session summaries", func() {
				_, err := mgr.CreateSession("agent-a")
				Expect(err).NotTo(HaveOccurred())
				_, err = mgr.CreateSession("agent-b")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(2))
			})

			It("includes agent ID in summaries", func() {
				_, err := mgr.CreateSession("my-agent")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))
				Expect(summaries[0].AgentID).To(Equal("my-agent"))
			})

			It("includes message count in summaries", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				mockStream.addChunk(provider.StreamChunk{Content: "Hello"})
				_, err = mgr.SendMessage(ctx, sess.ID, "Hi")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))
				Expect(summaries[0].MessageCount).To(Equal(1))
			})
		})

		Context("frontend SessionSummary contract gaps", func() {
			It("populates UpdatedAt with a non-zero timestamp when sessions exist", func() {
				before := time.Now().Add(-time.Second)
				_, err := mgr.CreateSession("agent-x")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))
				Expect(summaries[0].UpdatedAt.IsZero()).To(BeFalse(),
					"UpdatedAt should be populated, not the zero time 0001-01-01T00:00:00Z")
				Expect(summaries[0].UpdatedAt).To(BeTemporally(">=", before),
					"UpdatedAt should reflect a real session timestamp")
			})

			It("emits a createdAt key in the JSON summary so the Vue SessionSummary contract is satisfied", func() {
				_, err := mgr.CreateSession("agent-with-createdAt")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))

				data, err := json.Marshal(summaries[0])
				Expect(err).NotTo(HaveOccurred())

				var decoded map[string]interface{}
				Expect(json.Unmarshal(data, &decoded)).To(Succeed())
				Expect(decoded).To(HaveKey("createdAt"),
					"frontend SessionSummary expects a createdAt field; backend Summary does not currently emit one")
			})

			It("populates Title from session metadata rather than always returning an empty string", func() {
				_, err := mgr.CreateSession("agent-with-title")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))
				Expect(summaries[0].Title).NotTo(BeEmpty(),
					"Title is hard-coded to empty in ListSessions; frontend SessionSummary expects a meaningful title")
			})

			It("populates CurrentAgentID in the summary so the frontend can restore the user's last-selected agent", func() {
				sess, err := mgr.CreateSession("agent-original")
				Expect(err).NotTo(HaveOccurred())
				Expect(mgr.UpdateSessionAgent(sess.ID, "agent-switched")).To(Succeed())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))
				Expect(summaries[0].CurrentAgentID).To(Equal("agent-switched"),
					"Summary must expose CurrentAgentID so the Vue UI restores the last-selected agent on session-list load")

				data, err := json.Marshal(summaries[0])
				Expect(err).NotTo(HaveOccurred())
				var decoded map[string]interface{}
				Expect(json.Unmarshal(data, &decoded)).To(Succeed())
				Expect(decoded).To(HaveKey("currentAgentId"),
					"frontend SessionSummary expects camelCase currentAgentId in JSON")
			})

			It("backfills a non-zero UpdatedAt for restored sessions that were persisted with the zero time", func() {
				restored := &session.Session{
					ID:        "restored-1",
					AgentID:   "agent-restored",
					Messages:  nil,
					CreatedAt: time.Now().Add(-time.Hour),
				}
				mgr.RestoreSessions([]*session.Session{restored})

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))
				Expect(summaries[0].ID).To(Equal("restored-1"))
				Expect(summaries[0].UpdatedAt.IsZero()).To(BeFalse(),
					"restored sessions with zero UpdatedAt expose 0001-01-01T00:00:00Z to the frontend; manager should backfill from CreatedAt")
			})

			It("populates ParentID in the summary so the frontend can filter parent-only sessions in the switcher", func() {
				parent, err := mgr.CreateSession("parent-agent")
				Expect(err).NotTo(HaveOccurred())
				child, err := mgr.CreateWithParent(parent.ID, "child-agent")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(2))

				summaryByID := map[string]*session.Summary{}
				for _, s := range summaries {
					summaryByID[s.ID] = s
				}

				Expect(summaryByID).To(HaveKey(parent.ID))
				Expect(summaryByID[parent.ID].ParentID).To(BeEmpty(),
					"top-level sessions must report an empty ParentID so the frontend filter !parentId selects them")
				Expect(summaryByID).To(HaveKey(child.ID))
				Expect(summaryByID[child.ID].ParentID).To(Equal(parent.ID),
					"child session summaries must carry the parent identifier so the SessionSwitcher dropdown hides them")
			})

			It("emits a parentId key in the JSON summary so the Vue SessionSummary contract is satisfied", func() {
				parent, err := mgr.CreateSession("agent-parent")
				Expect(err).NotTo(HaveOccurred())
				_, err = mgr.CreateWithParent(parent.ID, "agent-child")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(2))

				var childSummary *session.Summary
				for _, s := range summaries {
					if s.ParentID != "" {
						childSummary = s
						break
					}
				}
				Expect(childSummary).NotTo(BeNil(),
					"at least one summary should be a child carrying ParentID for this scenario")

				data, err := json.Marshal(childSummary)
				Expect(err).NotTo(HaveOccurred())

				var decoded map[string]interface{}
				Expect(json.Unmarshal(data, &decoded)).To(Succeed())
				Expect(decoded).To(HaveKey("parentId"),
					"frontend SessionSummary expects a camelCase parentId field; the Vue switcher filter relies on it to drop child sessions")
				Expect(decoded["parentId"]).To(Equal(parent.ID))
			})
		})
	})

	Describe("SendMessage", func() {
		var (
			ctx  context.Context
			sess *session.Session
		)

		BeforeEach(func() {
			ctx = context.Background()
			var err error
			sess, err = mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when the session exists", func() {
			It("returns a channel of stream chunks", func() {
				mockStream.addChunk(provider.StreamChunk{Content: "Response"})
				ch, err := mgr.SendMessage(ctx, sess.ID, "Hello")
				Expect(err).NotTo(HaveOccurred())
				Expect(ch).NotTo(BeNil())
			})

			It("calls the streamer with the correct agent ID", func() {
				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "Test message")
				Expect(err).NotTo(HaveOccurred())
				Expect(mockStream.lastAgentID).To(Equal("test-agent"))
			})

			It("adds the message to the session history", func() {
				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "User message")
				Expect(err).NotTo(HaveOccurred())

				sess, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Messages).To(HaveLen(1))
				Expect(sess.Messages[0].Role).To(Equal("user"))
				Expect(sess.Messages[0].Content).To(Equal("User message"))
			})

			It("receives chunks from the streamer channel", func() {
				mockStream.addChunk(provider.StreamChunk{Content: "First"})
				mockStream.addChunk(provider.StreamChunk{Content: "Second"})
				mockStream.addChunk(provider.StreamChunk{Done: true})

				ch, err := mgr.SendMessage(ctx, sess.ID, "Hello")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for chunk := range ch {
					if chunk.Content != "" {
						contents = append(contents, chunk.Content)
					}
				}
				Expect(contents).To(ConsistOf("First", "Second"))
			})
		})

		Context("when the session does not exist", func() {
			It("returns an error", func() {
				_, err := mgr.SendMessage(ctx, "nonexistent", "Hello")
				Expect(err).To(HaveOccurred())
			})
		})

		Context("session model/provider overrides", func() {
			It("injects CurrentModelID and CurrentProviderID into the stream context when both are set", func() {
				Expect(mgr.UpdateSessionModel(sess.ID, "anthropic", "claude-opus-4.7")).To(Succeed())
				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "test")
				Expect(err).NotTo(HaveOccurred())
				Expect(mockStream.lastProviderOverride).To(Equal("anthropic"))
				Expect(mockStream.lastModelOverride).To(Equal("claude-opus-4.7"))
			})

			It("injects only CurrentProviderID when CurrentModelID is empty", func() {
				Expect(mgr.UpdateSessionModel(sess.ID, "openai", "")).To(Succeed())

				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "test")
				Expect(err).NotTo(HaveOccurred())
				Expect(mockStream.lastProviderOverride).To(Equal("openai"))
				Expect(mockStream.lastModelOverride).To(BeEmpty())
			})

			It("passes empty overrides when no session model/provider is configured", func() {
				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "test")
				Expect(err).NotTo(HaveOccurred())
				Expect(mockStream.lastProviderOverride).To(BeEmpty())
				Expect(mockStream.lastModelOverride).To(BeEmpty())
			})
		})
	})

	Describe("CloseSession", func() {
		Context("when the session exists", func() {
			It("marks the session as completed", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				err = mgr.CloseSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())

				sess, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Status).To(Equal("completed"))
			})

			It("updates the session timestamp", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())
				originalTime := sess.UpdatedAt

				time.Sleep(time.Millisecond)
				err = mgr.CloseSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())

				sess, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.UpdatedAt).To(BeTemporally(">", originalTime))
			})
		})

		Context("when the session does not exist", func() {
			It("returns an error", func() {
				err := mgr.CloseSession("nonexistent-id")
				Expect(err).To(HaveOccurred())
			})
		})

		// Bug: CloseSession flipped Status in memory but skipped the
		// persist-locked helper that the message-append path uses, so the
		// .meta.json sidecar stayed at "active". On reload the sealed
		// child re-appeared as active, cluttering the UI and risking
		// replay collisions. The behavioural pin is "after sealing, the
		// on-disk status is no longer active" — read back via the public
		// LoadSessionMetadata helper, no internal-call peeking.
		Context("when sessionsDir is configured", func() {
			It("persists the sealed status to the on-disk meta sidecar so the child is not re-loaded as active after restart", func() {
				tmpDir := GinkgoT().TempDir()
				mgr.SetSessionsDir(tmpDir)

				sess, err := mgr.CreateSession("worker")
				Expect(err).NotTo(HaveOccurred())

				Expect(mgr.CloseSession(sess.ID)).To(Succeed())

				loaded, err := session.LoadSessionMetadata(tmpDir, sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded).NotTo(BeNil(),
					"CloseSession must write the .meta.json sidecar — otherwise the sealed child re-appears as active after restart")
				Expect(loaded.Status).NotTo(Equal(string(session.StatusActive)),
					"on-disk status must reflect the seal — staying at \"active\" defeats the whole point of the seal")
				Expect(loaded.Status).To(Equal(string(session.StatusCompleted)))
			})
		})
	})

	Describe("Concurrent access", func() {
		It("handles concurrent CreateSession without races", func() {
			var wg sync.WaitGroup
			const goroutines = 50

			wg.Add(goroutines)
			for range goroutines {
				go func() {
					defer GinkgoRecover()
					defer wg.Done()

					_, err := mgr.CreateSession("agent")
					Expect(err).NotTo(HaveOccurred())
				}()
			}
			wg.Wait()

			summaries := mgr.ListSessions()
			Expect(summaries).To(HaveLen(goroutines))
		})

		It("handles concurrent GetSession without races", func() {
			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			var wg sync.WaitGroup
			const goroutines = 50

			wg.Add(goroutines)
			for range goroutines {
				go func() {
					defer GinkgoRecover()
					defer wg.Done()

					_, err := mgr.GetSession(sess.ID)
					Expect(err).NotTo(HaveOccurred())
				}()
			}
			wg.Wait()
		})

		It("handles concurrent CreateSession and SendMessage without races", func() {
			var wg sync.WaitGroup
			const goroutines = 25

			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			wg.Add(goroutines)
			for range goroutines {
				go func() {
					defer GinkgoRecover()
					defer wg.Done()

					_, err := mgr.CreateSession("new-agent")
					Expect(err).NotTo(HaveOccurred())

					mockStream.addChunk(provider.StreamChunk{Done: true})
					_, err = mgr.SendMessage(context.Background(), sess.ID, "test")
					Expect(err).NotTo(HaveOccurred())
				}()
			}
			wg.Wait()
		})

		// Regression for the data race surfaced during the Track A
		// streaming signal-drop fixes: the API SSE fast-path called
		// GetSession then read sess.Messages outside the manager lock,
		// while SendMessage appended to sess.Messages under the write
		// lock. `go test -race` flagged manager.go:647 (write) vs
		// server.go:756 (read) on the slice header. The fix introduces
		// LastMessageRole, which projects the value under RLock without
		// leaking the *Session pointer past the lock boundary.
		//
		// This spec drives both code paths concurrently against a
		// single session. Pre-fix it surfaces the race in the manager
		// layer (the projection used to require dereferencing
		// session.Messages outside the lock); post-fix the race
		// detector reports clean.
		It("handles concurrent SendMessage and LastMessageRole without races", func() {
			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			// Prime the streamer with enough Done chunks to satisfy
			// every SendMessage iteration; mockStreamer drains a queued
			// channel per call.
			const turns = 50
			for range turns {
				mockStream.addChunk(provider.StreamChunk{Done: true})
			}

			var wg sync.WaitGroup
			wg.Add(2)

			// Writer: hammers SendMessage which appends under WLock.
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for range turns {
					ch, sendErr := mgr.SendMessage(context.Background(), sess.ID, "msg")
					Expect(sendErr).NotTo(HaveOccurred())
					if ch != nil {
						for range ch {
						}
					}
				}
			}()

			// Reader: hammers LastMessageRole which must do the
			// projection under RLock. Pre-fix the equivalent path
			// (GetSession + sess.Messages dereference) raced.
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				for range turns * 4 {
					_, _, readErr := mgr.LastMessageRole(sess.ID)
					Expect(readErr).NotTo(HaveOccurred())
				}
			}()

			wg.Wait()
		})
	})

	Describe("Notifications", func() {
		Context("when injecting a notification", func() {
			It("stores the notification for the given session", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				notification := streaming.CompletionNotificationEvent{
					TaskID:      "task-123",
					Description: "Test task",
					Agent:       "worker-agent",
					Duration:    5 * time.Second,
					Status:      "completed",
					Result:      "Success",
					AgentID:     "worker-agent",
				}

				err = mgr.InjectNotification(sess.ID, notification)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns an error when session ID is empty", func() {
				notification := streaming.CompletionNotificationEvent{
					TaskID:      "task-123",
					Description: "Test task",
					Agent:       "worker-agent",
					Status:      "completed",
				}

				err := mgr.InjectNotification("", notification)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when retrieving notifications", func() {
			It("returns a single injected notification", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				notification := streaming.CompletionNotificationEvent{
					TaskID:      "task-123",
					Description: "Test task",
					Agent:       "worker-agent",
					Duration:    5 * time.Second,
					Status:      "completed",
					Result:      "Success",
					AgentID:     "worker-agent",
				}

				err = mgr.InjectNotification(sess.ID, notification)
				Expect(err).NotTo(HaveOccurred())

				notifications, err := mgr.GetNotifications(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(notifications).To(HaveLen(1))
				Expect(notifications[0].TaskID).To(Equal("task-123"))
				Expect(notifications[0].Description).To(Equal("Test task"))
			})

			It("returns multiple notifications in order", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				for i := range 3 {
					notification := streaming.CompletionNotificationEvent{
						TaskID:      "task-" + string(rune('0'+i)),
						Description: "Task " + string(rune('0'+i)),
						Agent:       "worker",
						Status:      "completed",
					}
					err := mgr.InjectNotification(sess.ID, notification)
					Expect(err).NotTo(HaveOccurred())
				}

				notifications, err := mgr.GetNotifications(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(notifications).To(HaveLen(3))
				Expect(notifications[0].TaskID).To(Equal("task-0"))
				Expect(notifications[1].TaskID).To(Equal("task-1"))
				Expect(notifications[2].TaskID).To(Equal("task-2"))
			})

			It("returns an empty slice when no notifications exist", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				notifications, err := mgr.GetNotifications(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(notifications).To(BeEmpty())
			})

			It("clears notifications after retrieval", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				notification := streaming.CompletionNotificationEvent{
					TaskID:      "task-123",
					Description: "Test task",
					Agent:       "worker",
					Status:      "completed",
				}

				err = mgr.InjectNotification(sess.ID, notification)
				Expect(err).NotTo(HaveOccurred())

				notifications, err := mgr.GetNotifications(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(notifications).To(HaveLen(1))

				notifications, err = mgr.GetNotifications(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(notifications).To(BeEmpty())
			})

			It("returns an error when session ID is empty on get", func() {
				_, err := mgr.GetNotifications("")
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Isolation", func() {
		It("each session has its own coordination store", func() {
			sess1, err := mgr.CreateSession("agent-a")
			Expect(err).NotTo(HaveOccurred())
			sess2, err := mgr.CreateSession("agent-b")
			Expect(err).NotTo(HaveOccurred())

			Expect(sess1.CoordinationStore).NotTo(BeIdenticalTo(sess2.CoordinationStore))
		})

		It("coordination stores are independent", func() {
			sess1, err := mgr.CreateSession("agent-a")
			Expect(err).NotTo(HaveOccurred())
			sess2, err := mgr.CreateSession("agent-b")
			Expect(err).NotTo(HaveOccurred())

			err = sess1.CoordinationStore.Set("key", []byte("value-a"))
			Expect(err).NotTo(HaveOccurred())

			_, err = sess2.CoordinationStore.Get("key")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("SessionRecorder integration", func() {
		var (
			ctx  context.Context
			sess *session.Session
			rec  *spyRecorder
		)

		BeforeEach(func() {
			ctx = context.Background()
			var err error
			sess, err = mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())
			rec = &spyRecorder{}
		})

		Context("when recorder is nil", func() {
			It("returns the raw channel without wrapping", func() {
				mockStream.addChunk(provider.StreamChunk{Content: "raw"})
				mockStream.addChunk(provider.StreamChunk{Done: true})

				ch, err := mgr.SendMessage(ctx, sess.ID, "Hello")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for chunk := range ch {
					if chunk.Content != "" {
						contents = append(contents, chunk.Content)
					}
				}
				Expect(contents).To(ConsistOf("raw"))
			})
		})

		Context("when recorder is set", func() {
			BeforeEach(func() {
				mgr.SetRecorder(rec)
			})

			It("forwards chunks to both recorder and caller", func() {
				mockStream.addChunk(provider.StreamChunk{Content: "First"})
				mockStream.addChunk(provider.StreamChunk{Content: "Second"})
				mockStream.addChunk(provider.StreamChunk{Done: true})

				ch, err := mgr.SendMessage(ctx, sess.ID, "Hello")
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for chunk := range ch {
					if chunk.Content != "" {
						contents = append(contents, chunk.Content)
					}
				}
				Expect(contents).To(ConsistOf("First", "Second"))

				Eventually(func() int {
					rec.mu.Lock()
					defer rec.mu.Unlock()
					return len(rec.chunks)
				}).Should(Equal(3))

				rec.mu.Lock()
				defer rec.mu.Unlock()
				Expect(rec.chunks[0].Content).To(Equal("First"))
				Expect(rec.chunks[1].Content).To(Equal("Second"))
				Expect(rec.chunks[2].Done).To(BeTrue())
			})

			It("records chunks with the correct session ID", func() {
				mockStream.addChunk(provider.StreamChunk{Content: "data"})

				ch, err := mgr.SendMessage(ctx, sess.ID, "Test")
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				Eventually(func() int {
					rec.mu.Lock()
					defer rec.mu.Unlock()
					return len(rec.sessionIDs)
				}).Should(Equal(1))

				rec.mu.Lock()
				defer rec.mu.Unlock()
				Expect(rec.sessionIDs[0]).To(Equal(sess.ID))
			})

			It("receives chunks in the same order as the caller", func() {
				mockStream.addChunk(provider.StreamChunk{Content: "a"})
				mockStream.addChunk(provider.StreamChunk{Content: "b"})
				mockStream.addChunk(provider.StreamChunk{Content: "c"})

				ch, err := mgr.SendMessage(ctx, sess.ID, "Order test")
				Expect(err).NotTo(HaveOccurred())

				var callerOrder []string
				for chunk := range ch {
					if chunk.Content != "" {
						callerOrder = append(callerOrder, chunk.Content)
					}
				}

				Eventually(func() int {
					rec.mu.Lock()
					defer rec.mu.Unlock()
					return len(rec.chunks)
				}).Should(Equal(3))

				rec.mu.Lock()
				defer rec.mu.Unlock()
				var recorderOrder []string
				for _, c := range rec.chunks {
					if c.Content != "" {
						recorderOrder = append(recorderOrder, c.Content)
					}
				}
				Expect(recorderOrder).To(Equal(callerOrder))
			})
		})
	})

	Describe("RegisterSession", func() {
		It("makes the session visible via GetSession", func() {
			mgr.RegisterSession("known-id", "known-agent")
			sess, err := mgr.GetSession("known-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(sess.ID).To(Equal("known-id"))
			Expect(sess.AgentID).To(Equal("known-agent"))
		})

		It("is a no-op when the session already exists", func() {
			original, err := mgr.CreateSession("original-agent")
			Expect(err).NotTo(HaveOccurred())

			mgr.RegisterSession(original.ID, "different-agent")

			retrieved, err := mgr.GetSession(original.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(retrieved.AgentID).To(Equal("original-agent"))
		})

		It("allows CreateWithParent to succeed after registration", func() {
			mgr.RegisterSession("parent-id", "parent-agent")

			child, err := mgr.CreateWithParent("parent-id", "child-agent")
			Expect(err).NotTo(HaveOccurred())
			Expect(child.ParentID).To(Equal("parent-id"))
		})
	})

	Describe("RestoreSessions", func() {
		Context("when restoring sessions that do not yet exist", func() {
			It("registers each session into the manager", func() {
				sessions := []*session.Session{
					{ID: "restored-1", AgentID: "agent-a", Status: "active"},
					{ID: "restored-2", AgentID: "agent-b", Status: "completed"},
				}

				mgr.RestoreSessions(sessions)

				sess, err := mgr.GetSession("restored-1")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.ID).To(Equal("restored-1"))

				sess, err = mgr.GetSession("restored-2")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.ID).To(Equal("restored-2"))
			})
		})

		Context("when a session already exists with the same ID", func() {
			It("skips the duplicate and keeps the original", func() {
				mgr.RegisterSession("existing-id", "original-agent")

				mgr.RestoreSessions([]*session.Session{
					{ID: "existing-id", AgentID: "replacement-agent", Status: "active"},
				})

				sess, err := mgr.GetSession("existing-id")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.AgentID).To(Equal("original-agent"))
			})
		})

		Context("after RestoreSessions with parent-child relationships", func() {
			It("returns child sessions via ChildSessions", func() {
				sessions := []*session.Session{
					{ID: "parent-sess", AgentID: "parent-agent", Status: "active"},
					{ID: "child-sess", ParentID: "parent-sess", AgentID: "child-agent", Status: "active"},
				}

				mgr.RestoreSessions(sessions)

				children, err := mgr.ChildSessions("parent-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(children).To(HaveLen(1))
				Expect(children[0].ID).To(Equal("child-sess"))
			})
		})

		// Boot-time orphan sweep — backstop for the seal persistence hole
		// (May 2026). When the parent process crashes mid-flight, no seal
		// event ever fires for child sessions, so RestoreSessions re-loads
		// them as "active" forever. The sweep runs after the per-session
		// map population: any restored session whose Status is still
		// "active" AND whose UpdatedAt is older than the configured grace
		// window is sealed as "abandoned" (a status distinct from clean
		// completion so forensics and the UI can tell the difference).
		// Sessions within the grace window are left alone — a parent that
		// has just been restarted may legitimately re-attach to its
		// children. Already-sealed sessions are not re-touched.
		Context("boot-time orphan sweep", func() {
			It("seals stale-active sessions as abandoned in memory and on disk after the grace window elapses", func() {
				tmpDir := GinkgoT().TempDir()
				sweepMgr := session.NewManager(&mockStreamer{})
				sweepMgr.SetSessionsDir(tmpDir)
				sweepMgr.SetOrphanGrace(30 * time.Minute)

				stale := &session.Session{
					ID:        "orphan-stale",
					AgentID:   "worker",
					Status:    string(session.StatusActive),
					CreatedAt: time.Now().Add(-2 * time.Hour),
					UpdatedAt: time.Now().Add(-2 * time.Hour),
				}

				sweepMgr.RestoreSessions([]*session.Session{stale})

				inMem, err := sweepMgr.GetSession("orphan-stale")
				Expect(err).NotTo(HaveOccurred())
				Expect(inMem.Status).NotTo(Equal(string(session.StatusActive)),
					"stale-active session must be sealed by the boot sweep — leaving it active resurrects ghost children after every restart")
				Expect(inMem.Status).To(Equal(string(session.StatusAbandoned)),
					"swept sessions are abandoned, not completed — downstream consumers need to distinguish process-crash orphans from clean completions")

				loaded, err := session.LoadSessionMetadata(tmpDir, "orphan-stale")
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded).NotTo(BeNil(),
					"sweep must persist via the same locked-persist helper the seal sites now use — otherwise the sweep is ephemeral and the next restart re-loads the orphan as active")
				Expect(loaded.Status).To(Equal(string(session.StatusAbandoned)),
					"on-disk meta must reflect the sweep so the abandoned status survives across restarts")
			})

			It("preserves active sessions whose UpdatedAt is within the grace window", func() {
				tmpDir := GinkgoT().TempDir()
				sweepMgr := session.NewManager(&mockStreamer{})
				sweepMgr.SetSessionsDir(tmpDir)
				sweepMgr.SetOrphanGrace(30 * time.Minute)

				fresh := &session.Session{
					ID:        "orphan-fresh",
					AgentID:   "worker",
					Status:    string(session.StatusActive),
					CreatedAt: time.Now().Add(-1 * time.Minute),
					UpdatedAt: time.Now().Add(-1 * time.Minute),
				}

				sweepMgr.RestoreSessions([]*session.Session{fresh})

				inMem, err := sweepMgr.GetSession("orphan-fresh")
				Expect(err).NotTo(HaveOccurred())
				Expect(inMem.Status).To(Equal(string(session.StatusActive)),
					"a session inside the grace window must NOT be reaped — the parent may have just restarted and is about to re-attach")
			})

			It("leaves already-sealed sessions unchanged regardless of UpdatedAt age", func() {
				tmpDir := GinkgoT().TempDir()
				sweepMgr := session.NewManager(&mockStreamer{})
				sweepMgr.SetSessionsDir(tmpDir)
				sweepMgr.SetOrphanGrace(30 * time.Minute)

				oldCompleted := &session.Session{
					ID:        "old-completed",
					AgentID:   "worker",
					Status:    string(session.StatusCompleted),
					CreatedAt: time.Now().Add(-3 * time.Hour),
					UpdatedAt: time.Now().Add(-3 * time.Hour),
				}
				oldFailed := &session.Session{
					ID:        "old-failed",
					AgentID:   "worker",
					Status:    string(session.StatusFailed),
					CreatedAt: time.Now().Add(-3 * time.Hour),
					UpdatedAt: time.Now().Add(-3 * time.Hour),
				}

				sweepMgr.RestoreSessions([]*session.Session{oldCompleted, oldFailed})

				gotCompleted, err := sweepMgr.GetSession("old-completed")
				Expect(err).NotTo(HaveOccurred())
				Expect(gotCompleted.Status).To(Equal(string(session.StatusCompleted)),
					"the sweep must only target active sessions — rewriting a completed session loses information")

				gotFailed, err := sweepMgr.GetSession("old-failed")
				Expect(err).NotTo(HaveOccurred())
				Expect(gotFailed.Status).To(Equal(string(session.StatusFailed)),
					"the sweep must not downgrade a known failure to abandoned — failed > abandoned in semantic precedence")
			})
		})
	})

	// AllSessions / ChildSessions previously iterated a Go map directly,
	// returning sessions in non-deterministic order. The delegation
	// picker's left/right arrow navigation depends on a stable creation
	// order — these specs lock the new sort and verify the tiebreaker
	// for sessions that share a CreatedAt timestamp.
	Describe("ordered session listing", func() {
		It("returns AllSessions ordered by CreatedAt (oldest first)", func() {
			t0 := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
			ordered := []*session.Session{
				{ID: "third", ParentID: "root", AgentID: "c", CreatedAt: t0.Add(2 * time.Second)},
				{ID: "first", ParentID: "root", AgentID: "a", CreatedAt: t0},
				{ID: "second", ParentID: "root", AgentID: "b", CreatedAt: t0.Add(1 * time.Second)},
			}
			mgr := session.NewManager(&mockStreamer{})
			mgr.RestoreSessions(append(ordered, &session.Session{ID: "root", AgentID: "root", CreatedAt: t0}))

			got, err := mgr.AllSessions()
			Expect(err).NotTo(HaveOccurred())
			ids := make([]string, len(got))
			for i := range got {
				ids[i] = got[i].ID
			}
			Expect(ids).To(Equal([]string{"first", "second", "third"}),
				"AllSessions must order by CreatedAt — Go map iteration is "+
					"non-deterministic and would otherwise return these in any order")
		})

		It("returns ChildSessions ordered by CreatedAt for a given parent", func() {
			t0 := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
			restored := []*session.Session{
				{ID: "root", AgentID: "root", CreatedAt: t0},
				{ID: "B", ParentID: "root", AgentID: "b", CreatedAt: t0.Add(2 * time.Second)},
				{ID: "A", ParentID: "root", AgentID: "a", CreatedAt: t0.Add(1 * time.Second)},
				{ID: "C", ParentID: "root", AgentID: "c", CreatedAt: t0.Add(3 * time.Second)},
			}
			mgr := session.NewManager(&mockStreamer{})
			mgr.RestoreSessions(restored)

			got, err := mgr.ChildSessions("root")
			Expect(err).NotTo(HaveOccurred())
			ids := make([]string, len(got))
			for i := range got {
				ids[i] = got[i].ID
			}
			Expect(ids).To(Equal([]string{"A", "B", "C"}),
				"ChildSessions must mirror AllSessions's ordering contract")
		})

		It("breaks CreatedAt ties by ID so order stays stable across calls", func() {
			t := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
			restored := []*session.Session{
				{ID: "root", AgentID: "root", CreatedAt: t},
				// All three children share CreatedAt — possible when a
				// parent fans out parallel delegations in one tick.
				{ID: "delta", ParentID: "root", AgentID: "d", CreatedAt: t.Add(time.Second)},
				{ID: "alpha", ParentID: "root", AgentID: "a", CreatedAt: t.Add(time.Second)},
				{ID: "charlie", ParentID: "root", AgentID: "c", CreatedAt: t.Add(time.Second)},
			}
			mgr := session.NewManager(&mockStreamer{})
			mgr.RestoreSessions(restored)

			got, err := mgr.AllSessions()
			Expect(err).NotTo(HaveOccurred())
			Expect(got[0].ID).To(Equal("alpha"))
			Expect(got[1].ID).To(Equal("charlie"))
			Expect(got[2].ID).To(Equal("delta"))
		})
	})

	Describe("TruncateMessages", func() {
		var (
			ctx  context.Context
			sess *session.Session
		)

		BeforeEach(func() {
			ctx = context.Background()
			var err error
			sess, err = mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			// Seed three messages via SendMessage so each has a real UUID.
			for _, text := range []string{"first", "second", "third"} {
				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err = mgr.SendMessage(ctx, sess.ID, text)
				Expect(err).NotTo(HaveOccurred())
			}
			// Drain accumulator goroutines for all sessions before reading.
			Eventually(func() int {
				got, _ := mgr.GetSession(sess.ID)
				return len(got.Messages)
			}).Should(Equal(3))
		})

		Context("happy path", func() {
			It("removes the target message and all subsequent messages", func() {
				got, err := mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				secondID := got.Messages[1].ID

				err = mgr.TruncateMessages(sess.ID, secondID)
				Expect(err).NotTo(HaveOccurred())

				got, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.Messages).To(HaveLen(1))
				Expect(got.Messages[0].Content).To(Equal("first"))
			})

			It("removes all messages when truncating at the first message", func() {
				got, err := mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				firstID := got.Messages[0].ID

				err = mgr.TruncateMessages(sess.ID, firstID)
				Expect(err).NotTo(HaveOccurred())

				got, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(got.Messages).To(BeEmpty())
			})
		})

		Context("when the message ID does not exist", func() {
			It("returns ErrMessageNotFound", func() {
				err := mgr.TruncateMessages(sess.ID, "nonexistent-msg-id")
				Expect(err).To(MatchError(session.ErrMessageNotFound))
			})
		})

		Context("when the session does not exist", func() {
			It("returns ErrSessionNotFound", func() {
				err := mgr.TruncateMessages("nonexistent-session", "any-msg-id")
				Expect(err).To(MatchError(session.ErrSessionNotFound))
			})
		})
	})

	Describe("UpdateSessionAgent", func() {
		var (
			ctx  context.Context
			sess *session.Session
		)

		BeforeEach(func() {
			ctx = context.Background()
			var err error
			sess, err = mgr.CreateSession("agent-a")
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when the agent is switched after session creation", func() {
			It("uses the updated agent, not the original session agent", func() {
				err := mgr.UpdateSessionAgent(sess.ID, "agent-b")
				Expect(err).NotTo(HaveOccurred())

				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err = mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())

				Expect(mockStream.lastAgentID).To(Equal("agent-b"),
					"SendMessage must use the switched-to agent, not the original session agent")
			})

			It("falls back to the original agent if UpdateSessionAgent was never called", func() {
				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())

				Expect(mockStream.lastAgentID).To(Equal("agent-a"),
					"SendMessage must use the original session agent when no switch has occurred")
			})

			It("returns ErrSessionNotFound when the session does not exist", func() {
				err := mgr.UpdateSessionAgent("nonexistent-id", "agent-b")
				Expect(err).To(MatchError(session.ErrSessionNotFound))
			})

			It("persists CurrentAgentID to disk so the choice survives a backend restart", func() {
				tmpDir := GinkgoT().TempDir()
				mgr.SetSessionsDir(tmpDir)
				Expect(mgr.UpdateSessionAgent(sess.ID, "agent-persisted")).To(Succeed())

				loaded, err := session.LoadSessionMetadata(tmpDir, sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(loaded).NotTo(BeNil(),
					"UpdateSessionAgent must write the .meta.json sidecar so a fresh process can restore the user's last-selected agent")
				Expect(loaded.CurrentAgentID).To(Equal("agent-persisted"))
			})

			It("stamps user messages with the current agent so user and assistant messages share the same agent label", func() {
				// Bug: user messages were stamped with sess.AgentID (creation
				// agent) while the assistant-stamping path resolved to
				// CurrentAgentID || AgentID. Result: a user picks "Planner",
				// switches mid-session to "API-Engineer", submits a turn —
				// their own bubble renders under "Planner" while the reply
				// renders under "API-Engineer". Pin the symmetric semantic:
				// when CurrentAgentID is set, user messages carry it too.
				Expect(mgr.UpdateSessionAgent(sess.ID, "agent-b")).To(Succeed())

				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "hello after switch")
				Expect(err).NotTo(HaveOccurred())

				stored, err := mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(stored.Messages).NotTo(BeEmpty())
				Expect(stored.Messages[0].Role).To(Equal("user"))
				Expect(stored.Messages[0].AgentID).To(Equal("agent-b"),
					"user message must be stamped with the active agent (CurrentAgentID), not the creation agent — otherwise the user's bubble and the reply render under different agent labels")
			})

			It("stamps user messages with the creation agent when no switch has occurred", func() {
				// Symmetric guard: when CurrentAgentID is empty, the resolved
				// agent ID is the creation AgentID. Pin that the user
				// message carries it (i.e. the resolution rule is the same
				// as the streaming path, not "always sess.AgentID").
				mockStream.addChunk(provider.StreamChunk{Done: true})
				_, err := mgr.SendMessage(ctx, sess.ID, "hello no switch")
				Expect(err).NotTo(HaveOccurred())

				stored, err := mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(stored.Messages).NotTo(BeEmpty())
				Expect(stored.Messages[0].AgentID).To(Equal("agent-a"))
			})
		})
	})

	Describe("UpdateSessionModel", func() {
		var sess *session.Session

		BeforeEach(func() {
			var err error
			sess, err = mgr.CreateSession("agent-a")
			Expect(err).NotTo(HaveOccurred())
		})

		It("sets CurrentModelID and CurrentProviderID on the session", func() {
			Expect(mgr.UpdateSessionModel(sess.ID, "anthropic", "claude-opus-4.7")).To(Succeed())

			updated, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.CurrentProviderID).To(Equal("anthropic"))
			Expect(updated.CurrentModelID).To(Equal("claude-opus-4.7"))
		})

		It("returns ErrSessionNotFound when the session does not exist", func() {
			err := mgr.UpdateSessionModel("nonexistent-id", "anthropic", "claude-opus-4.7")
			Expect(err).To(MatchError(session.ErrSessionNotFound))
		})

		It("persists CurrentModelID and CurrentProviderID to disk so the choice survives a backend restart", func() {
			tmpDir := GinkgoT().TempDir()
			mgr.SetSessionsDir(tmpDir)
			Expect(mgr.UpdateSessionModel(sess.ID, "anthropic", "claude-opus-4.7")).To(Succeed())

			loaded, err := session.LoadSessionMetadata(tmpDir, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).NotTo(BeNil(),
				"UpdateSessionModel must write the .meta.json sidecar so a fresh process can restore the user's last-selected model + provider")
			Expect(loaded.CurrentProviderID).To(Equal("anthropic"))
			Expect(loaded.CurrentModelID).To(Equal("claude-opus-4.7"))
		})
	})

	// MarkEndedFromEvent is the bus-driven counterpart to CloseSession.
	// Specs cover the four branches: known-session-active, known-session-
	// already-completed (idempotent), known-session-failed (terminal,
	// not downgraded), and unknown-session (silent no-op).
	Describe("MarkEndedFromEvent", func() {
		It("flips an active session's status to completed", func() {
			mgr := session.NewManager(&mockStreamer{})
			sess, err := mgr.CreateSession("worker")
			Expect(err).NotTo(HaveOccurred())
			Expect(sess.Status).To(Equal(string(session.StatusActive)))

			mgr.MarkEndedFromEvent(sess.ID)

			got, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(string(session.StatusCompleted)),
				"a session.ended event must auto-flip Status without "+
					"requiring an explicit CloseSession call")
		})

		It("is idempotent on an already-completed session", func() {
			mgr := session.NewManager(&mockStreamer{})
			sess, err := mgr.CreateSession("worker")
			Expect(err).NotTo(HaveOccurred())
			Expect(mgr.CloseSession(sess.ID)).To(Succeed())
			before, _ := mgr.GetSession(sess.ID)
			beforeAt := before.UpdatedAt

			mgr.MarkEndedFromEvent(sess.ID)

			got, _ := mgr.GetSession(sess.ID)
			Expect(got.Status).To(Equal(string(session.StatusCompleted)))
			Expect(got.UpdatedAt).To(Equal(beforeAt),
				"a duplicate ended event must not bump UpdatedAt — the "+
					"session is already in the terminal completed state")
		})

		It("does NOT downgrade a failed session to completed", func() {
			// RestoreSessions only inserts new IDs (it skips existing
			// entries) so we seed the failed session via that path
			// directly rather than CreateSession + Restore-overwrite.
			// failed is the terminal/most-specific status; an ended
			// event arriving later (which fires for both clean and
			// error stream closes) must not silently rewrite the
			// known failure to a successful completion.
			mgr := session.NewManager(&mockStreamer{})
			mgr.RestoreSessions([]*session.Session{
				{ID: "worker-1", AgentID: "worker", Status: string(session.StatusFailed), CreatedAt: time.Now()},
			})

			mgr.MarkEndedFromEvent("worker-1")

			got, err := mgr.GetSession("worker-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Status).To(Equal(string(session.StatusFailed)),
				"failed > completed in semantic precedence; ended events "+
					"must not rewrite a known failure to a clean completion")
		})

		It("silently ignores unknown session IDs", func() {
			mgr := session.NewManager(&mockStreamer{})
			Expect(func() { mgr.MarkEndedFromEvent("ghost") }).NotTo(Panic())
		})

		It("ignores empty session IDs", func() {
			mgr := session.NewManager(&mockStreamer{})
			Expect(func() { mgr.MarkEndedFromEvent("") }).NotTo(Panic())
		})

		// Symmetric pin to the CloseSession persistence spec above.
		// MarkEndedFromEvent is the bus-driven seal site — it mutated the
		// in-memory Status under the write lock but never invoked the
		// persist-locked helper, so the on-disk meta stayed "active".
		// After restart the orchestrator re-loaded the sealed child as
		// active. Behaviour pinned: when sessionsDir is configured, the
		// bus-driven seal must also flush to the sidecar.
		It("persists the sealed status to the on-disk meta sidecar when sessionsDir is configured", func() {
			tmpDir := GinkgoT().TempDir()
			lockMgr := session.NewManager(&mockStreamer{})
			lockMgr.SetSessionsDir(tmpDir)

			sess, err := lockMgr.CreateSession("worker")
			Expect(err).NotTo(HaveOccurred())

			lockMgr.MarkEndedFromEvent(sess.ID)

			loaded, err := session.LoadSessionMetadata(tmpDir, sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded).NotTo(BeNil(),
				"MarkEndedFromEvent must write the .meta.json sidecar — staying at \"active\" on disk re-loads the sealed child as active after restart")
			Expect(loaded.Status).NotTo(Equal(string(session.StatusActive)),
				"on-disk status must reflect the seal so reload does not resurrect the child as active")
			Expect(loaded.Status).To(Equal(string(session.StatusCompleted)))
		})
	})

	// ── Lock contention: GetSession must not block while AppendMessage persists ──
	//
	// appendSessionMessage holds m.mu (write lock) while calling persistLocked,
	// which serialises the full session JSON and writes to disk. During streaming,
	// AppendMessage is called on every accumulated chunk. Because sync.RWMutex
	// queues writers before readers, a pending AppendMessage write blocks
	// GetSession reads for the entire duration of the file I/O.
	//
	// The fix: release the write lock BEFORE calling persist so that readers
	// (handleSessionMessages, GetSession) can proceed immediately.

	Describe("AppendMessage vs GetSession lock contention", func() {
		var (
			ctx         context.Context
			lockMgr     *session.Manager
			persistGate chan struct{}
			sess        *session.Session
		)

		BeforeEach(func() {
			ctx = context.Background()
			persistGate = make(chan struct{})
			lockMgr = session.NewManager(newMockStreamer())
			lockMgr.SetSessionsDir(GinkgoT().TempDir())

			// Install a blocking persistFn: it acquires persistGate and only
			// returns after the test releases it. This simulates slow disk I/O.
			lockMgr.SetPersistFnForTest(func(_ string, _ *session.Session) error {
				<-persistGate
				return nil
			})

			var err error
			sess, err = lockMgr.CreateSession("agent-1")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			// Unblock any goroutine still waiting on persistGate.
			select {
			case <-persistGate:
			default:
				close(persistGate)
			}
		})

		It("allows GetSession to return while AppendMessage is blocked in persist", func() {
			// Start AppendMessage — it will hold the write lock while waiting
			// for persistGate (simulating slow disk I/O).
			appendStarted := make(chan struct{})
			appendDone := make(chan struct{})
			go func() {
				close(appendStarted)
				lockMgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "streaming chunk"})
				close(appendDone)
			}()

			<-appendStarted
			// Give the goroutine a moment to acquire the write lock and enter persist.
			time.Sleep(5 * time.Millisecond)

			// GetSession MUST NOT wait for AppendMessage's persist to complete.
			// With the bug, this call blocks until persistGate is closed (never
			// in this test, causing a timeout).
			getSessDone := make(chan error, 1)
			go func() {
				_, err := lockMgr.GetSession(sess.ID)
				getSessDone <- err
			}()

			select {
			case err := <-getSessDone:
				Expect(err).NotTo(HaveOccurred())
			case <-time.After(200 * time.Millisecond):
				Fail("GetSession blocked while AppendMessage was holding the write lock during persistence")
			}

			// Unblock AppendMessage so BeforeEach cleanup works.
			close(persistGate)
			Eventually(appendDone, "1s").Should(BeClosed())
		})

		It("persists the message outside the critical section (write lock released before I/O)", func() {
			// Concurrent GetSession calls during AppendMessage must complete
			// without waiting for the persist to finish. This confirms the lock
			// is NOT held during disk I/O.
			ctx = context.Background()
			_ = ctx

			readsDone := make(chan struct{}, 10)
			for range 5 {
				go func() {
					_, _ = lockMgr.GetSession(sess.ID)
					readsDone <- struct{}{}
				}()
			}

			// Trigger the blocking persist.
			appendDone := make(chan struct{})
			go func() {
				lockMgr.AppendMessage(sess.ID, session.Message{Role: "user", Content: "hello"})
				close(appendDone)
			}()

			// All reads should complete within 200ms regardless of persist duration.
			Eventually(func() int {
				return len(readsDone)
			}, "200ms", "10ms").Should(Equal(5))

			// Unblock persist.
			close(persistGate)
			Eventually(appendDone, "1s").Should(BeClosed())
		})
	})

	// ── Lock contention: GetSession must not block while UpdateDelegation persists ──
	//
	// UpdateDelegation holds m.mu (write lock) while calling persistLocked on
	// every streaming delegation chunk. Because sync.RWMutex queues writers
	// before readers, a pending write blocks GetSession reads for the full
	// duration of file I/O.
	//
	// The fix: snapshot state then release the lock BEFORE calling persist.

	Describe("UpdateDelegation vs GetSession lock contention", func() {
		var (
			lockMgr     *session.Manager
			persistGate chan struct{}
			sess        *session.Session
		)

		BeforeEach(func() {
			persistGate = make(chan struct{})
			lockMgr = session.NewManager(newMockStreamer())
			lockMgr.SetSessionsDir(GinkgoT().TempDir())

			var err error
			sess, err = lockMgr.CreateSession("agent-1")
			Expect(err).NotTo(HaveOccurred())
			// AppendMessage must complete before the blocking gate is installed.
			lockMgr.AppendMessage(sess.ID, session.Message{Role: "assistant", Content: "delegating", ChainID: "chain-1"})

			lockMgr.SetPersistFnForTest(func(_ string, _ *session.Session) error {
				<-persistGate
				return nil
			})
		})

		AfterEach(func() {
			select {
			case <-persistGate:
			default:
				close(persistGate)
			}
		})

		It("allows GetSession to return while UpdateDelegation is blocked in persist", func() {
			updateStarted := make(chan struct{})
			updateDone := make(chan struct{})
			go func() {
				close(updateStarted)
				lockMgr.UpdateDelegation(sess.ID, "chain-1", func(m *session.Message) {
					m.Content = "updated"
				})
				close(updateDone)
			}()

			<-updateStarted
			time.Sleep(5 * time.Millisecond)

			getSessDone := make(chan error, 1)
			go func() {
				_, err := lockMgr.GetSession(sess.ID)
				getSessDone <- err
			}()

			select {
			case err := <-getSessDone:
				Expect(err).NotTo(HaveOccurred())
			case <-time.After(200 * time.Millisecond):
				Fail("GetSession blocked while UpdateDelegation was holding the write lock during persistence")
			}

			close(persistGate)
			Eventually(updateDone, "1s").Should(BeClosed())
		})
	})

	// ── Lock contention: GetSession must not block while UpdateSessionAgent persists ──

	Describe("UpdateSessionAgent vs GetSession lock contention", func() {
		var (
			lockMgr     *session.Manager
			persistGate chan struct{}
			sess        *session.Session
		)

		BeforeEach(func() {
			persistGate = make(chan struct{})
			lockMgr = session.NewManager(newMockStreamer())
			lockMgr.SetSessionsDir(GinkgoT().TempDir())

			lockMgr.SetPersistFnForTest(func(_ string, _ *session.Session) error {
				<-persistGate
				return nil
			})

			var err error
			sess, err = lockMgr.CreateSession("agent-1")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			select {
			case <-persistGate:
			default:
				close(persistGate)
			}
		})

		It("allows GetSession to return while UpdateSessionAgent is blocked in persist", func() {
			updateStarted := make(chan struct{})
			updateDone := make(chan struct{})
			go func() {
				close(updateStarted)
				_ = lockMgr.UpdateSessionAgent(sess.ID, "agent-switched")
				close(updateDone)
			}()

			<-updateStarted
			time.Sleep(5 * time.Millisecond)

			getSessDone := make(chan error, 1)
			go func() {
				_, err := lockMgr.GetSession(sess.ID)
				getSessDone <- err
			}()

			select {
			case err := <-getSessDone:
				Expect(err).NotTo(HaveOccurred())
			case <-time.After(200 * time.Millisecond):
				Fail("GetSession blocked while UpdateSessionAgent was holding the write lock during persistence")
			}

			close(persistGate)
			Eventually(updateDone, "1s").Should(BeClosed())
		})
	})

	// ── Lock contention: GetSession must not block while UpdateSessionModel persists ──

	Describe("UpdateSessionModel vs GetSession lock contention", func() {
		var (
			lockMgr     *session.Manager
			persistGate chan struct{}
			sess        *session.Session
		)

		BeforeEach(func() {
			persistGate = make(chan struct{})
			lockMgr = session.NewManager(newMockStreamer())
			lockMgr.SetSessionsDir(GinkgoT().TempDir())

			lockMgr.SetPersistFnForTest(func(_ string, _ *session.Session) error {
				<-persistGate
				return nil
			})

			var err error
			sess, err = lockMgr.CreateSession("agent-1")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			select {
			case <-persistGate:
			default:
				close(persistGate)
			}
		})

		It("allows GetSession to return while UpdateSessionModel is blocked in persist", func() {
			updateStarted := make(chan struct{})
			updateDone := make(chan struct{})
			go func() {
				close(updateStarted)
				_ = lockMgr.UpdateSessionModel(sess.ID, "anthropic", "claude-opus-4.7")
				close(updateDone)
			}()

			<-updateStarted
			time.Sleep(5 * time.Millisecond)

			getSessDone := make(chan error, 1)
			go func() {
				_, err := lockMgr.GetSession(sess.ID)
				getSessDone <- err
			}()

			select {
			case err := <-getSessDone:
				Expect(err).NotTo(HaveOccurred())
			case <-time.After(200 * time.Millisecond):
				Fail("GetSession blocked while UpdateSessionModel was holding the write lock during persistence")
			}

			close(persistGate)
			Eventually(updateDone, "1s").Should(BeClosed())
		})
	})

	// ── Context seeding after restart ────────────────────────────────────────
	//
	// When a session has prior messages and the streamer implements
	// streaming.HistorySeeder, SendMessage must seed the streamer with the
	// session's historical messages (excluding the current turn) before
	// calling Stream — so that after a server restart the engine's in-memory
	// context store is pre-populated and the agent retains conversation context.

	Describe("SendMessage context seeding", func() {
		var (
			ctx         context.Context
			seederMock  *mockHistorySeederStreamer
			seedManager *session.Manager
		)

		BeforeEach(func() {
			ctx = context.Background()
			seederMock = newMockHistorySeederStreamer()
			seedManager = session.NewManager(seederMock)
		})

		It("calls SeedHistory with prior messages before streaming when the streamer is a HistorySeeder", func() {
			// Restore a session that already has two prior messages (simulates
			// the state after a server restart where session messages were loaded
			// from disk but the engine's in-memory store is empty).
			restored := &session.Session{
				ID:      "sess-restart",
				AgentID: "planner",
				Messages: []session.Message{
					{ID: "m1", Role: "user", Content: "What is the plan?", AgentID: "planner"},
					{ID: "m2", Role: "assistant", Content: "Here is the plan.", AgentID: "planner"},
				},
			}
			seedManager.RestoreSessions([]*session.Session{restored})

			seederMock.addChunk(provider.StreamChunk{Done: true})
			_, err := seedManager.SendMessage(ctx, "sess-restart", "Continue the plan.")
			Expect(err).NotTo(HaveOccurred())

			// The seeder must have been called with the two prior messages
			// (the current "Continue the plan." is excluded — it is appended by
			// the engine itself).
			Expect(seederMock.seededSessionID).To(Equal("sess-restart"))
			Expect(seederMock.seededMessages).To(HaveLen(2))
			Expect(seederMock.seededMessages[0].Role).To(Equal("user"))
			Expect(seederMock.seededMessages[0].Content).To(Equal("What is the plan?"))
			Expect(seederMock.seededMessages[1].Role).To(Equal("assistant"))
			Expect(seederMock.seededMessages[1].Content).To(Equal("Here is the plan."))
		})

		It("does not call SeedHistory when the session has no prior messages", func() {
			sess, err := seedManager.CreateSession("planner")
			Expect(err).NotTo(HaveOccurred())

			seederMock.addChunk(provider.StreamChunk{Done: true})
			_, err = seedManager.SendMessage(ctx, sess.ID, "First message ever.")
			Expect(err).NotTo(HaveOccurred())

			Expect(seederMock.seededSessionID).To(BeEmpty())
		})

		It("does not call SeedHistory when the streamer does not implement HistorySeeder", func() {
			plainMock := newMockStreamer()
			plainMgr := session.NewManager(plainMock)

			restored := &session.Session{
				ID:      "sess-plain",
				AgentID: "planner",
				Messages: []session.Message{
					{ID: "m1", Role: "user", Content: "Hello", AgentID: "planner"},
				},
			}
			plainMgr.RestoreSessions([]*session.Session{restored})

			plainMock.addChunk(provider.StreamChunk{Done: true})
			_, err := plainMgr.SendMessage(ctx, "sess-plain", "Continue.")
			Expect(err).NotTo(HaveOccurred())
			// No panic, no error — graceful no-op for non-seeder streamers.
		})
	})
})

// mockHistorySeederStreamer implements both streaming.Streamer and
// streaming.HistorySeeder, capturing what was seeded for assertions.
type mockHistorySeederStreamer struct {
	mockStreamer
	seededSessionID string
	seededMessages  []provider.Message
}

func newMockHistorySeederStreamer() *mockHistorySeederStreamer {
	return &mockHistorySeederStreamer{
		mockStreamer: mockStreamer{chunks: make([]provider.StreamChunk, 0)},
	}
}

func (m *mockHistorySeederStreamer) SeedHistory(sessionID string, messages []provider.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seededSessionID = sessionID
	m.seededMessages = messages
}

type mockStreamer struct {
	mu                   sync.Mutex
	chunks               []provider.StreamChunk
	lastAgentID          string
	lastMessage          string
	lastModelOverride    string
	lastProviderOverride string
}

func newMockStreamer() *mockStreamer {
	return &mockStreamer{
		chunks: make([]provider.StreamChunk, 0),
	}
}

func (m *mockStreamer) addChunk(chunk provider.StreamChunk) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chunks = append(m.chunks, chunk)
}

func (m *mockStreamer) Stream(ctx context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	m.mu.Lock()
	m.lastAgentID = agentID
	m.lastMessage = message
	m.lastModelOverride = session.ModelOverrideFromContext(ctx)
	m.lastProviderOverride = session.ProviderOverrideFromContext(ctx)
	chunks := make([]provider.StreamChunk, len(m.chunks))
	copy(chunks, m.chunks)
	m.chunks = nil
	m.mu.Unlock()

	ch := make(chan provider.StreamChunk, len(chunks))
	for i := range chunks {
		ch <- chunks[i]
	}
	close(ch)

	return ch, nil
}

type spyRecorder struct {
	mu         sync.Mutex
	sessionIDs []string
	chunks     []provider.StreamChunk
}

func (s *spyRecorder) RecordChunk(sessionID string, chunk provider.StreamChunk) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionIDs = append(s.sessionIDs, sessionID)
	s.chunks = append(s.chunks, chunk)
}
