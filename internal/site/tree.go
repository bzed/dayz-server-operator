// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

package site

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Defaults are the instance settings site.yaml can pre-fill (§C3): any
// field an instance leaves unset is taken from here.
type Defaults struct {
	Params       Params          `yaml:"params,omitempty"`
	Updates      UpdatesConfig   `yaml:"updates,omitempty"`
	Restarts     RestartsConfig  `yaml:"restarts,omitempty"`
	Health       HealthConfig    `yaml:"health,omitempty"`
	RestartLimit RestartLimit    `yaml:"restart_limit,omitempty"`
	Stop         StopConfig      `yaml:"stop,omitempty"`
	Notify       NotifyConfig    `yaml:"notify,omitempty"`
	Container    ContainerConfig `yaml:"container,omitempty"`
	Backup       BackupConfig    `yaml:"backup,omitempty"`
	Logs         LogsConfig      `yaml:"logs,omitempty"`
}

// LocalModSource is one site.yaml `local_mods` entry (§C7): a release
// archive pinned by sha256, or an absolute host directory. A local mod with
// no entry is read from the site repo's localmods/<name>/.
type LocalModSource struct {
	URL    string `yaml:"url,omitempty"`
	SHA256 string `yaml:"sha256,omitempty"`
	Path   string `yaml:"path,omitempty"`
}

// Validate checks that exactly one of url (with sha256) or an absolute path is set.
func (l LocalModSource) Validate(name string) error {
	switch {
	case (l.URL == "") == (l.Path == ""):
		return fmt.Errorf("local_mods.%s: exactly one of url or path must be set", name)
	case l.URL != "" && l.SHA256 == "":
		return fmt.Errorf("local_mods.%s: sha256 is required with url", name)
	case l.Path != "" && !filepath.IsAbs(l.Path):
		return fmt.Errorf("local_mods.%s: path must be absolute", name)
	}
	return nil
}

// Site is site.yaml: defaults shared by all instances.
type Site struct {
	// Image is the runtime container image, e.g. "localhost/dzo-runtime:latest".
	Image     string                    `yaml:"image,omitempty"`
	Defaults  Defaults                  `yaml:"defaults,omitempty"`
	LocalMods map[string]LocalModSource `yaml:"local_mods,omitempty"`
}

// LoadedIntegration is an integrations/mods/<id>/integration.yaml with the directory it was
// read from, which its local file sources and hooks are relative to.
type LoadedIntegration struct {
	Integration
	Dir string
}

// Tree is a loaded site checkout: site.yaml, every instance, every map preset and the mod
// integrations. Integrations holds the shared ones by mod id (aliases included),
// InstanceIntegrations the per-instance overrides (instances/<name>/integrations/<id>/), which
// replace the shared one of the same mod for that instance.
type Tree struct {
	Dir                  string
	Site                 Site
	Instances            map[string]Instance
	Maps                 map[string]MapPreset
	Integrations         map[uint64]LoadedIntegration
	InstanceIntegrations map[string]map[uint64]LoadedIntegration
}

