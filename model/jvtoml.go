// SPDX-License-Identifier: GPL-3.0-only
// Copyright mxreal64, 2026

package model

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// JVToml is the project-level jv configuration (jv.toml next to a module's pom.xml).
type JVToml struct {
	Build      BuildConfig     `toml:"build"`
	Test       TestConfig      `toml:"test"`
	Plugins    []PluginDecl    `toml:"plugin"`
	Generators []GeneratorDecl `toml:"generator"`

	// Warnings lists keys and sections jv does not recognise (usually typos).
	Warnings []string
}

type BuildConfig struct {
	Source   string `toml:"source"`
	Release  string `toml:"release"`
	Encoding string `toml:"encoding"`
}

type TestConfig struct {
	Includes       []string `toml:"includes"`
	Excludes       []string `toml:"excludes"`
	Groups         string   `toml:"groups"`
	ExcludedGroups string   `toml:"excludedGroups"`
	ArgLine        string   `toml:"argLine"`
}

type PluginDecl struct {
	ID      string                 `toml:"id"`
	Version string                 `toml:"version"`
	Phase   string                 `toml:"phase"`
	Config  map[string]interface{} `toml:"config"`
}

type GeneratorDecl struct {
	ID      string   `toml:"id"`
	Phase   string   `toml:"phase"`
	Command []string `toml:"command"`
	Output  string   `toml:"output"`
	Inputs  []string `toml:"inputs"`
}

// Load reads jv.toml from dir. A missing file is not an error and yields the zero value; a file that
// cannot be parsed is.
func Load(dir string) (JVToml, error) {
	p := filepath.Join(dir, "jv.toml")
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return JVToml{}, nil
	}
	if err != nil {
		return JVToml{}, err
	}
	doc, err := parseTOML(data)
	if err != nil {
		return JVToml{}, fmt.Errorf("%s: %w", p, err)
	}

	var res JVToml
	res.PluginDeclInit()
	d := decoder{}

	if t, ok := d.table(doc, "build"); ok {
		res.Build.Source = d.str(t, "build", "source")
		res.Build.Release = d.str(t, "build", "release")
		res.Build.Encoding = d.str(t, "build", "encoding")
		d.checkKeys("build", t, "source", "release", "encoding")
	}
	if t, ok := d.table(doc, "test"); ok {
		res.Test.Includes = d.strs(t, "test", "includes")
		res.Test.Excludes = d.strs(t, "test", "excludes")
		res.Test.Groups = d.str(t, "test", "groups")
		res.Test.ExcludedGroups = d.str(t, "test", "excludedGroups")
		res.Test.ArgLine = d.str(t, "test", "argLine")
		d.checkKeys("test", t, "includes", "excludes", "groups", "excludedGroups", "argLine")
	}
	for _, t := range d.tables(doc, "plugin") {
		res.Plugins = append(res.Plugins, PluginDecl{
			ID: d.str(t, "plugin", "id"), Version: d.str(t, "plugin", "version"), Phase: d.str(t, "plugin", "phase"),
		})
		d.checkKeys("plugin", t, "id", "version", "phase", "config")
	}
	for _, t := range d.tables(doc, "generator") {
		res.Generators = append(res.Generators, GeneratorDecl{
			ID: d.str(t, "generator", "id"), Phase: d.str(t, "generator", "phase"), Command: d.strs(t, "generator", "command"),
			Output: d.str(t, "generator", "output"), Inputs: d.strs(t, "generator", "inputs"),
		})
		d.checkKeys("generator", t, "id", "phase", "command", "output", "inputs")
	}
	d.checkKeys("", doc, "build", "test", "plugin", "generator")
	if d.err != nil {
		return JVToml{}, fmt.Errorf("%s: %w", p, d.err)
	}
	res.Warnings = d.warnings
	return res, nil
}

// decoder reads typed values out of a parsed document, remembering the first type error and any
// unrecognised keys.
type decoder struct {
	err      error
	warnings []string
}

func (d *decoder) fail(format string, a ...any) {
	if d.err == nil {
		d.err = fmt.Errorf(format, a...)
	}
}

func (d *decoder) table(m map[string]any, key string) (map[string]any, bool) {
	switch v := m[key].(type) {
	case nil:
		return nil, false
	case map[string]any:
		return v, true
	default:
		d.fail("[%s] must be a table", key)
		return nil, false
	}
}

func (d *decoder) tables(m map[string]any, key string) []map[string]any {
	switch v := m[key].(type) {
	case nil:
		return nil
	case []map[string]any:
		return v
	default:
		d.fail("%q must be written as [[%s]]", key, key)
		return nil
	}
}

// str accepts a string, or a number or boolean written bare (release = 21).
func (d *decoder) str(m map[string]any, section, key string) string {
	switch v := m[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	default:
		d.fail("%s.%s must be a string", section, key)
		return ""
	}
}

func (d *decoder) strs(m map[string]any, section, key string) []string {
	switch v := m[key].(type) {
	case nil:
		return nil
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				d.fail("%s.%s must be a list of strings", section, key)
				return nil
			}
			out = append(out, s)
		}
		return out
	default:
		d.fail("%s.%s must be a list of strings", section, key)
		return nil
	}
}

func (d *decoder) checkKeys(section string, m map[string]any, known ...string) {
	ok := map[string]bool{}
	for _, k := range known {
		ok[k] = true
	}
	var unknown []string
	for k := range m {
		if !ok[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		name := k
		if section != "" {
			name = section + "." + k
		}
		d.warnings = append(d.warnings, "unknown setting "+name)
	}
}

func (j *JVToml) PluginDeclInit() {
	if j.Plugins == nil {
		j.Plugins = []PluginDecl{}
	}
	if j.Generators == nil {
		j.Generators = []GeneratorDecl{}
	}
}
