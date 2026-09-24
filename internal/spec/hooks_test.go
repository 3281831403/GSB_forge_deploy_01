package spec

import (
	"bytes"
	"testing"
)

var (
	fixCustomerAfterCreate  = fixID(KindHook, "51")
	fixCustomerBeforeUpdate = fixID(KindHook, "52")
)

// withHooks attaches hooks to the first resource of a valid spec.
func withHooks(hooks ...*Hook) *ProjectSpec {
	s := validSpec()
	s.Resources[0].Hooks = hooks
	return s
}

func TestValidHooksAccepted(t *testing.T) {
	s := withHooks(
		&Hook{ID: fixCustomerAfterCreate, Event: HookAfterCreate},
		&Hook{ID: fixCustomerBeforeUpdate, Event: HookBeforeUpdate},
	)
	if d := Validate(s); d != nil {
		t.Fatalf("valid hooks rejected:\n%s", d.Error())
	}
}

func TestHookUnknownEventRejected(t *testing.T) {
	s := withHooks(&Hook{ID: fixCustomerAfterCreate, Event: "on_save"})
	d := Validate(s)
	if !d.Has(CodeInvalidHook) {
		t.Errorf("unknown hook event must report %s; got %v", CodeInvalidHook, d.Codes())
	}
}

func TestHookDuplicateEventRejected(t *testing.T) {
	s := withHooks(
		&Hook{ID: fixCustomerAfterCreate, Event: HookAfterCreate},
		&Hook{ID: fixCustomerBeforeUpdate, Event: HookAfterCreate},
	)
	d := Validate(s)
	if !d.Has(CodeInvalidHook) {
		t.Errorf("duplicate hook event must report %s; got %v", CodeInvalidHook, d.Codes())
	}
}

func TestHookMalformedIDRejected(t *testing.T) {
	s := withHooks(&Hook{ID: "not-a-hook-id", Event: HookAfterCreate})
	d := Validate(s)
	if !d.Has(CodeMalformedID) {
		t.Errorf("malformed hook id must report %s; got %v", CodeMalformedID, d.Codes())
	}
}

func TestHookMissingIDRejected(t *testing.T) {
	s := withHooks(&Hook{Event: HookAfterCreate})
	d := Validate(s)
	if !d.Has(CodeMissingID) {
		t.Errorf("missing hook id must report %s; got %v", CodeMissingID, d.Codes())
	}
}

// TestDuplicateHookIDRejected exercises the global ID uniqueness rule: two hooks
// sharing one stable ID is a duplicate id. (A field's fld_ id reused as a hook
// fails the kind check first, so identity reuse across kinds surfaces as a
// malformed id, not a duplicate — the duplicate rule bites within a kind.)
func TestDuplicateHookIDRejected(t *testing.T) {
	s := withHooks(
		&Hook{ID: fixCustomerAfterCreate, Event: HookAfterCreate},
		&Hook{ID: fixCustomerAfterCreate, Event: HookBeforeUpdate},
	)
	d := Validate(s)
	if !d.Has(CodeDuplicateID) {
		t.Errorf("two hooks sharing an id must report %s; got %v", CodeDuplicateID, d.Codes())
	}
}

func TestNilHookEntryRejected(t *testing.T) {
	s := withHooks(nil)
	d := Validate(s)
	if !d.Has(CodeInvalidHook) {
		t.Errorf("nil hook entry must report %s; got %v", CodeInvalidHook, d.Codes())
	}
}

// TestHooksRoundTrip confirms hooks survive canonical marshal/unmarshal in
// authored order, and that a hook-free spec omits the field entirely.
func TestHooksRoundTrip(t *testing.T) {
	s := withHooks(
		&Hook{ID: fixCustomerAfterCreate, Event: HookAfterCreate},
		&Hook{ID: fixCustomerBeforeUpdate, Event: HookBeforeUpdate},
	)
	data, err := Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	back, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	got := back.Resources[0].Hooks
	if len(got) != 2 || got[0].Event != HookAfterCreate || got[1].Event != HookBeforeUpdate {
		t.Fatalf("hooks did not round-trip in order: %+v", got)
	}
	if got[0].ID != fixCustomerAfterCreate {
		t.Errorf("hook id lost in round-trip: got %q", got[0].ID)
	}
}

func TestHookFreeSpecOmitsHooks(t *testing.T) {
	data, err := Marshal(validSpec())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if bytes.Contains(data, []byte(`"hooks"`)) {
		t.Error(`a hook-free spec must omit "hooks" from canonical JSON`)
	}
}

func TestHookEventsStableOrder(t *testing.T) {
	got := HookEvents()
	want := []HookEvent{
		HookBeforeCreate, HookAfterCreate,
		HookBeforeUpdate, HookAfterUpdate,
		HookBeforeDelete, HookAfterDelete,
	}
	if len(got) != len(want) {
		t.Fatalf("HookEvents count: got %d want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] || !got[i].Valid() {
			t.Errorf("HookEvents[%d]: got %q", i, got[i])
		}
	}
	if HookEvent("nope").Valid() {
		t.Error("unknown event reported valid")
	}
}
