// mutate_contacts_test.go covers the pure staging logic of create_contact:
// field validation, the forward/inverse argv layout, the unique-marker inverse,
// and the structural injection guarantee. No test drives osascript or touches the
// real Contacts database — staging is a pure function of its parameters, so the
// plan is inspected directly.
package engine

import (
	"context"
	"strings"
	"testing"

	"mcp-server-mac-os/internal/registry"
)

// stageContact is a tiny helper that stages create_contact from a raw param map.
func stageContact(t *testing.T, in map[string]any) (*StagedPlan, error) {
	t.Helper()
	return stageCreateContact(context.Background(), contactsCap("create_contact"), in)
}

// contactsCap returns a minimal Capability standing in for a manifest entry in a
// direct mutator unit test (the mutator only reads params, not the capability).
func contactsCap(builder string) registry.Capability {
	return registry.Capability{Name: builder, Builder: builder}
}

// TestStageCreateContact_ForwardAndInverse verifies a valid request produces a
// forward that carries every field as osascript argv data after "--", and an
// inverse that deletes by the SAME unique marker the forward tags the card with.
func TestStageCreateContact_ForwardAndInverse(t *testing.T) {
	plan, err := stageContact(t, map[string]any{
		"first_name":   "Jane",
		"last_name":    "Doe",
		"organization": "Example Corp",
		"phone":        "+1 (555) 555-0123",
		"email":        "jane@example.com",
	})
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if plan.Inverse == nil {
		t.Fatal("create_contact must be reversible (inverse deletes the created card)")
	}

	// Forward argv: -e <script> -- first last org phone email marker
	fwd := plan.Forward.Args
	if len(fwd) != 9 || fwd[2] != "--" {
		t.Fatalf("forward argv shape unexpected: %q", fwd)
	}
	if fwd[3] != "Jane" || fwd[4] != "Doe" || fwd[5] != "Example Corp" || fwd[6] != "+1 (555) 555-0123" || fwd[7] != "jane@example.com" {
		t.Errorf("forward fields not carried verbatim after '--': %q", fwd[3:8])
	}
	marker := fwd[8]
	if !strings.HasPrefix(marker, "mcp-created-contact-") {
		t.Errorf("marker %q should be the unique tag written to the note field", marker)
	}

	// Inverse argv: -e <script> -- marker — the SAME marker, so undo removes
	// exactly the contact this operation created.
	inv := plan.Inverse.Args
	if len(inv) != 4 || inv[2] != "--" || inv[3] != marker {
		t.Errorf("inverse must delete by the forward's marker; got argv %q (marker %q)", inv, marker)
	}
	if !strings.Contains(plan.Preview, "Jane Doe") || !strings.Contains(plan.Preview, "Undo will delete") {
		t.Errorf("preview should name the contact and describe undo:\n%s", plan.Preview)
	}
}

// TestStageCreateContact_MarkerIsUnique ensures two stagings never collide on a
// marker, which is what makes the delete-by-marker inverse safe.
func TestStageCreateContact_MarkerIsUnique(t *testing.T) {
	p1, err := stageContact(t, map[string]any{"first_name": "Jane"})
	if err != nil {
		t.Fatalf("stage 1: %v", err)
	}
	p2, err := stageContact(t, map[string]any{"first_name": "Jane"})
	if err != nil {
		t.Fatalf("stage 2: %v", err)
	}
	if p1.Forward.Args[8] == p2.Forward.Args[8] {
		t.Errorf("two stagings produced the same marker %q", p1.Forward.Args[8])
	}
}

// TestStageCreateContact_Validation walks the rejection table: an all-blank card,
// a phone with letters, a malformed email, and a control character in a name are
// each refused at stage time (before any command is assembled).
func TestStageCreateContact_Validation(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"all blank", map[string]any{}, "at least one of"},
		{"blank whitespace", map[string]any{"first_name": "   "}, "at least one of"},
		{"phone with letters", map[string]any{"first_name": "Jane", "phone": "call-me"}, "valid phone number"},
		{"bad email", map[string]any{"first_name": "Jane", "email": "not-an-email"}, "valid email"},
		{"control char in name", map[string]any{"first_name": "Ja\x00ne"}, "control characters"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := stageContact(t, tc.in)
			if err == nil {
				t.Fatalf("expected rejection, got nil error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err.Error(), tc.want)
			}
		})
	}
}

// TestStageCreateContact_HostileFieldLandsAsData proves the structural guard: a
// field value that would be a flag to osascript (e.g. "-e") still lands as inert
// argv data after the "--" terminator. first_name skips the phone/email shape
// checks, so it is the field used to smuggle each hostile value.
func TestStageCreateContact_HostileFieldLandsAsData(t *testing.T) {
	for _, h := range hostileValues {
		// A leading control-char/NUL value is legitimately rejected by
		// rejectControlChars, so skip those here — this test targets the "--"
		// terminator's defense of dash-leading/metacharacter values.
		if strings.ContainsAny(h, "\x00\n") {
			continue
		}
		plan, err := stageContact(t, map[string]any{"first_name": h})
		if err != nil {
			t.Fatalf("%q: stage failed: %v", h, err)
		}
		if got := plan.Forward.Args[3]; got != h {
			t.Errorf("%q: first_name landed as %q, want the value verbatim after '--'", h, got)
		}
		if plan.Forward.Args[2] != "--" {
			t.Errorf("%q: missing '--' terminator before data: %q", h, plan.Forward.Args)
		}
	}
}
