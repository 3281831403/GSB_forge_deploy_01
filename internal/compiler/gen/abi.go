package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/gombit-dev/gombit-forge/internal/compiler/graph"
)

// ABI is the normalized backend extension surface of a whole project — the
// contract user extension code binds to (ADR-001 §39). It is built only from
// stable identity, frozen code symbols and Go types, never from labels or
// storage names, so a relabel or a storage rename with a frozen source symbol
// yields an identical ABI, while a code-symbol rename or an extension-visible
// type change yields a different one.
//
// It is order-independent: the extension surface is a set of contracts, not a
// sequence, so page/field/navigation reordering (all ABI-neutral, §38) must not
// change it. Slices are sorted by stable ID and every map is JSON-marshaled with
// sorted keys, so Sum is a deterministic digest.
type ABI struct {
	Resources []ResourceABI `json:"resources"`
}

// ResourceABI is one resource's extension surface.
type ResourceABI struct {
	// ID is the resource's stable identity; Type is its frozen code symbol,
	// which names every generated extension type (CustomerView, CustomerHooks,
	// …), so a source rename changes Type and thereby the ABI.
	ID   string `json:"id"`
	Type string `json:"type"`
	// Fields is keyed by field stable ID so a diff can tell a rename (same id,
	// new symbol) from a delete-plus-add. Its value is the extension-visible
	// symbol and type each field contributes to the view accessor, the
	// draft/change mutators and the field reference.
	Fields map[string]FieldABI `json:"fields"`
	// Hooks maps an enabled lifecycle event to its contract method signature.
	Hooks map[string]string `json:"hooks,omitempty"`
}

// FieldABI is the extension-visible part of one field: its frozen accessor
// symbol and Go type. Both are independent of the field's label and storage
// name (ADR-001 D2), so neither moves under a relabel or storage rename.
type FieldABI struct {
	Symbol string `json:"symbol"`
	Type   string `json:"type"`
}

// ExtensionABI computes the normalized extension surface of a resolved graph.
func ExtensionABI(g *graph.Graph) (ABI, error) {
	if g == nil {
		return ABI{}, fmt.Errorf("gen: nil graph")
	}

	abi := ABI{Resources: make([]ResourceABI, 0, len(g.Resources))}
	for _, resource := range g.Resources {
		r := ResourceABI{
			ID:     string(resource.Spec.ID),
			Type:   resource.CodeName(),
			Fields: make(map[string]FieldABI, len(resource.Fields)),
		}
		for _, field := range resource.Fields {
			mapping, err := resolveType(field)
			if err != nil {
				return ABI{}, err
			}
			r.Fields[string(field.Spec.ID)] = FieldABI{
				Symbol: goFieldName(field),
				Type:   mapping.goType,
			}
		}
		if len(resource.Spec.Hooks) > 0 {
			r.Hooks = make(map[string]string, len(resource.Spec.Hooks))
			for _, hook := range resource.Spec.Hooks {
				method, ok := hookMethodFor(hook.Event, resource.CodeName())
				if !ok {
					return ABI{}, fmt.Errorf("gen: resource %s hook %s has unsupported event %q",
						resource.Spec.ID, hook.ID, hook.Event)
				}
				r.Hooks[string(hook.Event)] = method.params
			}
		}
		abi.Resources = append(abi.Resources, r)
	}
	sort.Slice(abi.Resources, func(i, j int) bool { return abi.Resources[i].ID < abi.Resources[j].ID })
	return abi, nil
}

