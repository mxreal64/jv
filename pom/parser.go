// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 jv contributors

package pom

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
)

type Exclusion struct {
	GroupID    string `xml:"groupId"`
	ArtifactID string `xml:"artifactId"`
}

type Dependency struct {
	GroupID    string      `xml:"groupId"`
	ArtifactID string      `xml:"artifactId"`
	Version    string      `xml:"version"`
	Type       string      `xml:"type"`
	Classifier string      `xml:"classifier"`
	Scope      string      `xml:"scope"`
	Optional   string      `xml:"optional"`
	Exclusions []Exclusion `xml:"exclusions>exclusion"`
}

// Key is Maven's management/conflict key: groupId:artifactId:type:classifier.
func (d Dependency) Key() string {
	t := d.Type
	if t == "" {
		t = "jar"
	}
	return d.GroupID + ":" + d.ArtifactID + ":" + t + ":" + d.Classifier
}

func (d *Dependency) trim() {
	for _, p := range []*string{&d.GroupID, &d.ArtifactID, &d.Version, &d.Type, &d.Classifier, &d.Scope, &d.Optional} {
		*p = strings.TrimSpace(*p)
	}
	for i := range d.Exclusions {
		d.Exclusions[i].GroupID = strings.TrimSpace(d.Exclusions[i].GroupID)
		d.Exclusions[i].ArtifactID = strings.TrimSpace(d.Exclusions[i].ArtifactID)
	}
}

type DependencyManagement struct {
	Dependencies []Dependency `xml:"dependencies>dependency"`
}

type Parent struct {
	GroupID      string  `xml:"groupId"`
	ArtifactID   string  `xml:"artifactId"`
	Version      string  `xml:"version"`
	RelativePath *string `xml:"relativePath"`
}

type Repository struct {
	ID  string `xml:"id"`
	URL string `xml:"url"`
}

type Property struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
}

type Properties struct {
	Items []Property `xml:",any"`
}

type PropActivation struct {
	Name  string `xml:"name"`
	Value string `xml:"value"`
}

type OSActivation struct {
	Name    string `xml:"name"`
	Family  string `xml:"family"`
	Arch    string `xml:"arch"`
	Version string `xml:"version"`
}

type FileActivation struct {
	Exists  string `xml:"exists"`
	Missing string `xml:"missing"`
}

type Activation struct {
	ActiveByDefault string          `xml:"activeByDefault"`
	JDK             string          `xml:"jdk"`
	Property        *PropActivation `xml:"property"`
	OS              *OSActivation   `xml:"os"`
	File            *FileActivation `xml:"file"`
}

type Profile struct {
	ID                   string               `xml:"id"`
	Activation           Activation           `xml:"activation"`
	Modules              []string             `xml:"modules>module"`
	Properties           Properties           `xml:"properties"`
	Repositories         []Repository         `xml:"repositories>repository"`
	Dependencies         []Dependency         `xml:"dependencies>dependency"`
	DependencyManagement DependencyManagement `xml:"dependencyManagement"`
}

type PluginExecution struct {
	ID    string   `xml:"id"`
	Phase string   `xml:"phase"`
	Goals []string `xml:"goals>goal"`
}

type PluginConfigChild struct {
	XMLName xml.Name
}

// PluginConfig keeps the few compiler settings jv understands and records every
// other child element so unsupported configuration can be detected.
type PluginConfig struct {
	Release  string              `xml:"release"`
	Source   string              `xml:"source"`
	Target   string              `xml:"target"`
	Encoding string              `xml:"encoding"`
	Other    []PluginConfigChild `xml:",any"`
}

type Plugin struct {
	GroupID       string            `xml:"groupId"`
	ArtifactID    string            `xml:"artifactId"`
	Configuration PluginConfig      `xml:"configuration"`
	Executions    []PluginExecution `xml:"executions>execution"`
}

type Resource struct {
	Directory string   `xml:"directory"`
	Filtering string   `xml:"filtering"`
	Includes  []string `xml:"includes>include"`
	Excludes  []string `xml:"excludes>exclude"`
}

