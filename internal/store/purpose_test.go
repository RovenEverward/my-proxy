package store

import (
	"errors"
	"github.com/RovenEverward/my-proxy/internal/model"
	"path/filepath"
	"testing"
)

func TestPurposePersistsReopensAndMetadataCascades(t *testing.T) {
	path := filepath.Join(t.TempDir(), "purpose.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := &model.Purpose{Slug: "media", Name: "Media", Kind: "media"}
	if err = s.CreatePurpose(p); err != nil {
		t.Fatal(err)
	}
	n := newTestNode(t, s, "one")
	in := newTestInbound(t, s, n.ID, "one")
	h := &model.Host{InboundID: in.ID, Remark: "One"}
	if err = s.CreateHost(h); err != nil {
		t.Fatal(err)
	}
	m := model.PurposeMetadata{Region: "US", Line: "premium", Purposes: []string{"media"}}
	for kind, id := range map[string]int64{"nodes": n.ID, "hosts": h.ID} {
		if err = s.SavePurposeMetadata(kind, id, m); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetPurpose("media")
	if err != nil || got.Strategy != "url-test" {
		t.Fatalf("%+v %v", got, err)
	}
	if err = s.DeleteNode(n.ID); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"nodes", "hosts"} {
		items, e := s.ListPurposeMetadata(kind)
		if e != nil || len(items) != 0 {
			t.Fatalf("cascade %s %v %v", kind, items, e)
		}
	}
	if err = s.DeletePurpose("media"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpdatePurpose(p); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if _, err = s.GetPurpose("media"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
