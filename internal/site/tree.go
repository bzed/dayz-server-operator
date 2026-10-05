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

// Tree is a loaded site checkout: site.yaml, every instance and every map preset.
type Tree struct {
	Dir       string
	Site      Site
	Instances map[string]Instance
	Maps      map[string]MapPreset
}

// LoadTree reads and validates the site checkout at dir. site.yaml is
// optional; unknown YAML keys are errors, so a typo does not silently
// fall back to a default.
func LoadTree(dir string) (*Tree, error) {
	t := &Tree{Dir: dir, Instances: map[string]Instance{}, Maps: map[string]MapPreset{}}
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
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return t, nil
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
