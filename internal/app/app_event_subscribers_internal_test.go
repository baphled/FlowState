package app

import (
	"context"

	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/learning"

	pluginpkg "github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/external"
)

type recordingPlugin struct {
	mu sync.Mutex

	beforeTools []string
	afterTools  []string
	gotEvents   []string

	afterErr error
}

func (p *recordingPlugin) Init() error     { return nil }
func (p *recordingPlugin) Name() string    { return "recording" }
func (p *recordingPlugin) Version() string { return "test" }
func (p *recordingPlugin) gotBefore() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.beforeTools...)
}
func (p *recordingPlugin) gotAfter() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.afterTools...)
}
func (p *recordingPlugin) gotEventTypes() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.gotEvents...)
}

func (p *recordingPlugin) Hooks() map[pluginpkg.HookType]interface{} {
	return map[pluginpkg.HookType]interface{}{
		pluginpkg.ToolExecBefore: pluginpkg.ToolExecHook(func(_ context.Context, _ string, _ map[string]any) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.beforeTools = append(p.beforeTools, "called")
			return nil
		}),
		pluginpkg.ToolExecAfter: pluginpkg.ToolExecHook(func(_ context.Context, toolName string, _ map[string]any) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.afterTools = append(p.afterTools, toolName)
			if p.afterErr != nil {
				return p.afterErr
			}
			return nil
		}),
		pluginpkg.EventType: pluginpkg.EventHook(func(_ context.Context, evt pluginpkg.Event) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.gotEvents = append(p.gotEvents, evt.Type())
			return nil
		}),
	}
}

type fakeLearningClient struct {
	mu      sync.Mutex
	records []*learning.Record
	err     error
}

func (c *fakeLearningClient) CreateEntities(context.Context, []learning.Entity) ([]learning.Entity, error) {
	return nil, nil
}

func (c *fakeLearningClient) CreateRelations(context.Context, []learning.Relation) ([]learning.Relation, error) {
	return nil, nil
}

func (c *fakeLearningClient) AddObservations(context.Context, []learning.ObservationEntry) ([]learning.ObservationEntry, error) {
	return nil, nil
}

func (c *fakeLearningClient) DeleteEntities(context.Context, []string) ([]string, error) {
	return nil, nil
}

func (c *fakeLearningClient) DeleteObservations(context.Context, []learning.DeletionEntry) error {
	return nil
}

func (c *fakeLearningClient) DeleteRelations(context.Context, []learning.Relation) error { return nil }

func (c *fakeLearningClient) ReadGraph(context.Context) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}

func (c *fakeLearningClient) SearchNodes(context.Context, string) ([]learning.Entity, error) {
	return nil, nil
}

func (c *fakeLearningClient) OpenNodes(context.Context, []string) (learning.KnowledgeGraph, error) {
	return learning.KnowledgeGraph{}, nil
}

func (c *fakeLearningClient) WriteLearningRecord(record *learning.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.records = append(c.records, record)
	return nil
}

type fakeDistiller struct {
	mu      sync.Mutex
	entries []learning.Entry
	err     error
}

func (d *fakeDistiller) Distill(entry learning.Entry) (learning.Entity, []learning.Relation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries = append(d.entries, entry)
	return learning.Entity{}, nil, d.err
}

type pluginBusEvent struct{ typ string }

func (e pluginBusEvent) Type() string         { return e.typ }
func (e pluginBusEvent) Timestamp() time.Time { return time.Unix(0, 0) }
func (e pluginBusEvent) Data() any            { return nil }

