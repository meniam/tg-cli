package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAudit04_BindingSeesThroughASymlink covers sameDir: a data directory reached
// through a symbolic link is the recorded one, not a second account.
func TestAudit04_BindingSeesThroughASymlink(t *testing.T) {
	first, second := bindingFixture(t)
	if err := BindDatabase(first); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(shortTempDir(t), "home-link")
	if err := os.Symlink(first.Dir, link); err != nil {
		t.Fatal(err)
	}
	viaLink := first
	viaLink.Dir = link
	if err := CheckDatabaseBinding(viaLink); err != nil {
		t.Errorf("the recorded directory behind a symlink must pass: %v", err)
	}
	// The same link to another directory is still another account.
	other := filepath.Join(shortTempDir(t), "other-link")
	if err := os.Symlink(second.Dir, other); err != nil {
		t.Fatal(err)
	}
	viaOther := second
	viaOther.Dir = other
	var binding *BindingError
	if err := CheckDatabaseBinding(viaOther); !errors.As(err, &binding) {
		t.Errorf("a symlink to another data directory must be a mismatch: %v", err)
	}
}

// TestAudit04_BindingUnreadableRecordIsNotAMismatch covers a record that cannot be
// read: the answer is a plain error and not a *BindingError, so the owner does not
// get advice to remove a record that is only unreadable.
func TestAudit04_BindingUnreadableRecordIsNotAMismatch(t *testing.T) {
	first, _ := bindingFixture(t)
	if err := os.Mkdir(DatabaseBindingFile(first), 0o700); err != nil {
		t.Fatal(err)
	}
	err := CheckDatabaseBinding(first)
	var binding *BindingError
	if err == nil || errors.As(err, &binding) || !strings.Contains(err.Error(), "read the data directory record") {
		t.Fatalf("an unreadable record must be a plain read error: %v", err)
	}
}

// TestAudit04_WriteFileAtomicNeverReplaces covers the race of two starts: a record
// that appeared meanwhile stays, the loser gets an error and leaves no temporary
// file.
func TestAudit04_WriteFileAtomicNeverReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data-dir")
	if err := os.WriteFile(path, []byte("winner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := writeFileAtomic(path, []byte("loser\n"))
	if err == nil || !strings.Contains(err.Error(), "appeared during this start") {
		t.Fatalf("error = %v, want the appeared-during-this-start refusal", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "winner\n" {
		t.Errorf("the record = %q %v, want the winner's", got, readErr)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("directory = %v %v, want only the record", entries, err)
	}
}
