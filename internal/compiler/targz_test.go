package compiler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"testing"
)

func TestWriteTarGzDeterministic(t *testing.T) {
	files := []SourceFile{
		{Path: "go.mod", Content: []byte("module example.com/app\n")},
		{Path: "internal/forge_generated/widget/model.go", Content: []byte("package widget\n")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\n"), Executable: true},
	}

	var a, b bytes.Buffer
	if err := WriteTarGz(files, &a); err != nil {
		t.Fatal(err)
	}
	if err := WriteTarGz(files, &b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("WriteTarGz is not byte-deterministic for the same input")
	}
}

func TestWriteTarGzRoundTrip(t *testing.T) {
	files := []SourceFile{
		{Path: "go.mod", Content: []byte("module example.com/app\n")},
		{Path: "scripts/run.sh", Content: []byte("#!/bin/sh\necho hi\n"), Executable: true},
	}
	var buf bytes.Buffer
	if err := WriteTarGz(files, &buf); err != nil {
		t.Fatal(err)
	}

	gz, err := gzip.NewReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)

	type got struct {
		mode    int64
		content string
	}
	seen := map[string]got{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		seen[hdr.Name] = got{mode: hdr.Mode, content: string(b)}
	}

	if len(seen) != 2 {
		t.Fatalf("archived %d entries, want 2", len(seen))
	}
	if g := seen["go.mod"]; g.content != "module example.com/app\n" || g.mode != 0o644 {
		t.Fatalf("go.mod round-trip = %+v", g)
	}
	// The executable bit survives so Cloud can run a scaffold script.
	if g := seen["scripts/run.sh"]; g.mode != 0o755 {
		t.Fatalf("run.sh mode = %o, want 0755", g.mode)
	}
}
