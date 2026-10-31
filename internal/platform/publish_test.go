//go:build darwin || windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestPublishExclusivePreservesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "candidate"), filepath.Join(dir, "delivered")
	if err := os.WriteFile(source, []byte("verified candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("existing delivery"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PublishExclusive(source, target); !errors.Is(err, ErrPublishExists) {
		t.Fatalf("collision: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing delivery" {
		t.Fatal("destination overwritten")
	}
	if _, err = os.Stat(source); err != nil {
		t.Fatal("candidate lost on collision")
	}
}

func TestPublishExclusiveConcurrentCandidatesHaveExactlyOneWinner(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "delivered")
	const count = 24
	sources := make([]string, count)
	for i := range sources {
		file, err := os.CreateTemp(dir, "candidate-")
		if err != nil {
			t.Fatal(err)
		}
		sources[i] = file.Name()
		if _, err = file.Write([]byte{byte(i)}); err != nil {
			t.Fatal(err)
		}
		if err = file.Sync(); err != nil {
			t.Fatal(err)
		}
		file.Close()
	}
	results := make(chan int, count)
	failures := make(chan error, count)
	var wg sync.WaitGroup
	for i, source := range sources {
		wg.Add(1)
		go func(i int, source string) {
			defer wg.Done()
			err := PublishExclusive(source, target)
			if err == nil {
				results <- i
			} else if !errors.Is(err, ErrPublishExists) {
				failures <- err
			}
		}(i, source)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("winners=%d", len(results))
	}
	winner := <-results
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 || int(data[0]) != winner {
		t.Fatal("published bytes differ from winning candidate")
	}
	for i, source := range sources {
		_, err := os.Stat(source)
		if i == winner && !os.IsNotExist(err) {
			t.Fatal("winner temporary not moved")
		}
		if i != winner && err != nil {
			t.Fatal("losing candidate lost")
		}
	}
}

func TestPublishExclusiveDoesNotFollowTargetSymlink(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	target := filepath.Join(dir, "link")
	source := filepath.Join(dir, "candidate")
	os.WriteFile(original, []byte("original"), 0o600)
	os.WriteFile(source, []byte("candidate"), 0o600)
	if err := os.Symlink(original, target); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := PublishExclusive(source, target); !errors.Is(err, ErrPublishExists) {
		t.Fatalf("symlink target collision: %v", err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "original" {
		t.Fatal("symlink target modified")
	}
}
