package projectspec_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gombit-dev/gombit-forge/controlplane/internal/project"
	"github.com/gombit-dev/gombit-forge/controlplane/internal/projectspec"
	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// fakeRevisions serves canned head/revision lookups.
type fakeRevisions struct {
	head    project.Revision
	headOK  bool
	headErr error

	rev    project.Revision
	revOK  bool
	revErr error
}

func (f fakeRevisions) Head(context.Context, uint) (project.Revision, bool, error) {
	return f.head, f.headOK, f.headErr
}
func (f fakeRevisions) Revision(context.Context, uint) (project.Revision, bool, error) {
	return f.rev, f.revOK, f.revErr
}

// sampleRevision builds a revision holding the canonical bytes and hash of a
// minimal valid spec — exactly what the store pins.
func sampleRevision(t *testing.T, id uint) (project.Revision, string) {
	t.Helper()
	mk := func(k spec.Kind) spec.ID { return spec.MustNewID(k) }
	sp := &spec.ProjectSpec{
		SpecVersion: spec.SpecVersion,
		Project:     spec.Project{ID: mk(spec.KindProject), Name: "Acme", Slug: "acme"},
		Database:    spec.Database{Driver: spec.DriverPostgres},
		Auth:        spec.Auth{Mode: spec.AuthCookie},
		Resources: []*spec.Resource{{
			ID: mk(spec.KindResource), Label: "Customer", CodeName: "Customer", StorageName: "customers",
			Fields: []*spec.Field{{ID: mk(spec.KindField), Label: "Email", Type: spec.TypeString, CodeName: "Email", StorageName: "email"}},
		}},
	}
	canonical, err := spec.Marshal(sp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	hash, err := spec.Hash(sp)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return project.Revision{ID: id, SpecJSON: string(canonical), SpecHash: hash}, hash
}

func TestHeadSpecDecodesRevision(t *testing.T) {
	rev, hash := sampleRevision(t, 5)
	src := projectspec.NewSource(fakeRevisions{head: rev, headOK: true})

	sp, ref, err := src.HeadSpec(context.Background(), 1)
	if err != nil {
		t.Fatalf("head spec: %v", err)
	}
	if sp == nil || sp.Project.Slug != "acme" {
		t.Fatalf("decoded spec = %+v", sp)
	}
	if ref != hash {
		t.Errorf("provenance ref = %q, want the revision hash %q", ref, hash)
	}
}

func TestHeadSpecNoRevisionIsNilNotError(t *testing.T) {
	src := projectspec.NewSource(fakeRevisions{headOK: false})
	sp, ref, err := src.HeadSpec(context.Background(), 1)
	if err != nil || sp != nil || ref != "" {
		t.Errorf("no head: sp=%v ref=%q err=%v, want nil,\"\",nil", sp, ref, err)
	}
}

func TestHeadSpecPropagatesError(t *testing.T) {
	boom := errors.New("db down")
	src := projectspec.NewSource(fakeRevisions{headErr: boom})
	if _, _, err := src.HeadSpec(context.Background(), 1); !errors.Is(err, boom) {
		t.Errorf("head error must propagate; got %v", err)
	}
}

func TestRevisionSpecDecodesRevision(t *testing.T) {
	rev, hash := sampleRevision(t, 9)
	src := projectspec.NewSource(fakeRevisions{rev: rev, revOK: true})

	sp, ref, err := src.RevisionSpec(context.Background(), 9)
	if err != nil {
		t.Fatalf("revision spec: %v", err)
	}
	if sp == nil || ref != hash {
		t.Errorf("decoded = %+v ref=%q", sp, ref)
	}
}

func TestRevisionSpecMissingIsError(t *testing.T) {
	src := projectspec.NewSource(fakeRevisions{revOK: false})
	if _, _, err := src.RevisionSpec(context.Background(), 9); err == nil {
		t.Error("a missing revision must error, not return an empty spec")
	}
}

func TestDecodeCorruptBytesErrors(t *testing.T) {
	src := projectspec.NewSource(fakeRevisions{rev: project.Revision{ID: 1, SpecJSON: "{not valid"}, revOK: true})
	if _, _, err := src.RevisionSpec(context.Background(), 1); err == nil {
		t.Error("undecodable canonical bytes must error")
	}
}
