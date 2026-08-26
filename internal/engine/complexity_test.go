package engine_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("ComplexityEstimation", func() {
	Describe("EstimateComplexity", func() {
		Context("simple tasks", func() {
			It("classifies a short summarise request as Simple", func() {
				msg := "Summarise this paragraph for me."
				complexity, signals := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexitySimple))
				Expect(signals.Length).To(BeNumerically("<", 300))
				Expect(signals.HasCode).To(BeFalse())
				Expect(signals.ComplexityMatches).To(Equal(0))
				Expect(signals.SimpleMatches).To(BeNumerically(">=", 1))
			})

			It("classifies a quick question as Simple", func() {
				msg := "What is the capital of France?"
				complexity, _ := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexitySimple))
			})

			It("classifies a brief list request as Simple", func() {
				msg := "List the files in the current directory."
				complexity, _ := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexitySimple))
			})
		})

		Context("complex tasks", func() {
			It("classifies a very long message as Complex", func() {
				msg := strings.Repeat("We need to implement a comprehensive solution. ", 40)
				complexity, signals := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexityComplex))
				Expect(signals.Length).To(BeNumerically(">", 1500))
			})

			It("classifies a message with many complexity keywords as Complex", func() {
				msg := "We need to refactor the authentication module, migrate the database, " +
					"and debug the deployment pipeline."
				complexity, signals := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexityComplex))
				Expect(signals.ComplexityMatches).To(BeNumerically(">=", 3))
			})

			It("classifies a code-heavy message with complexity keywords as Complex", func() {
				msg := "```go\nfunc main() {}\n```\nPlease refactor this function and debug the edge cases."
				complexity, signals := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexityComplex))
				Expect(signals.HasCode).To(BeTrue())
				Expect(signals.ComplexityMatches).To(BeNumerically(">=", 2))
			})
		})

		Context("moderate tasks", func() {
			It("classifies a medium-length request without strong signals as Moderate", func() {
				msg := "Can you help me write a test for the user service? It should cover the happy path and error cases."
				complexity, _ := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexityModerate))
			})

			It("classifies a single-keyword request as Moderate", func() {
				msg := "Implement a new validation function for the config loader."
				complexity, signals := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexityModerate))
				Expect(signals.ComplexityMatches).To(Equal(1))
			})
		})

		Context("edge cases", func() {
			It("classifies empty string as Moderate (default)", func() {
				complexity, _ := engine.EstimateComplexity("")
				Expect(complexity).To(Equal(engine.ComplexityModerate))
			})

			It("classifies a short message with only complexity keywords as Moderate", func() {
				msg := "Debug this."
				complexity, signals := engine.EstimateComplexity(msg)
				Expect(complexity).To(Equal(engine.ComplexityModerate))
				Expect(signals.ComplexityMatches).To(Equal(1))
				Expect(signals.Length).To(BeNumerically("<", 300))
			})
		})
	})

	Describe("TaskComplexity", func() {
		Describe("String", func() {
			It("returns human-readable names for each tier", func() {
				Expect(engine.ComplexitySimple.String()).To(Equal("simple"))
				Expect(engine.ComplexityModerate.String()).To(Equal("moderate"))
				Expect(engine.ComplexityComplex.String()).To(Equal("complex"))
				Expect(engine.ComplexityUnknown.String()).To(Equal("unknown"))
			})
		})

		Describe("EnforcesTodoGate", func() {
			It("returns true only for Complex", func() {
				Expect(engine.ComplexitySimple.EnforcesTodoGate()).To(BeFalse())
				Expect(engine.ComplexityModerate.EnforcesTodoGate()).To(BeFalse())
				Expect(engine.ComplexityComplex.EnforcesTodoGate()).To(BeTrue())
				Expect(engine.ComplexityUnknown.EnforcesTodoGate()).To(BeFalse())
			})
		})
	})
})