// LoadTree reads and validates the site checkout at dir. site.yaml is
// optional; unknown YAML keys are errors, so a typo does not silently
// fall back to a default.
func LoadTree(dir string) (*Tree, error) {
	t := &Tree{
		Dir: dir, Instances: map[string]Instance{}, Maps: map[string]MapPreset{},
		Integrations: map[uint64]LoadedIntegration{}, InstanceIntegrations: map[string]map[uint64]LoadedIntegration{},
	}
	var errs []error
	if _, err := readStrict(filepath.Join(dir, "site.yaml"), &t.Site, true); err != nil {
		errs = append(errs, err)
	}
	for name, l := range t.Site.LocalMods {
		if err := l.Validate(name); err != nil {
			errs = append(errs, err)
		}
	}

	maps, _ := filepath.Glob(filepath.Join(dir, "integrations", "maps", "*.yaml"))
	for _, path := range maps {
		var m MapPreset
		if _, err := readStrict(path, &m, false); err != nil {
			errs = append(errs, err)
			continue
		}
		t.Maps[strings.TrimSuffix(filepath.Base(path), ".yaml")] = m
	}

	loadIntegrations(filepath.Join(dir, "integrations", "mods"), t.Integrations, &errs)

	files, _ := filepath.Glob(filepath.Join(dir, "instances", "*", "instance.yaml"))
	for _, path := range files {
		var inst Instance
		if _, err := readStrict(path, &inst, false); err != nil {
			errs = append(errs, err)
			continue
		}
		if dirName := filepath.Base(filepath.Dir(path)); inst.Name != dirName {
			errs = append(errs, fmt.Errorf("%s: name %q does not match directory %q", path, inst.Name, dirName))
			continue
		}
		if err := inst.Validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		t.Instances[inst.Name] = inst
		mine := map[uint64]LoadedIntegration{}
		loadIntegrations(filepath.Join(filepath.Dir(path), "integrations"), mine, &errs)
		if len(mine) > 0 {
			t.InstanceIntegrations[inst.Name] = mine
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return t, nil
}

// loadIntegrations reads <root>/<id>/integration.yaml for every directory below root into out,
// keyed by the mod id and by each alias. A directory without an integration.yaml is skipped.
func loadIntegrations(root string, out map[uint64]LoadedIntegration, errs *[]error) {
	files, _ := filepath.Glob(filepath.Join(root, "*", "integration.yaml"))
	sort.Strings(files)
	for _, path := range files {
		var in Integration
		if _, err := readStrict(path, &in, false); err != nil {
			*errs = append(*errs, err)
			continue
		}
		if err := in.Validate(); err != nil {
			*errs = append(*errs, fmt.Errorf("%s: %w", path, err))
			continue
		}
		li := LoadedIntegration{Integration: in, Dir: filepath.Dir(path)}
		for _, id := range append([]uint64{in.Mod}, in.Aliases...) {
			out[id] = li
		}
	}
}

// IntegrationFor returns the integration an instance uses for a workshop mod: its own override,
// else the shared one.
func (t *Tree) IntegrationFor(instance string, mod uint64) (LoadedIntegration, bool) {
	if li, ok := t.InstanceIntegrations[instance][mod]; ok {
		return li, true
	}
	li, ok := t.Integrations[mod]
	return li, ok
}

// OverlayDir finds an overlay folder for an instance: instances/<name>/overlays/<overlay>/, else
// overlays/<overlay>/.
func (t *Tree) OverlayDir(instance, overlay string) (string, bool) {
	for _, d := range []string{filepath.Join(t.Dir, "instances", instance, "overlays", overlay), filepath.Join(t.Dir, "overlays", overlay)} {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d, true
		}
	}
	return "", false
}

func readStrict(path string, v any, optional bool) (bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is inside the operator's site checkout
	if err != nil {
		if optional && os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("site: read %s: %w", path, err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) { // empty file = all defaults
		return false, fmt.Errorf("site: parse %s: %w", path, err)
	}
	return true, nil
}

// Instance returns the named instance or an error listing what exists.
func (t *Tree) Instance(name string) (Instance, error) {
	inst, ok := t.Instances[name]
	if !ok {
		return Instance{}, fmt.Errorf("site: no instance %q in %s", name, t.Dir)
	}
	return inst, nil
}

// InstanceNames returns the instance names, sorted.
func (t *Tree) InstanceNames() []string {
	names := make([]string, 0, len(t.Instances))
	for n := range t.Instances {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// AddMod appends ref to the mods list of the instance file at path, keeping
// the rest of the file (comments included) as it is. It is an error if the
// mod is listed already.
func AddMod(path string, ref ModRef) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is inside the operator's site checkout
	if err != nil {
		return fmt.Errorf("site: read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("site: parse %s: %w", path, err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("site: %s: not a YAML mapping", path)
	}
	var entry yaml.Node
	if err := entry.Encode(ref); err != nil {
		return err
	}
	entry.Style = yaml.FlowStyle

	m := doc.Content[0]
	var list *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "mods" {
			list = m.Content[i+1]
		}
	}
	if list == nil {
		list = &yaml.Node{Kind: yaml.SequenceNode}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "mods"}, list)
	}
	for _, n := range list.Content {
		var have ModRef
		if err := n.Decode(&have); err == nil && have.Key() == ref.Key() {
			return fmt.Errorf("site: %s already lists mod %s", path, ref.Key())
		}
	}
	list.Content = append(list.Content, &entry)

	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o600)
}

// editMods runs fn on the mods list of the instance file at path and writes the file back,
// keeping the rest of it (comments included). It is an error if the file has no mods list.
func editMods(path string, fn func(list *yaml.Node) error) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is inside the operator's site checkout
	if err != nil {
		return fmt.Errorf("site: read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("site: parse %s: %w", path, err)
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("site: %s: not a YAML mapping", path)
	}
	var list *yaml.Node
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "mods" {
			list = m.Content[i+1]
		}
	}
	if list == nil || list.Kind != yaml.SequenceNode {
		return fmt.Errorf("site: %s has no mods list", path)
	}
	if err := fn(list); err != nil {
		return err
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}
	return os.WriteFile(path, out.Bytes(), 0o600)
}

func modIndex(list *yaml.Node, key string) int {
	for i, n := range list.Content {
		var ref ModRef
		if err := n.Decode(&ref); err == nil && ref.Key() == key {
			return i
		}
	}
	return -1
}

// RemoveMod deletes the mod with the given key (a workshop id or a local name) from the mods list
// of the instance file at path.
func RemoveMod(path, key string) error {
	return editMods(path, func(list *yaml.Node) error {
		i := modIndex(list, key)
		if i < 0 {
			return fmt.Errorf("site: %s does not list mod %s", path, key)
		}
		list.Content = append(list.Content[:i], list.Content[i+1:]...)
		return nil
	})
}

// MoveMod moves the mod key right before (or, with after, right behind) the mod other in the mods
// list of the instance file at path. The list order is the merge precedence and the order of the
// -mod= and -servermod= arguments.
func MoveMod(path, key, other string, after bool) error {
	if key == other {
		return fmt.Errorf("site: cannot move mod %s relative to itself", key)
	}
	return editMods(path, func(list *yaml.Node) error {
		i := modIndex(list, key)
		if i < 0 {
			return fmt.Errorf("site: %s does not list mod %s", path, key)
		}
		if modIndex(list, other) < 0 {
			return fmt.Errorf("site: %s does not list mod %s", path, other)
		}
		node := list.Content[i]
		list.Content = append(list.Content[:i], list.Content[i+1:]...)
		j := modIndex(list, other)
		if after {
			j++
		}
		list.Content = append(list.Content[:j], append([]*yaml.Node{node}, list.Content[j:]...)...)
		return nil
	})
}
