// SPDX-FileCopyrightText: 2026 Bernd Zeimetz <bernd@bzed.de>
// SPDX-License-Identifier: AGPL-3.0-or-later

// Package integrate turns an instance's mod integrations and overlays (§C6 steps 2 to 4) into
// the contributions the render merges into its staging tree: it reads the files from the site
// repository, from a URL (cached, optionally pinned by hash) or from the installed mod, and wires
// the post_merge hooks.
package integrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/bzed/dayz-server-operator/internal/hooks"
	"github.com/bzed/dayz-server-operator/internal/mission"
	"github.com/bzed/dayz-server-operator/internal/resolve"
	"github.com/bzed/dayz-server-operator/internal/site"
)

// maxFile bounds a downloaded integration file.
const maxFile = 32 << 20

// OverlayFile is the name of an overlay's manifest; it is not a mission file.
const OverlayFile = "overlay.yaml"

// Options are what Collect needs besides the tree and the instance.
type Options struct {
	CacheDir string // downloaded files are kept in <CacheDir>/integrations
	// Get fetches a URL; nil uses a plain HTTP client with a timeout.
	Get func(ctx context.Context, url string) ([]byte, error)
	// HookTimeout bounds one post_merge script; 0 means ten minutes.
	HookTimeout time.Duration
	// Log gets the output of hooks.
	Log func(format string, args ...any)
	// Env is added to the hook environment (DZO_LIVE_MISSION, ...).
	Env map[string]string
}

