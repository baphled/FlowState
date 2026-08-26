package engine_test

import (
	"errors"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/tool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateToolArgs", func() {
	var schema tool.Schema

	BeforeEach(func() {
		schema = tool.Schema{
			Type: "object",
			Properties: map[string]tool.Property{
				"query": {Type: "string", Description: "search query"},
				"top_k": {Type: "integer", Description: "result count"},
			},
			Required: []string{"query"},
		}
	})

	Context("with valid arguments", func() {
		It("returns sanitised args without error", func() {
			args := map[string]interface{}{"query": "hello", "top_k": float64(5)}
			result, err := engine.ValidateToolArgs(schema, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveKey("query"))
			Expect(result).To(HaveKey("top_k"))
		})
	})

	Context("with unknown arguments", func() {
		It("returns an error naming each unknown key and leaves the args untouched", func() {
			args := map[string]interface{}{
				"query":         "hello",
				"session_id":    "1234567890",
				"subagent_type": "query_vault",
			}
			result, err := engine.ValidateToolArgs(schema, args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown"))
			Expect(err.Error()).To(ContainSubstring("session_id"))
			Expect(err.Error()).To(ContainSubstring("subagent_type"))
			Expect(err.Error()).NotTo(MatchRegexp(`unknown arguments:[^.]*\bquery\b`))
			// The args map must not be mutated — silent stripping is the
			// behaviour we are eliminating, so callers must see the args
			// the model produced, not a sanitised copy.
			Expect(args).To(HaveKey("query"))
			Expect(args).To(HaveKey("session_id"))
			Expect(args).To(HaveKey("subagent_type"))
			Expect(result).To(BeNil())
		})
	})

	Context("with both unknown keys and missing required keys", func() {
		It("surfaces the unknown-key error first so the model can self-correct", func() {
			args := map[string]interface{}{
				"session_id": "abc",
			}
			_, err := engine.ValidateToolArgs(schema, args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unknown"))
			Expect(err.Error()).To(ContainSubstring("session_id"))
		})
	})

	Context("with missing required arguments", func() {
		It("returns an error", func() {
			args := map[string]interface{}{"top_k": float64(5)}
			_, err := engine.ValidateToolArgs(schema, args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("query"))
		})
	})

	Context("with empty schema properties", func() {
		It("passes through all arguments", func() {
			emptySchema := tool.Schema{Type: "object"}
			args := map[string]interface{}{"anything": "goes"}
			result, err := engine.ValidateToolArgs(emptySchema, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveKey("anything"))
		})
	})

	// Schema-aware hint (Recommendation B from the May 2026 investigation of
	// the glm-4.6 `librarian` mis-call). When validation fails on unknown
	// keys, the error message names the expected keys so the model has the
	// schema in front of it on the next turn instead of guessing again. When
	// exactly one unknown key is present, the message also offers a
	// "Did the value 'X' belong in '<expected_key>'?" hint pairing the
	// unknown key's value with each expected key — mirrors the skill-name
	// redirect pattern at engine.go:4817-4834.
	Context("with a single unknown key that looks like a value misplaced as a key", func() {
		var delegateSchema tool.Schema

		BeforeEach(func() {
			delegateSchema = tool.Schema{
				Type: "object",
				Properties: map[string]tool.Property{
					"subagent_type": {Type: "string", Description: "specialised sub-agent"},
					"message":       {Type: "string", Description: "instruction"},
					"chainID":       {Type: "string", Description: "coordination chainID"},
				},
				Required: []string{"subagent_type", "message"},
			}
		})

		It("names the expected keys in the error message", func() {
			args := map[string]interface{}{
				"librarian": "go research the docs",
			}
			_, err := engine.ValidateToolArgs(delegateSchema, args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("librarian"))
			Expect(err.Error()).To(ContainSubstring("Expected:"))
			Expect(err.Error()).To(ContainSubstring("subagent_type"))
			Expect(err.Error()).To(ContainSubstring("message"))
			Expect(err.Error()).To(ContainSubstring("chainID"))
		})

		It("offers a redirect hint pairing the value with each expected key", func() {
			args := map[string]interface{}{
				"librarian": "go research the docs",
			}
			_, err := engine.ValidateToolArgs(delegateSchema, args)
			Expect(err).To(HaveOccurred())
			// The hint shape mirrors the skill-name redirect's
			// "Did you mean..." phrasing so consumers grep one
			// substring across the engine's recovery hints.
			Expect(err.Error()).To(ContainSubstring("Did the value"))
			Expect(err.Error()).To(ContainSubstring("'librarian'"))
		})
	})

	// Telemetry classification (Recommendation E). The returned error
	// carries a structured class so the engine call site can stamp the
	// bus event without re-parsing the message string.
	Context("error classification", func() {
		It("classifies unknown-keys errors as engine.ValidationClassUnknownKeys", func() {
			args := map[string]interface{}{"librarian": "x"}
			_, err := engine.ValidateToolArgs(schema, args)
			Expect(err).To(HaveOccurred())
			var vErr *engine.ValidationError
			Expect(errors.As(err, &vErr)).To(BeTrue())
			Expect(vErr.Class).To(Equal(engine.ValidationClassUnknownKeys))
			Expect(vErr.UnknownKeys).To(ContainElement("librarian"))
			Expect(vErr.ExpectedKeys).To(ConsistOf("query", "top_k"))
		})

		It("classifies missing-required errors as engine.ValidationClassMissingRequired", func() {
			args := map[string]interface{}{"top_k": float64(5)}
			_, err := engine.ValidateToolArgs(schema, args)
			Expect(err).To(HaveOccurred())
			var vErr *engine.ValidationError
			Expect(errors.As(err, &vErr)).To(BeTrue())
			Expect(vErr.Class).To(Equal(engine.ValidationClassMissingRequired))
			Expect(vErr.MissingKeys).To(ContainElement("query"))
		})

		// xml_bleed_detected covers the canonical glm-4.6 failure mode the
		// investigation captured — model emits `value</arg_key>...` or
		// `<arg_key>value</arg_key>` into a string argument. The validator
		// does NOT strip the bleed (Option C from the investigation is
		// explicitly rejected — masks corruption / prompt injection); it
		// classifies so dashboards can attribute provider-side issues.
		It("classifies XML bleed in string values as engine.ValidationClassXMLBleed even when other keys are unknown", func() {
			args := map[string]interface{}{
				"librarian": "message</arg_key>actual content",
			}
			_, err := engine.ValidateToolArgs(schema, args)
			Expect(err).To(HaveOccurred())
			var vErr *engine.ValidationError
			Expect(errors.As(err, &vErr)).To(BeTrue())
			Expect(vErr.Class).To(Equal(engine.ValidationClassXMLBleed),
				"XML bleed in any value takes precedence over unknown-keys so dashboards "+
					"distinguish provider-side corruption from genuine schema confusion")
		})
	})
})
