package compiler

import (
	"fmt"

	"github.com/gombit-dev/gombit-forge/internal/spec"
)

// DeletionBlocker is one entity in the candidate spec that still references a
// resource the candidate deletes (ADR-001 §45). While any blocker remains, the
// deletion is a hard dependency violation and must be rejected before generation
// (§54, §91) — resolving it means deleting the referencing entity in the same
// atomic candidate (§44).
type DeletionBlocker struct {
	// Kind is the sort of reference holding the resource alive:
	// "relationship", "page" or "dashboard_card".
	Kind string
	// Entity is the stable ID of the referencing entity — the belongs_to field,
	// the page, or the page that owns the dashboard card.
	Entity spec.ID
	// Owner is the resource that owns the referencing entity when the reference
	// lives on another resource (a relationship field). It is empty for pages and
	// dashboard cards, which hang off the project, not a resource.
	Owner spec.ID
	// Message states the dependency in deletion-centric terms.
	Message string
}

// DeletedResource is one resource present in the current spec but absent from
// the candidate, together with everything Forge must weigh before the deletion
// can proceed (ADR-001 §54): the references that still bind it (Blockers), and
// whether it carried backend extension surface that will need archival.
type DeletedResource struct {
	ID       spec.ID
	CodeName string
	Label    string
	// Blockers are the hard dependencies still pointing at this resource in the
	// candidate. A non-empty list means the deletion must be rejected.
	Blockers []DeletionBlocker
	// HadExtension reports whether the resource carried lifecycle hooks (an
	// active custom extension) in the current spec. It is not a blocker: when the
	// deletion proceeds, that user source is archived outside the build graph
	// (§46, §54), which is issue #30 — but the deletion UX must surface it, so it
	// is identified here.
	HadExtension bool
}

// Blocked reports whether a hard dependency prevents this deletion.
func (d DeletedResource) Blocked() bool { return len(d.Blockers) > 0 }

// AnalyzeDeletions identifies every resource the candidate removes and analyzes
// the dependencies on it (ADR-001 §45, §54, §91).
//
// It answers the deletion-centric question the mutation pipeline needs before it
// generates anything: for each resource being deleted, what in the candidate
// still references it, and does it have extension source to archive? This is
// distinct from spec validation. spec.Validate reports a stale reference from the
// referencing entity's side ("this belongs_to target does not exist"); this
// reports it from the deletion's side ("this resource cannot be deleted, these
// entities still bind it") and adds the extension/archival consequence that is
// not a spec-validity question at all. It deliberately requires neither a valid
// nor a compilable spec, so the deletion gate runs before generation, not after.
//
// Results are in the current spec's authored resource order, and each resource's
// blockers are in the candidate's authored order, so the analysis is
// deterministic.
//
// A resource referenced only by other resources that are themselves deleted in
// the same candidate has no surviving blocker — the atomic transaction of §44 is
// exactly how a "delete relationship + delete resource" batch clears its own
// dependencies.
func AnalyzeDeletions(current, candidate *spec.ProjectSpec) []DeletedResource {
	if current == nil {
		return nil
	}

	surviving := map[spec.ID]bool{}
	if candidate != nil {
		for _, r := range candidate.Resources {
			surviving[r.ID] = true
		}
	}

	var deletions []DeletedResource
	for _, r := range current.Resources {
		if surviving[r.ID] {
			continue
		}
		deletions = append(deletions, DeletedResource{
			ID:           r.ID,
			CodeName:     r.CodeName,
			Label:        r.Label,
			HadExtension: len(r.Hooks) > 0,
			Blockers:     blockersFor(candidate, r),
		})
	}
	return deletions
}

// blockersFor scans the candidate for references to the deleted resource. Only
// entities that survive into the candidate can block: a reference owned by a
// resource that is itself being deleted is already gone.
func blockersFor(candidate *spec.ProjectSpec, deleted *spec.Resource) []DeletionBlocker {
	if candidate == nil {
		return nil
	}
	var blockers []DeletionBlocker

	// Relationships: a belongs_to field on a surviving resource still targeting
	// the deleted one.
	for _, r := range candidate.Resources {
		for _, f := range r.Fields {
			if f.Type == spec.TypeBelongsTo && f.Target == deleted.ID {
				blockers = append(blockers, DeletionBlocker{
					Kind:   "relationship",
					Entity: f.ID,
					Owner:  r.ID,
					Message: fmt.Sprintf("relationship %s.%s (belongs_to) still references %s",
						r.CodeName, f.CodeName, deleted.CodeName),
				})
			}
		}
	}

	// Pages bound to the deleted resource, and dashboard cards pointing at it.
	for _, p := range candidate.Pages {
		if p.Resource == deleted.ID {
			blockers = append(blockers, DeletionBlocker{
				Kind:    "page",
				Entity:  p.ID,
				Message: fmt.Sprintf("page %q still references %s", p.Slug, deleted.CodeName),
			})
		}
		if p.Dashboard == nil {
			continue
		}
		for _, card := range append(append([]spec.DashboardCard(nil), p.Dashboard.CountCards...), p.Dashboard.RecentLists...) {
			if card.Resource == deleted.ID {
				blockers = append(blockers, DeletionBlocker{
					Kind:    "dashboard_card",
					Entity:  p.ID,
					Message: fmt.Sprintf("dashboard card %q on page %q still references %s", card.Label, p.Slug, deleted.CodeName),
				})
			}
		}
	}

	return blockers
}

// BlockedDeletions returns only the deletions with an unresolved hard dependency
// (ADR-001 §45). An empty result means every deletion in the candidate is
// dependency-free and may proceed to generation.
func BlockedDeletions(deletions []DeletedResource) []DeletedResource {
	var blocked []DeletedResource
	for _, d := range deletions {
		if d.Blocked() {
			blocked = append(blocked, d)
		}
	}
	return blocked
}
