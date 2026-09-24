package ghexport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/githubexport"
	"github.com/gombit-dev/gombit-forge/internal/compiler"
)

// TestPublisherPreservesExecutableBit proves the production adapter maps a
// SourceFile's exec bit through to the GitHub tree (mode 100755), closing the
// ZIP-vs-GitHub mode gap end to end.
func TestPublisherPreservesExecutableBit(t *testing.T) {
	modes := map[string]string{}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/octo/app/git/blobs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "blob"})
	})
	mux.HandleFunc("/repos/octo/app/git/trees", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tree []map[string]any
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		for _, e := range body.Tree {
			modes[e["path"].(string)] = e["mode"].(string)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "tree"})
	})
	mux.HandleFunc("/repos/octo/app/git/commits", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"sha": "commit"})
	})
	mux.HandleFunc("/repos/octo/app/git/refs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	pub := NewPublisher(githubexport.Config{APIBaseURL: srv.URL}, srv.Client())
	err := pub.PushFiles(context.Background(), "tok", "octo", "app", "main", []compiler.SourceFile{
		{Path: "go.mod", Content: []byte("module x")},
		{Path: "run.sh", Content: []byte("#!/bin/sh"), Executable: true},
	}, "msg")
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if modes["go.mod"] != "100644" {
		t.Errorf("go.mod mode = %q, want 100644", modes["go.mod"])
	}
	if modes["run.sh"] != "100755" {
		t.Errorf("run.sh mode = %q, want 100755 (exec bit must survive the GitHub push)", modes["run.sh"])
	}
}
