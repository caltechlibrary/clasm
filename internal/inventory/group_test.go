package inventory

import (
	"reflect"
	"sort"
	"testing"
)

func TestGroupInstancesByProject(t *testing.T) {
	instances := []Instance{
		{InstanceID: "i-1", Project: "caltechauthors"},
		{InstanceID: "i-2", Project: "caltechdata"},
		{InstanceID: "i-3", Project: "caltechauthors"},
		{InstanceID: "i-4"}, // untagged
	}

	got := GroupInstancesByProject(instances)

	wantKeys := []string{"", "caltechauthors", "caltechdata"}
	var gotKeys []string
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	sort.Strings(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("keys = %v, want %v", gotKeys, wantKeys)
	}

	if len(got["caltechauthors"]) != 2 {
		t.Errorf("caltechauthors group has %d instances, want 2", len(got["caltechauthors"]))
	}
	if len(got["caltechdata"]) != 1 {
		t.Errorf("caltechdata group has %d instances, want 1", len(got["caltechdata"]))
	}
	if len(got[""]) != 1 || got[""][0].InstanceID != "i-4" {
		t.Errorf("untagged group = %+v, want [i-4]", got[""])
	}
}

func TestGroupInstancesByEnvironment(t *testing.T) {
	instances := []Instance{
		{InstanceID: "i-1", Environment: "production"},
		{InstanceID: "i-2", Environment: "development"},
		{InstanceID: "i-3", Environment: "production"},
	}

	got := GroupInstancesByEnvironment(instances)

	if len(got["production"]) != 2 {
		t.Errorf("production group has %d instances, want 2", len(got["production"]))
	}
	if len(got["development"]) != 1 {
		t.Errorf("development group has %d instances, want 1", len(got["development"]))
	}
}

func TestGroupImagesByProject(t *testing.T) {
	images := []Image{
		{ImageID: "ami-1", Project: "caltechauthors"},
		{ImageID: "ami-2", Project: "caltechdata"},
	}

	got := GroupImagesByProject(images)

	if len(got["caltechauthors"]) != 1 || got["caltechauthors"][0].ImageID != "ami-1" {
		t.Errorf("caltechauthors group = %+v, want [ami-1]", got["caltechauthors"])
	}
	if len(got["caltechdata"]) != 1 || got["caltechdata"][0].ImageID != "ami-2" {
		t.Errorf("caltechdata group = %+v, want [ami-2]", got["caltechdata"])
	}
}

func TestGroupImagesByEnvironment(t *testing.T) {
	images := []Image{
		{ImageID: "ami-1", Environment: "production"},
		{ImageID: "ami-2", Environment: "test"},
	}

	got := GroupImagesByEnvironment(images)

	if len(got["production"]) != 1 || got["production"][0].ImageID != "ami-1" {
		t.Errorf("production group = %+v, want [ami-1]", got["production"])
	}
	if len(got["test"]) != 1 || got["test"][0].ImageID != "ami-2" {
		t.Errorf("test group = %+v, want [ami-2]", got["test"])
	}
}
