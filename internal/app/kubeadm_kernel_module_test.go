package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEnsureKubeadmKernelModules(t *testing.T) {
	root := t.TempDir()
	for _, module := range []string{"overlay", "br_netfilter"} {
		if err := os.Mkdir(filepath.Join(root, module), 0700); err != nil {
			t.Fatal(err)
		}
	}
	var calls []string
	load := func(name string) error {
		calls = append(calls, name)
		return nil
	}
	if err := ensureKubeadmKernelModules(root, load); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 0 {
		t.Fatalf("already available modules were needlessly loaded: %v", calls)
	}

	if err := os.Remove(filepath.Join(root, "br_netfilter")); err != nil {
		t.Fatal(err)
	}
	if err := ensureKubeadmKernelModules(root, load); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"br_netfilter"}) {
		t.Fatalf("expected to load only missing module, got: %v", calls)
	}

	loadFailure := errors.New("module unavailable")
	if err := ensureKubeadmKernelModules(root, func(name string) error {
		return loadFailure
	}); !errors.Is(err, loadFailure) {
		t.Fatalf("module loading failure must not be skipped: %v", err)
	}
}
