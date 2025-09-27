package containers

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"claudex/internal/dockerx"
)

// Exists returns whether a container exists, whether it's running, and basic info.
func Exists(dx dockerx.Docker, name string) (bool, bool, *dockerx.Container, error) {
	c, err := dx.Inspect(name)
	if err != nil {
		return false, false, nil, nil
	}
	running := c.Status == "running"
	return true, running, &c, nil
}

// List returns claudex containers, optionally including stopped ones.
func List(dx dockerx.Docker, includeStopped bool) ([]dockerx.Container, error) {
	names, err := dx.PS(includeStopped)
	if err != nil {
		return nil, err
	}
	var res []dockerx.Container
	for _, n := range names {
		c, err := dx.Inspect(n)
		if err != nil {
			continue
		}
		if c.Labels["com.claudex.signature"] == "" {
			continue
		}
		if !includeStopped && c.Status != "running" {
			continue
		}
		res = append(res, c)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].CreatedAt.Before(res[j].CreatedAt) })
	return res, nil
}

// MountsFromLabel parses the claudex mounts label into a slice.
func MountsFromLabel(info *dockerx.Container) ([]string, error) {
	s := info.Labels["com.claudex.mounts"]
	if s == "" {
		return nil, errors.New("mount label missing")
	}
	var m []string
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// WarnOrErrorOnMountMismatch either errors (strict) or prints a warning when mounts differ.
// The caller is responsible for printing messages and deciding behavior; this function returns an error only if strict is true and a mismatch is detected.
func WarnOrErrorOnMountMismatch(info *dockerx.Container, normDirs []string, strict bool, name string) error {
	mounts, err := MountsFromLabel(info)
	if err != nil {
		if strict {
			return fmt.Errorf("container %s missing mount label: %v", name, err)
		}
		return nil
	}
	if !equalStrings(mounts, normDirs) {
		if strict {
			return fmt.Errorf("existing container %s mounts differ from requested", name)
		}
	}
	return nil
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
