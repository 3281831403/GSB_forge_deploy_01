package spec

// Identity continuity (ADR-001 §57-62, D14).
//
// Forge-managed mutations preserve stable-ID lineage by construction, so
// migration and rename inference can trust them. A local-first spec, though, can
// be hand-edited, script-changed, Git-merged or restored from odd history, so
// the on-disk spec cannot be assumed to descend safely from the last accepted
// revision (§57). CheckLineage is the semantic lineage validation reopening runs
// (§61): it compares the trusted prior spec with the current one and reports
// whether identity is continuous.
//
// The scope is schema identity — resources and their fields — because that is
// what migration and destructive rename/drop inference depend on (§57, §62).
// Pages, navigation and hooks carry stable IDs too, but rewriting one has no
// migration consequence, so they are deliberately out of this check.
//
// The check is ID-continuity only. It observes whether a stable ID still exists,
// not where it lives: a field whose fld_ ID is hand-moved from one resource to
// another reads as continuous, because Forge-managed mutations preserve lineage
// by construction (§57) and the ID is intact. Whether such a reparent needs its
// own migration signal (the column effectively changes tables) is a
// migration-inference question, not this gate's.

// IdentityRef is one schema entity's stable identity plus enough presentation to
// describe it in a resolution prompt (§59): the storage_name is what a
// discontinuity is usually seen through (email → contact_email).
type IdentityRef struct {
	ID      ID     `json:"id"`
	Kind    Kind   `json:"kind"`
	Label   string `json:"label,omitempty"`
	Storage string `json:"storage_name,omitempty"`
}

// Lineage is the identity-continuity analysis between a trusted prior spec (the
// last accepted revision) and the current on-disk spec (ADR-001 §58, §61).
//
// It reports only what changed by stable ID: entities Removed since the prior
// spec and entities Added. It never reports a rename. A Forge rename keeps the
// same stable ID and changes only label/storage, so it appears in neither list —
// which is the structural guarantee that a rename can never be *inferred* across
// an ID change (§58, §94): the analysis has no field a consumer could read as
// "fld_A became fld_B".
type Lineage struct {
	Removed []IdentityRef
	Added   []IdentityRef
}

// Discontinuous reports an identity discontinuity that Forge must fail closed on
// (ADR-001 §58-59, §94): a prior identity vanished while a new one appeared, so a
// rewrite (fld_A → fld_B) is indistinguishable from a delete-plus-add. When this
// is true, Forge must refuse automatic rename or destructive inference and ask
// for explicit resolution (DiscontinuityResolutions).
//
// A pure addition (nothing removed) or a pure deletion (nothing added) is not a
// discontinuity: there is no ambiguous rename target, so ID-based diffing handles
// it without guessing. Only the coexistence of a removal and an addition is the
// §58 ambiguity.
func (l Lineage) Discontinuous() bool {
	return len(l.Removed) > 0 && len(l.Added) > 0
}

// CheckLineage compares the trusted prior spec with the current spec and returns
// the identity delta (ADR-001 §58, §61). Either spec may be nil: a nil prior
// makes everything an addition (a first revision), a nil current makes everything
// a removal. The delta is deterministic — prior authored order for removals,
// current authored order for additions.
func CheckLineage(prior, current *ProjectSpec) Lineage {
	priorRefs := schemaIdentities(prior)
	currentRefs := schemaIdentities(current)
	priorSet := idSet(priorRefs)
	currentSet := idSet(currentRefs)

	var l Lineage
	for _, ref := range priorRefs {
		if !currentSet[ref.ID] {
			l.Removed = append(l.Removed, ref)
		}
	}
	for _, ref := range currentRefs {
		if !priorSet[ref.ID] {
			l.Added = append(l.Added, ref)
		}
	}
	return l
}

// schemaIdentities lists a spec's migration-bearing identities — each resource,
// then each of its fields — in authored order. Nil entries (a spec that decoded
// with holes) are skipped so lineage analysis works on a not-yet-valid spec, the
// same way the deletion gate does.
func schemaIdentities(s *ProjectSpec) []IdentityRef {
	if s == nil {
		return nil
	}
	var refs []IdentityRef
	for _, r := range s.Resources {
		if r == nil {
			continue
		}
		refs = append(refs, IdentityRef{ID: r.ID, Kind: KindResource, Label: r.Label, Storage: r.StorageName})
		for _, f := range r.Fields {
			if f == nil {
				continue
			}
			refs = append(refs, IdentityRef{ID: f.ID, Kind: KindField, Label: f.Label, Storage: f.StorageName})
		}
	}
	return refs
}

func idSet(refs []IdentityRef) map[ID]bool {
	set := make(map[ID]bool, len(refs))
	for _, ref := range refs {
		set[ref.ID] = true
	}
	return set
}

// LineageResolution is one explicit way a user can resolve an identity
// discontinuity (ADR-001 §59). Forge never picks one automatically — it fails
// closed and surfaces these — so the exact UX is out of scope; this is only the
// structured set of choices.
type LineageResolution string

const (
	ResolveAsRename        LineageResolution = "treat_as_rename"
	ResolveAsDeleteAndAdd  LineageResolution = "treat_as_delete_add"
	ResolveRestoreIdentity LineageResolution = "restore_previous_identity"
	ResolveCancel          LineageResolution = "cancel"
)

// DiscontinuityResolutions returns the §59 resolution choices in their fixed
// presentation order.
func DiscontinuityResolutions() []LineageResolution {
	return []LineageResolution{
		ResolveAsRename,
		ResolveAsDeleteAndAdd,
		ResolveRestoreIdentity,
		ResolveCancel,
	}
}
