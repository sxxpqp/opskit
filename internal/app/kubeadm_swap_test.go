package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDisableActiveKubeadmSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "swaps")
	header := "Filename\tType\tSize\tUsed\tPriority\n"
	if err := os.WriteFile(path, []byte(header), 0600); err != nil {
		t.Fatal(err)
	}
	count := 0
	swapoff := func() error {
		count++
		return nil
	}
	if err := disableActiveKubeadmSwap(path, swapoff); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("swapoff called even though /proc/swaps was empty")
	}

	if err := os.WriteFile(path, []byte(header+"/swapfile partition 1024 0 -2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := disableActiveKubeadmSwap(path, swapoff); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("swapoff not called for active swap: %d", count)
	}

	fail := errors.New("swapoff denied")
	if err := disableActiveKubeadmSwap(path, func() error {
		return fail
	}); !errors.Is(err, fail) {
		t.Fatalf("swapoff failure was suppressed: %v", err)
	}

	if err := os.WriteFile(path, []byte("malformed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := disableActiveKubeadmSwap(path, swapoff); err == nil {
		t.Fatal("unexpectedly accepted malformed swap status")
	}
}
