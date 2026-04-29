package session_test

import (
	"context"
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

			It("includes agent ID and status in summaries", func() {
				_, err := mgr.CreateSession("my-agent")
				Expect(err).NotTo(HaveOccurred())

				summaries := mgr.ListSessions()
				Expect(summaries).To(HaveLen(1))
				Expect(summaries[0].AgentID).To(Equal("my-agent"))
				Expect(summaries[0].Status).To(Equal("active"))
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
	})
})

type mockStreamer struct {
	mu          sync.Mutex
	chunks      []provider.StreamChunk
	lastAgentID string
	lastMessage string
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
