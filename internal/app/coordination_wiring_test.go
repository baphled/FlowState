package app_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	coordinationtool "github.com/baphled/flowstate/internal/tool/coordination"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildToolsForManifest_IncludesCoordinationTool(t *testing.T) {
	manifestWithCoordination := agent.Manifest{
		ID:   "explorer",
		Name: "Explorer Agent",
		Capabilities: agent.Capabilities{
			Tools: []string{"coordination_store", "read", "bash"},
		},
	}

	store := coordination.NewMemoryStore()
	coordTool := coordinationtool.New(store)

	hasCoordinationStore := hasTool(manifestWithCoordination, coordTool.Name())
	assert.True(t, hasCoordinationStore, "manifest with coordination_store capability should include coordination_store tool")
}

func TestBuildToolsForManifest_WithoutCoordinationTool(t *testing.T) {
	manifestWithoutCoordination := agent.Manifest{
		ID:   "simple-agent",
		Name: "Simple Agent",
		Capabilities: agent.Capabilities{
			Tools: []string{"read", "bash"},
		},
	}

	store := coordination.NewMemoryStore()
	coordTool := coordinationtool.New(store)

	hasCoordinationStore := hasTool(manifestWithoutCoordination, coordTool.Name())
	assert.False(t, hasCoordinationStore, "manifest without coordination_store capability should NOT include coordination_store tool")
}

func TestCoordinationToolIsCorrectType(t *testing.T) {
	store := coordination.NewMemoryStore()
	coordTool := coordinationtool.New(store)

	assert.Equal(t, "coordination_store", coordTool.Name())
	assert.Equal(t, "Read and write shared key-value context during agent delegation chains", coordTool.Description())
}

func TestMemoryStoreSharing(t *testing.T) {
	store := coordination.NewMemoryStore()

	err := store.Set("test-key", []byte("test-value"))
	require.NoError(t, err, "should be able to write to store")

	val, err := store.Get("test-key")
	require.NoError(t, err, "should be able to read from store")
	assert.Equal(t, "test-value", string(val))
}

func TestCoordinationKeyFormat(t *testing.T) {
	store := coordination.NewMemoryStore()
	chainID := "test-chain-123"

	err := store.Set(chainID+"/requirements", []byte("Build a REST API"))
	require.NoError(t, err, "coordinator should write requirements")

	val, err := store.Get(chainID + "/requirements")
	require.NoError(t, err, "delegate should read requirements")
	assert.Equal(t, "Build a REST API", string(val))

	err = store.Set(chainID+"/plan", []byte("# Plan\n- Task 1"))
	require.NoError(t, err, "writer should write plan")

	val, err = store.Get(chainID + "/plan")
	require.NoError(t, err, "reader should read plan")
	assert.Equal(t, "# Plan\n- Task 1", string(val))
}

func hasTool(manifest agent.Manifest, toolName string) bool {
	for _, t := range manifest.Capabilities.Tools {
		if t == toolName {
			return true
		}
	}
	return false
}
