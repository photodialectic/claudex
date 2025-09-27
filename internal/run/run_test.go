package run

import (
    "testing"
)

func TestParseArgsAndDerive(t *testing.T) {
    args := []string{"--host-network", "--name", "X", "--parallel", "--strict-mounts", "--replace", "."}
    o, err := ParseArgs(args)
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    if !o.UseHostNetwork || !o.StrictMounts || !o.ForceReplace || !o.AlwaysParallel {
        t.Fatalf("flags not parsed correctly: %+v", o)
    }
    if len(o.Workdirs) != 1 {
        t.Fatalf("expected 1 workdir, got %v", o.Workdirs)
    }
    if err := o.Derive(); err != nil {
        t.Fatalf("derive: %v", err)
    }
    if o.Name == "" || o.Signature == "" || o.Slug == "" || len(o.Normalized) == 0 {
        t.Fatalf("missing derived fields: %+v", o)
    }
}