var _ = Describe("app event subscribers", func() {
	Describe("subscribeDispatcherHooks", func() {
		It("forwards tool before, result and error events to plugin hooks", func() {
			registry := pluginpkg.NewRegistry()
			rec := &recordingPlugin{}
			Expect(registry.Register(rec)).To(Succeed())

			bus := eventbus.NewEventBus()
			subscribeDispatcherHooks(external.NewDispatcher(registry), bus)

			bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{
				ToolName: "before-tool",
				Args:     map[string]any{"k": "v"},
			}))
			bus.Publish(events.EventToolExecuteResult, events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
				ToolName: "result-tool",
			}))
			bus.Publish(events.EventToolExecuteError, events.NewToolExecuteErrorEvent(events.ToolExecuteErrorEventData{
				ToolName: "error-tool",
			}))

			Eventually(rec.gotBefore).Should(HaveLen(1), "tool.execute.before hook must fire")
			Eventually(rec.gotAfter).Should(ConsistOf("result-tool", "error-tool"),
				"result and error events must both dispatch as tool.execute.after hooks")
		})

		It("ignores messages published with the wrong concrete type", func() {
			registry := pluginpkg.NewRegistry()
			rec := &recordingPlugin{}
			Expect(registry.Register(rec)).To(Succeed())

			bus := eventbus.NewEventBus()
			subscribeDispatcherHooks(external.NewDispatcher(registry), bus)

			bus.Publish(events.EventToolExecuteResult, "not-a-tool-event")
			Consistently(rec.gotAfter, "100ms").Should(BeEmpty(),
				"malformed payloads must be dropped, not dispatched")
		})

		It("forwards plugin events implementing the pluginpkg.Event interface", func() {
			registry := pluginpkg.NewRegistry()
			rec := &recordingPlugin{}
			Expect(registry.Register(rec)).To(Succeed())

			bus := eventbus.NewEventBus()
			subscribeDispatcherHooks(external.NewDispatcher(registry), bus)

			bus.Publish(events.EventPluginEvent, pluginBusEvent{typ: "custom.event"})
			Eventually(rec.gotEventTypes).Should(ConsistOf("custom.event"))
		})
	})

	Describe("handleToolExecuteResult", func() {
		It("records tool results through the learning hook", func() {
			client := &fakeLearningClient{}
			hook := learning.NewLearningHook(client)
			evt := events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
				SessionID: "sess-1",
				ToolName:  "read",
				Result:    "file contents",
			})

			handleToolExecuteResult(evt, hook, nil)

			Eventually(func() []*learning.Record {
				client.mu.Lock()
				defer client.mu.Unlock()
				return client.records
			}).Should(HaveLen(1))
			Expect(client.records[0].Outcome).To(ContainSubstring("read:file contents"))
			Expect(client.records[0].ToolsUsed).NotTo(BeEmpty(), "the call stack is populated from the goroutine frames")
		})

		It("distils an entry when a distiller is configured", func() {
			client := &fakeLearningClient{}
			hook := learning.NewLearningHook(client)
			distiller := &fakeDistiller{}
			evt := events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
				SessionID: "sess-2",
				ToolName:  "write",
				Result:    "ok",
			})

			handleToolExecuteResult(evt, hook, distiller)

			Eventually(func() []learning.Entry {
				distiller.mu.Lock()
				defer distiller.mu.Unlock()
				return distiller.entries
			}).Should(HaveLen(1))
			Expect(distiller.entries[0].ToolsUsed).To(ConsistOf("write"))
		})

		It("ignores messages that are not tool execute result events", func() {
			client := &fakeLearningClient{}
			hook := learning.NewLearningHook(client)

			handleToolExecuteResult("not-an-event", hook, nil)

			Consistently(func() int {
				client.mu.Lock()
				defer client.mu.Unlock()
				return len(client.records)
			}, "100ms").Should(BeZero())
		})
	})

	Describe("startExternalPlugins", func() {
		It("returns immediately on a nil runtime", func() {
			Expect(func() { startExternalPlugins(nil) }).NotTo(Panic())
		})

		It("marks the runtime started even when the plugin directory is empty", func() {
			rt := setupPluginRuntime(&config.AppConfig{
				Plugins: config.PluginsConfig{Dir: GinkgoT().TempDir()},
			})
			Expect(rt).NotTo(BeNil())

			startExternalPlugins(rt)

			Expect(rt.externalStarted).To(BeTrue(), "externalStarted must be set after the attempt completes")
		})

		It("marks the runtime started when discovery fails", func() {
			dir := GinkgoT().TempDir()
			blocker := filepath.Join(dir, "occupied")
			Expect(os.WriteFile(blocker, []byte("not a dir"), 0o644)).To(Succeed())

			rt := setupPluginRuntime(&config.AppConfig{
				Plugins: config.PluginsConfig{Dir: filepath.Join(blocker, "plugins")},
			})

			startExternalPlugins(rt)

			Expect(rt.externalStarted).To(BeTrue(), "a discovery error must still mark the attempt as complete")
		})
	})
})