type Build struct {
	SourceDirectory     string     `xml:"sourceDirectory"`
	TestSourceDirectory string     `xml:"testSourceDirectory"`
	FinalName           string     `xml:"finalName"`
	Resources           []Resource `xml:"resources>resource"`
	TestResources       []Resource `xml:"testResources>testResource"`
	Plugins             []Plugin   `xml:"plugins>plugin"`
}

func resourcesUnsupported(rs []Resource, def string) bool {
	for _, r := range rs {
		d := strings.Trim(strings.TrimSpace(r.Directory), "/")
		if (d != "" && d != def) || strings.TrimSpace(r.Filtering) == "true" || len(r.Includes) > 0 || len(r.Excludes) > 0 {
			return true
		}
	}
	return false
}

var ignorableCompilerConfig = map[string]bool{"showWarnings": true, "showDeprecation": true, "optimize": true}

// Unsupported lists <build> settings that change what Maven would produce but that jv
// does not honour, so callers can delegate to mvn (or warn) instead of silently
// building something different.
func (b Build) Unsupported() []string {
	var out []string
	if strings.TrimSpace(b.SourceDirectory) != "" {
		out = append(out, "build/sourceDirectory")
	}
	if strings.TrimSpace(b.TestSourceDirectory) != "" {
		out = append(out, "build/testSourceDirectory")
	}
	if strings.TrimSpace(b.FinalName) != "" {
		out = append(out, "build/finalName")
	}
	if resourcesUnsupported(b.Resources, "src/main/resources") {
		out = append(out, "build/resources")
	}
	if resourcesUnsupported(b.TestResources, "src/test/resources") {
		out = append(out, "build/testResources")
	}
	for _, p := range b.Plugins {
		name := strings.TrimSpace(p.ArtifactID)
		isCompiler := name == "maven-compiler-plugin"
		c := p.Configuration
		var extra []string
		for _, o := range c.Other {
			if isCompiler && ignorableCompilerConfig[o.XMLName.Local] {
				continue
			}
			extra = append(extra, o.XMLName.Local)
		}
		if !isCompiler && (strings.TrimSpace(c.Release) != "" || strings.TrimSpace(c.Source) != "" || strings.TrimSpace(c.Target) != "" || strings.TrimSpace(c.Encoding) != "") {
			extra = append(extra, "release/source/target/encoding")
		}
		if len(extra) > 0 {
			out = append(out, name+": configuration ("+strings.Join(extra, ", ")+")")
		}
		if len(p.Executions) > 0 {
			out = append(out, name+": executions")
		}
	}
	return out
}

// CompilerProps returns the maven-compiler-plugin settings jv understands, as the
// equivalent Maven properties.
func (b Build) CompilerProps() map[string]string {
	out := map[string]string{}
	for _, p := range b.Plugins {
		if strings.TrimSpace(p.ArtifactID) != "maven-compiler-plugin" {
			continue
		}
		c := p.Configuration
		if v := strings.TrimSpace(c.Release); v != "" {
			out["maven.compiler.release"] = v
		}
		if v := strings.TrimSpace(c.Source); v != "" {
			out["maven.compiler.source"] = v
		}
		if v := strings.TrimSpace(c.Target); v != "" {
			out["maven.compiler.target"] = v
		}
		if v := strings.TrimSpace(c.Encoding); v != "" {
			out["project.build.sourceEncoding"] = v
		}
	}
	return out
}

type Model struct {
	XMLName              xml.Name             `xml:"project"`
	Parent               *Parent              `xml:"parent"`
	GroupID              string               `xml:"groupId"`
	ArtifactID           string               `xml:"artifactId"`
	Version              string               `xml:"version"`
	Packaging            string               `xml:"packaging"`
	Modules              []string             `xml:"modules>module"`
	Properties           Properties           `xml:"properties"`
	Repositories         []Repository         `xml:"repositories>repository"`
	Dependencies         []Dependency         `xml:"dependencies>dependency"`
	DependencyManagement DependencyManagement `xml:"dependencyManagement"`
	Profiles             []Profile            `xml:"profiles>profile"`
	Build                Build                `xml:"build"`
}

