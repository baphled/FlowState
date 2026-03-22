package memory_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gexec"
)

const (
	protocolVersion = "2024-11-05"
	expectedTools   = 9
)

var _ = Describe("Memory Server E2E", Label("integration"), func() {
	var serverBin string

	BeforeEach(func() {
		var err error
		serverBin, err = gexec.Build("github.com/baphled/flowstate/cmd/flowstate-memory-server")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(gexec.CleanupBuildArtifacts)
	})

	Describe("initialize handshake", func() {
		It("responds with serverInfo containing the server name", func() {
			tmpDir := GinkgoT().TempDir()
			memFile := filepath.Join(tmpDir, "test.jsonl")

			resp := sendSingleRequest(serverBin, memFile, initializeRequest())

			result := extractResult(resp)
			serverInfo := extractMap(result, "serverInfo")
			Expect(serverInfo["name"]).To(Equal("flowstate-memory-server"))
		})
	})

	Describe("tools/list", func() {
		It("returns all 9 registered tools", func() {
			tmpDir := GinkgoT().TempDir()
			memFile := filepath.Join(tmpDir, "test.jsonl")

			session := startSession(serverBin, memFile)
			DeferCleanup(session.close)

			session.send(initializeRequest())
			session.readResponse()

			session.send(initializedNotification())
			session.send(toolsListRequest(2))
			resp := session.readResponse()

			result := extractResult(resp)
			tools := result["tools"].([]any)
			Expect(tools).To(HaveLen(expectedTools))
		})
	})

	Describe("create_entities via tools/call", func() {
		It("creates an entity and returns it in the response", func() {
			tmpDir := GinkgoT().TempDir()
			memFile := filepath.Join(tmpDir, "test.jsonl")

			session := startSession(serverBin, memFile)
			DeferCleanup(session.close)

			session.send(initializeRequest())
			session.readResponse()
			session.send(initializedNotification())

			session.send(toolCallRequest(2, "create_entities", map[string]any{
				"entities": []map[string]any{
					{
						"name":         "TestEntity",
						"entityType":   "Test",
						"observations": []string{"first observation"},
					},
				},
			}))
			resp := session.readResponse()

			result := extractResult(resp)
			content := extractTextContent(result)
			Expect(content).To(ContainSubstring("TestEntity"))
		})
	})

	Describe("search_nodes via tools/call", func() {
		It("finds a previously created entity by name", func() {
			tmpDir := GinkgoT().TempDir()
			memFile := filepath.Join(tmpDir, "test.jsonl")

			session := startSession(serverBin, memFile)
			DeferCleanup(session.close)

			session.send(initializeRequest())
			session.readResponse()
			session.send(initializedNotification())

			session.send(toolCallRequest(2, "create_entities", map[string]any{
				"entities": []map[string]any{
					{
						"name":         "SearchTarget",
						"entityType":   "Test",
						"observations": []string{"findable observation"},
					},
				},
			}))
			session.readResponse()

			session.send(toolCallRequest(3, "search_nodes", map[string]any{
				"query": "SearchTarget",
			}))
			resp := session.readResponse()

			result := extractResult(resp)
			content := extractTextContent(result)
			Expect(content).To(ContainSubstring("SearchTarget"))
			Expect(content).To(ContainSubstring("findable observation"))
		})
	})

	Describe("delete_entities via tools/call", func() {
		It("removes an entity so it is no longer searchable", func() {
			tmpDir := GinkgoT().TempDir()
			memFile := filepath.Join(tmpDir, "test.jsonl")

			session := startSession(serverBin, memFile)
			DeferCleanup(session.close)

			session.send(initializeRequest())
			session.readResponse()
			session.send(initializedNotification())

			session.send(toolCallRequest(2, "create_entities", map[string]any{
				"entities": []map[string]any{
					{
						"name":         "Ephemeral",
						"entityType":   "Test",
						"observations": []string{"will be deleted"},
					},
				},
			}))
			session.readResponse()

			session.send(toolCallRequest(3, "delete_entities", map[string]any{
				"entityNames": []string{"Ephemeral"},
			}))
			session.readResponse()

			session.send(toolCallRequest(4, "search_nodes", map[string]any{
				"query": "Ephemeral",
			}))
			resp := session.readResponse()

			result := extractResult(resp)
			content := extractTextContent(result)
			Expect(content).NotTo(ContainSubstring("will be deleted"))
		})
	})

	Describe("JSONL persistence", func() {
		It("writes entity data to the JSONL file after create_entities", func() {
			tmpDir := GinkgoT().TempDir()
			memFile := filepath.Join(tmpDir, "persist.jsonl")

			session := startSession(serverBin, memFile)
			DeferCleanup(session.close)

			session.send(initializeRequest())
			session.readResponse()
			session.send(initializedNotification())

			session.send(toolCallRequest(2, "create_entities", map[string]any{
				"entities": []map[string]any{
					{
						"name":         "Persisted",
						"entityType":   "Test",
						"observations": []string{"survives restart"},
					},
				},
			}))
			session.readResponse()

			info, err := os.Stat(memFile)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Size()).To(BeNumerically(">", 0))

			data, err := os.ReadFile(memFile)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("Persisted"))
		})
	})
})

type mcpSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
}

func startSession(bin, memFile string) *mcpSession {
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "MEMORY_FILE_PATH="+memFile)

	stdin, err := cmd.StdinPipe()
	Expect(err).NotTo(HaveOccurred())

	stdout, err := cmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())

	cmd.Stderr = GinkgoWriter

	Expect(cmd.Start()).To(Succeed())

	return &mcpSession{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}
}

func (s *mcpSession) send(msg map[string]any) {
	data, err := json.Marshal(msg)
	Expect(err).NotTo(HaveOccurred())
	_, err = fmt.Fprintf(s.stdin, "%s\n", data)
	Expect(err).NotTo(HaveOccurred())
}

func (s *mcpSession) readResponse() map[string]any {
	line, err := s.reader.ReadBytes('\n')
	Expect(err).NotTo(HaveOccurred())
	var resp map[string]any
	Expect(json.Unmarshal(line, &resp)).To(Succeed())
	return resp
}

func (s *mcpSession) close() {
	s.stdin.Close()
	_ = s.cmd.Wait()
}

func sendSingleRequest(bin, memFile string, req map[string]any) map[string]any {
	session := startSession(bin, memFile)
	defer session.close()
	session.send(req)
	return session.readResponse()
}

func initializeRequest() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test-client", "version": "0.1.0"},
		},
	}
}

func initializedNotification() map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	}
}

func toolsListRequest(id int) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/list",
		"params":  map[string]any{},
	}
}

func toolCallRequest(id int, toolName string, args map[string]any) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      toolName,
			"arguments": args,
		},
	}
}

func extractResult(resp map[string]any) map[string]any {
	result, ok := resp["result"].(map[string]any)
	Expect(ok).To(BeTrue(), "expected response to contain 'result' map, got: %v", resp)
	return result
}

func extractMap(parent map[string]any, key string) map[string]any {
	child, ok := parent[key].(map[string]any)
	Expect(ok).To(BeTrue(), "expected '%s' to be a map, got: %v", key, parent[key])
	return child
}

func extractTextContent(result map[string]any) string {
	contentSlice, ok := result["content"].([]any)
	Expect(ok).To(BeTrue(), "expected 'content' to be an array, got: %v", result)
	Expect(contentSlice).NotTo(BeEmpty())

	first, ok := contentSlice[0].(map[string]any)
	Expect(ok).To(BeTrue(), "expected content[0] to be a map")

	text, ok := first["text"].(string)
	Expect(ok).To(BeTrue(), "expected content[0].text to be a string")
	return text
}