// Sum is the stable digest of the extension surface — the extension_abi_sha256
// of ADR-001 §39. Two specs whose extension ABIs are equal have equal sums; a
// relabel or storage rename leaves it unchanged, a source-symbol or type change
// changes it.
func (a ABI) Sum() (string, error) {
	// json.Marshal sorts map keys, and Resources is already sorted by ID, so the
	// encoding is canonical.
	encoded, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("gen: encoding extension ABI: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// Class is the compatibility classification of a candidate spec transition
// (ADR-001 §37-41).
type Class int

// ClassNeutral is an ABI-neutral transition (§38): the extension surface is
// unchanged, so the candidate may commit without user extension code compiling.
// Presentation edits — relabel, storage rename with a frozen symbol, page/nav
// reordering — land here.
//
// ClassAdditive is a backward-compatible additive transition (§40): the surface
// grew (a new resource, a new field's accessors, the first hook contract on a
// resource) but nothing existing extension code binds to was removed or changed,
// so compatibility holds without compiling user code.
//
// ClassBreaking is an ABI-breaking transition (§41): something existing
// extension code may bind to was removed, retyped or renamed. The candidate
// cannot become current until user code is proven compatible.
const (
	ClassNeutral Class = iota
	ClassAdditive
	ClassBreaking
)

func (c Class) String() string {
	switch c {
	case ClassNeutral:
		return "neutral"
	case ClassAdditive:
		return "additive"
	case ClassBreaking:
		return "breaking"
	default:
		return fmt.Sprintf("Class(%d)", int(c))
	}
}

// Transition is the result of classifying a candidate transition: its class, the
// candidate's fingerprint, and the concrete reasons behind an additive or
// breaking verdict (empty for neutral).
type Transition struct {
	Class       Class
	Fingerprint string
	Reasons     []string
}

// ClassifyTransition classifies a move from the current extension ABI to a
// candidate one (ADR-001 §37-41).
//
// It first compares fingerprints: equal means ABI-neutral, the fast path §38
// relies on so a presentation edit never waits on user code. Otherwise it walks
// a structured diff and reports breaking if anything existing extension code
// could bind to was removed, retyped or source-renamed (§41) — including a hook
// added to a resource that already has hooks, which adds a required method to an
// interface the user already implements (§40's caveat) — and additive when the
// only changes are new surface (§40).
func ClassifyTransition(current, candidate ABI) (Transition, error) {
	curSum, err := current.Sum()
	if err != nil {
		return Transition{}, err
	}
	candSum, err := candidate.Sum()
	if err != nil {
		return Transition{}, err
	}
	if curSum == candSum {
		return Transition{Class: ClassNeutral, Fingerprint: candSum}, nil
	}

	curByID := indexResources(current)
	candByID := indexResources(candidate)

	var breaking, additive []string
	for id, cur := range curByID {
		cand, ok := candByID[id]
		if !ok {
			breaking = append(breaking, fmt.Sprintf("resource %s (%s) removed", id, cur.Type))
			continue
		}
		if cur.Type != cand.Type {
			breaking = append(breaking, fmt.Sprintf("resource %s source symbol renamed %s -> %s", id, cur.Type, cand.Type))
		}
		diffResourceFields(id, cur, cand, &breaking, &additive)
		diffResourceHooks(id, cur, cand, &breaking, &additive)
	}
	for id, cand := range candByID {
		if _, ok := curByID[id]; !ok {
			additive = append(additive, fmt.Sprintf("resource %s (%s) added", id, cand.Type))
		}
	}

	if len(breaking) > 0 {
		sort.Strings(breaking)
		return Transition{Class: ClassBreaking, Fingerprint: candSum, Reasons: breaking}, nil
	}
	sort.Strings(additive)
	return Transition{Class: ClassAdditive, Fingerprint: candSum, Reasons: additive}, nil
}

func diffResourceFields(id string, cur, cand ResourceABI, breaking, additive *[]string) {
	for fieldID, curField := range cur.Fields {
		candField, ok := cand.Fields[fieldID]
		if !ok {
			*breaking = append(*breaking, fmt.Sprintf("resource %s: accessor %s removed", id, curField.Symbol))
			continue
		}
		if curField.Symbol != candField.Symbol {
			*breaking = append(*breaking, fmt.Sprintf("resource %s: accessor renamed %s -> %s", id, curField.Symbol, candField.Symbol))
		}
		if curField.Type != candField.Type {
			*breaking = append(*breaking, fmt.Sprintf("resource %s: accessor %s type changed %s -> %s", id, candField.Symbol, curField.Type, candField.Type))
		}
	}
	for fieldID, candField := range cand.Fields {
		if _, ok := cur.Fields[fieldID]; !ok {
			*additive = append(*additive, fmt.Sprintf("resource %s: accessor %s added", id, candField.Symbol))
		}
	}
}

func diffResourceHooks(id string, cur, cand ResourceABI, breaking, additive *[]string) {
	for event, curSig := range cur.Hooks {
		candSig, ok := cand.Hooks[event]
		if !ok {
			*breaking = append(*breaking, fmt.Sprintf("resource %s: hook %s removed", id, event))
			continue
		}
		if curSig != candSig {
			*breaking = append(*breaking, fmt.Sprintf("resource %s: hook %s signature changed", id, event))
		}
	}
	for event := range cand.Hooks {
		if _, ok := cur.Hooks[event]; ok {
			continue
		}
		// A new event. Adding it to a resource that already exposes a hook
		// contract adds a required method to an interface the user's Hooks type
		// already implements (§40's caveat), so it is breaking; the first hook on
		// a resource introduces a brand-new contract with no prior implementation,
		// so it is additive (§51 scenario C).
		if len(cur.Hooks) > 0 {
			*breaking = append(*breaking, fmt.Sprintf("resource %s: hook %s added to an already-hooked contract", id, event))
		} else {
			*additive = append(*additive, fmt.Sprintf("resource %s: hook contract introduced (%s)", id, event))
		}
	}
}

func indexResources(a ABI) map[string]ResourceABI {
	byID := make(map[string]ResourceABI, len(a.Resources))
	for _, r := range a.Resources {
		byID[r.ID] = r
	}
	return byID
}