// PropMap returns the <properties> block as a map.
func (m *Model) PropMap() map[string]string {
	out := make(map[string]string, len(m.Properties.Items))
	for _, p := range m.Properties.Items {
		out[p.XMLName.Local] = strings.TrimSpace(p.Value)
	}
	return out
}

func (m *Model) normalize() {
	m.GroupID = strings.TrimSpace(m.GroupID)
	m.ArtifactID = strings.TrimSpace(m.ArtifactID)
	m.Version = strings.TrimSpace(m.Version)
	m.Packaging = strings.TrimSpace(m.Packaging)
	if m.Packaging == "" {
		m.Packaging = "jar"
	}
	if m.Parent != nil {
		m.Parent.GroupID = strings.TrimSpace(m.Parent.GroupID)
		m.Parent.ArtifactID = strings.TrimSpace(m.Parent.ArtifactID)
		m.Parent.Version = strings.TrimSpace(m.Parent.Version)
		if m.GroupID == "" {
			m.GroupID = m.Parent.GroupID
		}
		if m.Version == "" {
			m.Version = m.Parent.Version
		}
	}
	for i := range m.Dependencies {
		m.Dependencies[i].trim()
	}
	for i := range m.DependencyManagement.Dependencies {
		m.DependencyManagement.Dependencies[i].trim()
	}
	for i := range m.Profiles {
		for j := range m.Profiles[i].Dependencies {
			m.Profiles[i].Dependencies[j].trim()
		}
		for j := range m.Profiles[i].DependencyManagement.Dependencies {
			m.Profiles[i].DependencyManagement.Dependencies[j].trim()
		}
	}
	for i := range m.Repositories {
		m.Repositories[i].ID = strings.TrimSpace(m.Repositories[i].ID)
		m.Repositories[i].URL = strings.TrimSpace(m.Repositories[i].URL)
	}
}

// Many old POMs declare ISO-8859-1; Go only handles UTF-8 natively.
func charsetReader(charset string, in io.Reader) (io.Reader, error) {
	switch strings.ToLower(charset) {
	case "iso-8859-1", "latin1", "windows-1252", "cp1252", "us-ascii", "ascii":
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, err
		}
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		return strings.NewReader(string(r)), nil
	}
	return in, nil
}

func ParsePOMBytes(data []byte) (*Model, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	dec.CharsetReader = charsetReader
	var m Model
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	m.normalize()
	return &m, nil
}

func ParsePOM(path string) (*Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParsePOMBytes(data)
}

// Interpolate resolves ${name} references in val using props (up to 10 passes for chained refs).
// It avoids regexp by scanning for ${ and } manually; this is the hot path for large BOMs.
func Interpolate(val string, props map[string]string) string {
	for range 10 {
		if !strings.Contains(val, "${") {
			return val
		}
		var sb strings.Builder
		changed, rest := false, val
		for {
			s := strings.Index(rest, "${")
			if s < 0 {
				sb.WriteString(rest)
				break
			}
			e := strings.IndexByte(rest[s:], '}')
			if e < 0 {
				sb.WriteString(rest)
				break
			}
			sb.WriteString(rest[:s])
			key := rest[s+2 : s+e]
			if v, ok := props[key]; ok && key != "" {
				sb.WriteString(v)
				changed = true
			} else {
				sb.WriteString(rest[s : s+e+1])
			}
			rest = rest[s+e+1:]
		}
		if !changed {
			return val
		}
		val = sb.String()
	}
	return val
}

func overlayDeps(base, over []Dependency) []Dependency {
	out := append([]Dependency{}, base...)
	idx := map[string]int{}
	for i, d := range out {
		idx[d.Key()] = i
	}
	for _, d := range over {
		if i, ok := idx[d.Key()]; ok {
			out[i] = d
		} else {
			idx[d.Key()] = len(out)
			out = append(out, d)
		}
	}
	return out
}
