package workflow

import (
	"testing"

	"github.com/caltechlibrary/clasm/internal/inventory"
)

func TestInstanceUploadPrefix_UsesNameWhenPresent(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors"}
	if got := InstanceUploadPrefix(inst); got != "newauthors" {
		t.Errorf("got %q, want %q", got, "newauthors")
	}
}

func TestInstanceUploadPrefix_FallsBackToInstanceIDWhenNameBlank(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1"}
	if got := InstanceUploadPrefix(inst); got != "i-1" {
		t.Errorf("got %q, want %q", got, "i-1")
	}
}

func TestOpenSearchIndexPrefix_UsesProjectWhenPresent(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors", Project: "caltechauthors"}
	if got := OpenSearchIndexPrefix(inst); got != "caltechauthors" {
		t.Errorf("got %q, want %q", got, "caltechauthors")
	}
}

func TestOpenSearchIndexPrefix_FallsBackToUploadPrefixWhenProjectBlank(t *testing.T) {
	inst := inventory.Instance{InstanceID: "i-1", Name: "newauthors"}
	if got := OpenSearchIndexPrefix(inst); got != "newauthors" {
		t.Errorf("got %q, want %q", got, "newauthors")
	}
}
