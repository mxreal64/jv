// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mxreal64/jv/lifecycle"
)

type PluginExecutor struct {
	Descriptor *PluginDescriptor
	Req        Request
	Inc        *lifecycle.Incremental
	// Inputs are glob patterns, relative to the module directory, of files the plugin reads. The plugin
	// runs again when any of them (or the plugin binary, its configuration or the phase) changed.
	Inputs []string
}

func (e *PluginExecutor) Phase() lifecycle.Phase { return lifecycle.Phase(e.Req.Phase) }
func (e *PluginExecutor) ID() string             { return e.Req.ID }

var unsafeInName = strings.NewReplacer(":", "_", "/", "_", "\\", "_", " ", "_")

// saved is what a plugin run contributed to the build; it is replayed when the run is skipped.
type saved struct {
	SourceRoots []string          `json:"sourceRoots,omitempty"`
	Properties  map[string]string `json:"properties,omitempty"`
}

func (e *PluginExecutor) fingerprint(bc lifecycle.BuildContext) []any {
	in := []any{e.Descriptor.Binary, e.Req.Config, e.Req.Phase}
	if st, err := os.Stat(e.Descriptor.Binary); err == nil { // an upgraded binary must rerun the plugin
		in = append(in, fmt.Sprintf("bin:%d:%d", st.Size(), st.ModTime().UnixNano()))
	}
	var files []string
	for _, pat := range e.Inputs {
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(bc.Dir, pat)
		}
		matches, _ := filepath.Glob(pat)
		for _, m := range matches {
			if st, err := os.Stat(m); err == nil && !st.IsDir() {
				files = append(files, fmt.Sprintf("%s:%d:%d", m, st.Size(), st.ModTime().UnixNano()))
			}
		}
	}
	return append(in, files)
}

func (e *PluginExecutor) apply(bc *lifecycle.BuildContext, sv saved) {
	for _, r := range sv.SourceRoots {
		bc.AddSourceRoot(r, e.Req.Phase == "generate-test-sources")
	}
	if bc.Props != nil {
		for k, v := range sv.Properties {
			bc.Props[k] = v
		}
	}
}

func (e *PluginExecutor) Run(ctx context.Context, bc lifecycle.BuildContext) error {
	e.Req.Context = ToJSONContext(bc)

	name := unsafeInName.Replace(e.Req.ID)
	stateFile := filepath.Join(bc.TargetDir, ".jv", fmt.Sprintf("plugin-%s.state", name))
	savedFile := filepath.Join(bc.TargetDir, ".jv", fmt.Sprintf("plugin-%s.out.json", name))
	inc := e.Inc
	if inc == nil {
		inc = &lifecycle.Incremental{StateFile: stateFile}
	}

	inputs := e.fingerprint(bc)
	changed, err := inc.Changed(inputs...)
	if err == nil && !changed {
		// Nothing changed: replay what the last run added (generated source roots, properties), so a
		// skipped plugin does not make its generated sources disappear from the build.
		if data, rerr := os.ReadFile(savedFile); rerr == nil {
			var sv saved
			if json.Unmarshal(data, &sv) == nil {
				e.apply(&bc, sv)
				return nil
			}
		}
	}

	resp, err := RunPlugin(ctx, e.Descriptor.Binary, e.Req)
	if err != nil {
		return fmt.Errorf("plugin %s execution: %w", e.Req.ID, err)
	}

	if resp.Skip {
		return nil
	}

	// Source roots a plugin reports are relative to the module.
	sv := saved{Properties: resp.Properties}
	for _, r := range resp.SourceRoots {
		if !filepath.IsAbs(r) && bc.Dir != "" {
			r = filepath.Join(bc.Dir, r)
		}
		sv.SourceRoots = append(sv.SourceRoots, r)
	}
	e.apply(&bc, sv)

	if data, merr := json.Marshal(sv); merr == nil {
		_ = os.MkdirAll(filepath.Dir(savedFile), 0o755)
		_ = os.WriteFile(savedFile, data, 0o644)
	}
	_ = inc.Done(inputs...)
	return nil
}
