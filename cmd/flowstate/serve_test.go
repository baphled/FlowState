package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
)

// runOutcome pairs one run's exit code with its stdout.
type runOutcome struct {
	code int
	out  string
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func serveStart() (string, func() int) {
	db := filepath.Join(GinkgoT().TempDir(), "serve.db")
	st, err := app.OpenStore(db)
	must(err)
	_, err = app.CreateSession(context.Background(), st, "z.ai", "glm-5.3", app.DefaultHarness, "3f7a", "wiring")
	must(err)
	must(st.Close())
	outR, outW, err := os.Pipe()
	must(err)
	exit := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		exit <- dispatchContexted(ctx, []string{"serve", "--addr", "127.0.0.1:0", "--db", db}, outW, outW)
	}()
	line, err := bufio.NewReader(outR).ReadString('\n')
	must(err)
	listen := map[string]string{}
	must(json.Unmarshal([]byte(line), &listen))
	return "http://" + listen["listen"], func() int {
		cancel()
		_ = outW.Close()
		code := <-exit
		return code
	}
}

var _ = Describe("Serve command wiring", Label("e2e"), func() {
	It("serves the seeded session list over a real store", func() {
		base, stop := serveStart()
		resp, err := http.Get(base + "/sessions")
		must(err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		must(err)
		rows := []struct {
			ID string "json:\"id\""
		}{}
		must(json.Unmarshal(body, &rows))
		stop()
		Expect(rows).To(HaveLen(1))
	})
	It("exits 4 config_invalid without serving when the database cannot open", func() {
		r := runArgv([]string{"serve", "--db", GinkgoT().TempDir()})
		Expect(runOutcome{r.exit, r.stdout}).To(Equal(runOutcome{exitConfigInvalid, ""}))
	})
})
