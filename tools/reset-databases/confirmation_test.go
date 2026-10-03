package resetdatabases

import (
	"os"
	"testing"
)

// withStdin feeds input to PromptForConfirmation as if typed at the console.
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(input); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	original := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = original
		_ = r.Close()
	})
}

func TestPromptForConfirmationRefusesEmptyKeys(t *testing.T) {
	for _, keys := range [][2]string{{"", ""}, {"key1", ""}, {"", "key2"}, {" ", "key2"}} {
		withStdin(t, keys[0]+"\n"+keys[1]+"\n")
		if err := PromptForConfirmation(keys[0], keys[1]); err == nil {
			t.Errorf("keys %q: confirmation passed", keys)
		}
	}
}

func TestPromptForConfirmationAcceptsMatchingKeys(t *testing.T) {
	withStdin(t, "key1\nkey2\n")
	if err := PromptForConfirmation("key1", "key2"); err != nil {
		t.Fatal(err)
	}
}