// Collect gathers the contributions of an instance in merge order: its messages.xml and EditorFiles, then every
// mod that has an integration (in the order of the mods list, so later wins), then its overlays
// (in the order of the overlays list), then the instance's own post_merge hooks.
func Collect(ctx context.Context, t *site.Tree, inst *resolve.Instance, o Options) ([]mission.Contribution, error) {
	var out []mission.Contribution
	instDir := filepath.Join(t.Dir, "instances", inst.Name)
	msgs, err := instanceMessages(instDir, inst)
	if err != nil {
		return nil, err
	}
	out = append(out, mission.Contribution{Name: "instance messages.xml", Folder: "custom_instance", Files: []mission.ContribFile{{Name: "messages.xml", Data: msgs}}})

	// EditorFiles (.dze) for the DayZEditorLoader servermod go to <mission>/EditorFiles.
	if files, err := readFiles("EditorFiles", filepath.Join(instDir, "EditorFiles"), ""); err == nil {
		if len(files) > 0 {
			out = append(out, mission.Contribution{Name: "instance EditorFiles", Folder: "EditorFiles", Files: files})
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	for _, m := range inst.Mods {
		if m.ID == 0 {
			continue // a local mod has no workshop id, so no integration
		}
		li, ok := t.IntegrationFor(inst.Name, m.ID)
		if !ok {
			continue
		}
		c := mission.Contribution{Name: fmt.Sprintf("mod %d (%s)", m.ID, li.Name), Folder: fmt.Sprintf("mod_%d", m.ID)}
		keys := make([]string, 0, len(li.Files))
		for k := range li.Files {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			src := li.Files[k]
			if len(src.Maps) > 0 && !contains(src.Maps, inst.Map) {
				continue
			}
			data, err := readSource(ctx, li.Dir, m.Dir, src, o)
			if err != nil {
				return nil, fmt.Errorf("mod %d (%s): files.%s: %w", m.ID, li.Name, k, err)
			}
			if data, err = Normalize(k, data, li.Normalize); err != nil {
				return nil, fmt.Errorf("mod %d (%s): normalize: %w", m.ID, li.Name, err)
			}
			if strings.EqualFold(k, "messages.xml") || strings.EqualFold(k, "db/messages.xml") {
				if err := site.CheckMessagesXML(data); err != nil {
					return nil, fmt.Errorf("mod %d (%s): %w", m.ID, li.Name, err)
				}
			}
			c.Files = append(c.Files, mission.ContribFile{Name: k, Data: data})
		}
		c.AfterMerge = hookRunner(inst, li.Hooks.PostMerge, li.Dir, o)
		out = append(out, c)
	}

	for _, name := range inst.Overlays {
		dir, ok := t.OverlayDir(inst.Name, name)
		if !ok {
			return nil, fmt.Errorf("overlay %q not found", name)
		}
		c, err := overlay(name, dir)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}

	if len(inst.Hooks.PostMerge) > 0 {
		out = append(out, mission.Contribution{Name: "instance post_merge hooks", Folder: "custom_instance", AfterMerge: hookRunner(inst, inst.Hooks.PostMerge, instDir, o)})
	}
	return out, nil
}

// instanceMessages is the instance's db/messages.xml: its own instances/<name>/messages.xml (used
// as is), or generated from the welcome message and messages: in instance.yaml. With no welcome
// and no messages it is an empty <messages>: dzo never leaves the pristine mission's example
// messages (possibly a shutdown countdown) in place. A file with a countdown or shutdown is
// refused, restarts are dzo's.
func instanceMessages(instDir string, inst *resolve.Instance) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(instDir, "messages.xml")) //nolint:gosec // the site checkout
	switch {
	case err == nil:
		if len(inst.Messages) > 0 {
			return nil, fmt.Errorf("instance %s: both instances/%s/messages.xml and messages: in instance.yaml are set, use one", inst.Name, inst.Name)
		}
		if err := site.CheckMessagesXML(data); err != nil {
			return nil, fmt.Errorf("instance %s: %w", inst.Name, err)
		}
		return data, nil
	case os.IsNotExist(err):
		var ms []site.Message
		if w, ok := site.WelcomeMessage(inst.Welcome); ok {
			ms = append(ms, w)
		}
		return site.RenderMessagesXML(append(ms, inst.Messages...))
	default:
		return nil, err
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

// readSource reads one integration file.
func readSource(ctx context.Context, integrationDir, modDir string, src site.FileSource, o Options) ([]byte, error) {
	switch src.Source {
	case "local":
		return readInside(integrationDir, src.Path)
	case "mod":
		if modDir == "" {
			return nil, fmt.Errorf("the mod is not downloaded")
		}
		return readInside(modDir, src.Path)
	case "url":
		return fetch(ctx, src, o)
	}
	return nil, fmt.Errorf("unknown source %q", src.Source)
}

// readInside reads rel below root and refuses a path that leaves it (".." or a link out).
func readInside(root, rel string) ([]byte, error) {
	if filepath.IsAbs(rel) {
		return nil, fmt.Errorf("path %q must be relative", rel)
	}
	abs, err := filepath.EvalSymlinks(filepath.Join(root, rel))
	if err != nil {
		return nil, err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if abs != base && !strings.HasPrefix(abs, base+string(filepath.Separator)) {
		return nil, fmt.Errorf("path %q leaves %s", rel, root)
	}
	return os.ReadFile(abs) //nolint:gosec // checked to be inside root
}

// fetch downloads a URL once and keeps it by content: with a sha256 the download is verified and
// kept under that hash, without one it is kept under the hash of the URL and not fetched again.
func fetch(ctx context.Context, src site.FileSource, o Options) ([]byte, error) {
	dir := filepath.Join(o.CacheDir, "integrations")
	key := src.SHA256
	if key == "" {
		sum := sha256.Sum256([]byte(src.URL))
		key = "url-" + hex.EncodeToString(sum[:])
	}
	cached := filepath.Join(dir, key)
	if data, err := os.ReadFile(cached); err == nil { //nolint:gosec // our own cache
		if src.SHA256 == "" || hashOf(data) == src.SHA256 {
			return data, nil
		}
	}
	get := o.Get
	if get == nil {
		get = httpGet
	}
	data, err := get(ctx, src.URL)
	if err != nil {
		return nil, err
	}
	if src.SHA256 != "" && hashOf(data) != src.SHA256 {
		return nil, fmt.Errorf("%s: sha256 is %s, not the pinned %s", src.URL, hashOf(data), src.SHA256)
	}
	if err := os.MkdirAll(dir, 0o750); err == nil {
		_ = os.WriteFile(cached, data, 0o600) // a cache that cannot be written only costs a download
	}
	return data, nil
}

func hashOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func httpGet(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // a URL from the operator's own site repository
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxFile+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxFile {
		return nil, fmt.Errorf("%s: more than %d MB", url, maxFile>>20)
	}
	return b, nil
}

// overlay reads an overlay folder: every file except overlay.yaml is a contribution, the
// manifest says which of them cfggameplay.json must reference.
func overlay(name, dir string) (mission.Contribution, error) {
	c := mission.Contribution{Name: "overlay " + name, Folder: "custom_" + name}
	if b, err := os.ReadFile(filepath.Join(dir, OverlayFile)); err == nil { //nolint:gosec // the site checkout
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&c.Overlay); err != nil && err != io.EOF {
			return c, fmt.Errorf("overlay %s: %s: %w", name, OverlayFile, err)
		}
	}
	files, err := readFiles("overlay "+name, dir, OverlayFile)
	c.Files = files
	return c, err
}

// readFiles reads every regular file below dir (except the top-level file named skip) as a
// contribution file; anything else is an error.
func readFiles(what, dir, skip string) ([]mission.ContribFile, error) {
	var files []mission.ContribFile
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() || rel == skip {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s: %s is not a regular file", what, rel)
		}
		data, err := os.ReadFile(p) //nolint:gosec // inside the site checkout
		if err != nil {
			return err
		}
		files = append(files, mission.ContribFile{Name: filepath.ToSlash(rel), Data: data})
		return nil
	})
	return files, err
}

// hookRunner returns the AfterMerge callback that runs scripts (relative to dir) in the staging
// tree: a failing script fails the merge.
func hookRunner(inst *resolve.Instance, scripts []string, dir string, o Options) func(string) error {
	if len(scripts) == 0 {
		return nil
	}
	timeout := o.HookTimeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	return func(staging string) error {
		env := map[string]string{"DZO_STAGING": staging, "DZO_MAP": inst.Map, "DZO_LIVE_MISSION": inst.Paths.Live}
		for k, v := range o.Env {
			env[k] = v
		}
		r := &hooks.Runner{Dir: dir, Env: env, Timeout: timeout}
		for _, res := range hooks.RunAll(context.Background(), r, scripts, hooks.Context{Instance: inst.Name, Point: "post_merge"}, true) {
			if res.Stdout != "" && o.Log != nil {
				o.Log("post_merge %s: %s", res.Script, strings.TrimSpace(res.Stdout))
			}
			if res.Err != nil {
				return res.Err
			}
		}
		return nil
	}
}
