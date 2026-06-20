// builtins_printers_test.go tests the pure lpstat parsing/formatting half of the
// printer status builtins (renderPrinterList) against synthetic lpstat output —
// no live lpstat call. The disabled-printer path is the most important case: it
// must steer the user toward the Settings hand-off.
package engine

import (
	"strings"
	"testing"
)

func TestRenderPrinterList_IdleWithDefault(t *testing.T) {
	out := renderPrinterList("printer HP_LaserJet is idle.  enabled since Mon Jun 15 18:45:14 2026\nsystem default destination: HP_LaserJet\n")
	if !strings.Contains(out, "1 printer(s) configured:") {
		t.Errorf("expected a count of 1 printer, got: %s", out)
	}
	if !strings.Contains(out, "HP_LaserJet [default]") {
		t.Errorf("expected the default marker on HP_LaserJet, got: %s", out)
	}
	if strings.Contains(out, "disabled") {
		t.Errorf("an idle printer should not trigger the disabled note: %s", out)
	}
}

func TestRenderPrinterList_DisabledFlagsHandoff(t *testing.T) {
	out := renderPrinterList("printer Office_Laser disabled since Mon Jun 15 09:00:00 2026 -\nsystem default destination: Office_Laser\n")
	if !strings.Contains(out, "open_settings") || !strings.Contains(out, "administrator rights") {
		t.Errorf("a disabled printer should explain the admin hand-off via open_settings: %s", out)
	}
}

func TestRenderPrinterList_None(t *testing.T) {
	if out := renderPrinterList(""); !strings.Contains(out, "No printers are configured") {
		t.Errorf("expected a no-printers message, got: %s", out)
	}
}
