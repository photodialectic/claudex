package containers

import (
    "testing"
    "time"

    "claudex/internal/dockerx"
)

func TestListFiltersByLabelAndStatus(t *testing.T) {
    now := time.Now()
    f := &dockerx.Fake{Containers: map[string]dockerx.Container{}}
    // Container without claudex label should be ignored
    f.Containers["plain"] = dockerx.Container{Name: "plain", Status: "running", CreatedAt: now}
    // Running claudex container
    f.Containers["c1"] = dockerx.Container{Name: "c1", Status: "running", CreatedAt: now.Add(1 * time.Minute), Labels: map[string]string{"com.claudex.signature": "abc", "com.claudex.slug": "s"}}
    // Stopped claudex container
    f.Containers["c2"] = dockerx.Container{Name: "c2", Status: "exited", CreatedAt: now.Add(2 * time.Minute), Labels: map[string]string{"com.claudex.signature": "def", "com.claudex.slug": "s"}}

    // includeStopped=false should only return running claudex containers
    got, err := List(f, false)
    if err != nil {
        t.Fatalf("List error: %v", err)
    }
    if len(got) != 1 || got[0].Name != "c1" {
        t.Fatalf("expected only c1, got %+v", got)
    }

    // includeStopped=true should include both c1 and c2, sorted by CreatedAt
    got, err = List(f, true)
    if err != nil {
        t.Fatalf("List error: %v", err)
    }
    if len(got) != 2 || got[0].Name != "c1" || got[1].Name != "c2" {
        t.Fatalf("expected [c1 c2], got %+v", got)
    }
}

