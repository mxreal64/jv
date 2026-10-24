// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package plugin

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mxreal64/jv/lifecycle"
)

type PluginExecutor struct {
	Descriptor *PluginDescriptor
	Req        Request
	Inc        *lifecycle.Incremental
}

func (e *PluginExecutor) Phase() lifecycle.Phase { return lifecycle.Phase(e.Req.Phase) }
func (e *PluginExecutor) ID() string             { return e.Req.ID }

func (e *PluginExecutor) Run(ctx context.Context, bc lifecycle.BuildContext) error {
	e.Req.Context = ToJSONContext(bc)

	stateFile := filepath.Join(bc.TargetDir, ".jv", fmt.Sprintf("plugin-%s.state", e.Req.ID))
	inc := e.Inc
	if inc == nil {
		inc = &lifecycle.Incremental{StateFile: stateFile}
	}

	changed, err := inc.Changed(e.Descriptor.Binary, e.Req.Config, e.Req.Phase)
	if err == nil && !changed {
		return nil
	}

	resp, err := RunPlugin(ctx, e.Descriptor.Binary, e.Req)
	if err != nil {
		return fmt.Errorf("plugin %s execution: %w", e.Req.ID, err)
	}

	if resp.Skip {
		return nil
	}

	// Apply outputs and source roots
	if len(resp.SourceRoots) > 0 {
		if e.Req.Phase == "generate-test-sources" {
			bc.ExtraTestSourceRoots = append(bc.ExtraTestSourceRoots, resp.SourceRoots...)
		} else {
			bc.ExtraSourceRoots = append(bc.ExtraSourceRoots, resp.SourceRoots...)
		}
	}

	for k, v := range resp.Properties {
		bc.Props[k] = v
	}

	_ = inc.Done(e.Descriptor.Binary, e.Req.Config, e.Req.Phase)
	return nil
}
