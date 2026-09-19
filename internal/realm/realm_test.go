package realm

import "testing"

func TestOfResolvesTheRootModeKey(t *testing.T) {
	if got := Of(false); got != Standard || got.IsRoot() {
		t.Errorf("Of(false) = %v, want Standard", got)
	}
	if got := Of(true); got != Root || !got.IsRoot() {
		t.Errorf("Of(true) = %v, want Root", got)
	}
}

// The String is the wire and CLI name; `aos status` prints it verbatim.
func TestStringIsTheWireName(t *testing.T) {
	if Standard.String() != "standard" {
		t.Errorf("Standard.String() = %q, want standard", Standard.String())
	}
	if Root.String() != "root" {
		t.Errorf("Root.String() = %q, want root", Root.String())
	}
}
