package secrets

import (
	"os/exec"
	"runtime"
	"testing"
)

func TestRejectsInjection(t *testing.T) {
	for _, bad := range []string{"", "a\"b", "a\nb", "a\\b"} {
		if err := (&Memory{}).Set("x", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
		if err := (Keychain{Service: "dzinga-test"}).Set("x", bad); err == nil {
			t.Errorf("keychain accepted %q", bad)
		}
	}
}

// Round trip through the real Keychain (macOS only), in a throwaway service name.
func TestKeychainRoundTrip(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	if _, err := exec.LookPath("security"); err != nil {
		t.Skip("no security tool")
	}
	k := Keychain{Service: "dzinga-test-roundtrip"}
	defer k.Delete("tester@example.com")
	if err := k.Set("tester@example.com", "abcd efgh-ijkl.mnop_q*r!"); err != nil {
		t.Skipf("keychain not writable here: %v", err)
	}
	got, err := k.Get("tester@example.com")
	if err != nil || got != "abcd efgh-ijkl.mnop_q*r!" {
		t.Fatalf("got %q, %v", got, err)
	}
	if err := k.Delete("tester@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Get("tester@example.com"); err != ErrNotFound {
		t.Errorf("after delete: %v", err)
	}
}
