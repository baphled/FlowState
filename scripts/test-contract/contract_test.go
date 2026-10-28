package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Test contract", func() {
	DescribeTable("checks a parsed source against the contract", func(body string, failures int) {
		source := "package fixture\nimport (. \"github.com/onsi/ginkgo/v2\"; . \"github.com/onsi/gomega\"; \"testing\"; \"time\"; \"os\")\n" + body
		Expect(checkSource(sourceFile{Name: "fixture_test.go", Source: source})).To(HaveLen(failures))
	},
		Entry("constructor is not assertion", `var _=It("works",func(){ Expect(value) })`, 1),
		Entry("async must be bounded", `var _=It("works",func(){ Eventually(read).Should(Equal(1)) })`, 1),
		Entry("indirect body", `var _=It("works",body)`, 1),
		Entry("test main cannot mask failure", `func TestMain(m *testing.M){os.Exit(0)}`, 1),
		Entry("test main propagates status", `func TestMain(m *testing.M){os.Exit(m.Run())}`, 0),
		Entry("runtime skip through testing adapter", `var _=It("works",func(){GinkgoT().Skip("later");Expect(v).To(Equal(1))})`, 1),
		Entry("one expectation", `var _=It("works",func(){ Expect(value).To(Equal(1)) })`, 0),
		Entry("two expectations", `var _=It("works",func(){ Expect(a).To(Equal(1));Expect(b).To(Equal(2)) })`, 1),
		Entry("no expectation", `var _=It("works",func(){ work() })`, 1),
		Entry("explicit setup check", `var _=It("works",func(){ v,err:=open();Expect(err).NotTo(HaveOccurred());Expect(v).To(Equal(1)) })`, 0),
		Entry("setup alone is not behaviour", `var _=It("works",func(){ _,err:=open();Expect(err).NotTo(HaveOccurred()) })`, 1),
		Entry("negative error behaviour", `var _=It("fails",func(){ Expect(err).To(MatchError("bad")) })`, 0),
		Entry("stdlib test", `func TestBehaviour(t *testing.T){}`, 1),
		Entry("suite bootstrap", `func TestSuite(t *testing.T){RegisterFailHandler(Fail);RunSpecs(t,"Suite")}`, 0),
		Entry("wrong bootstrap arguments", `func TestSuite(t *testing.T){RegisterFailHandler(other);RunSpecs(other,"Suite")}`, 1),
		Entry("fake suite bootstrap", `func TestSuite(t *testing.T){RunSpecs(t,"Suite")}`, 1),
		Entry("focused spec", `var _=FIt("works",func(){})`, 1),
		Entry("pending decorator", `var _=Describe("suite",Pending,func(){})`, 1),
		Entry("runtime skip", `var _=It("works",func(){Skip("later");Expect(v).To(Equal(1))})`, 1),
		Entry("sleep", `var _=It("works",func(){time.Sleep(time.Second);Expect(v).To(Equal(1))})`, 1),
		Entry("hidden helper assertion", `func verify(){Expect(v).To(Equal(1))}`, 1),
		Entry("serial decorator", `var _=Describe("suite",Serial,func(){})`, 1),
		Entry("empty helper allowed", `func fixture()int{return 1}`, 0),
		Entry("bounded async assertion", `var _=It("works",func(){Eventually(read).WithTimeout(time.Second).WithPolling(time.Millisecond).Should(Equal(1))})`, 0),
	)
	It("resolves aliased framework imports", func() {
		Expect(checkSource(sourceFile{Name: "alias_test.go", Source: `package fixture
import (g "github.com/onsi/ginkgo/v2"; m "github.com/onsi/gomega")
var _=g.It("works",func(){m.Expect(1).To(m.Equal(1));m.Expect(2).To(m.Equal(2))})`})).To(HaveLen(1))
	})
	It("rejects malformed source", func() {
		Expect(checkSource(sourceFile{Name: "broken_test.go", Source: "package"})).To(HaveLen(1))
	})
})
