// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package flatpak

import (
	"context"
	"strings"

	"github.com/goharbor/harbor/src/controller/artifact"
	"github.com/goharbor/harbor/src/controller/project"
	"github.com/goharbor/harbor/src/controller/repository"
	"github.com/goharbor/harbor/src/lib/errors"
	"github.com/goharbor/harbor/src/lib/q"
)

const (
	// AnnotationFlatpakRef is the OCI annotation key for Flatpak ref
	AnnotationFlatpakRef = "org.flatpak.ref"
)

// Index represents the Flatpak OCI registry index response
// See: https://github.com/flatpak/flatpak-oci-specs/blob/main/registry-index.md
type Index struct {
	Registry string             `json:"Registry"`
	Results  []IndexRepository  `json:"Results"`
}

// IndexRepository groups images and image lists by OCI repository name
type IndexRepository struct {
	Name   string           `json:"Name"`
	Images []IndexImage     `json:"Images,omitempty"`
	Lists  []IndexImageList `json:"Lists,omitempty"`
}

// IndexImage represents a single-platform OCI image in the index
type IndexImage struct {
	Digest       string            `json:"Digest"`
	MediaType    string            `json:"MediaType"`
	OS           string            `json:"OS,omitempty"`
	Architecture string            `json:"Architecture,omitempty"`
	Tags         []string          `json:"Tags,omitempty"`
	Annotations  map[string]string `json:"Annotations,omitempty"`
	Labels       map[string]string `json:"Labels,omitempty"`
}

// IndexImageList represents a multi-platform OCI image index/manifest list
type IndexImageList struct {
	Digest    string       `json:"Digest"`
	MediaType string       `json:"MediaType"`
	Tags      []string     `json:"Tags,omitempty"`
	Images    []IndexImage `json:"Images,omitempty"`
}

// QueryParams holds the optional query parameters for filtering the index
type QueryParams struct {
	Architecture []string
	OS           []string
	Tag          []string
	Label        []LabelFilter
	Annotation   []LabelFilter
}

// LabelFilter represents a label or annotation filter
type LabelFilter struct {
	Key       string
	Value     string
	ExistsOnly bool // true for ":exists=1" filters
}

// Builder builds the Flatpak index for a given project
type Builder struct {
	proCtl  project.Controller
	artCtl  artifact.Controller
	repoCtl repository.Controller
}

// NewBuilder creates a new Flatpak index builder
func NewBuilder(proCtl project.Controller, artCtl artifact.Controller, repoCtl repository.Controller) *Builder {
	return &Builder{
		proCtl:  proCtl,
		artCtl:  artCtl,
		repoCtl: repoCtl,
	}
}

// Build generates the Flatpak index for the given project
func (b *Builder) Build(ctx context.Context, registryURL, projectName string, params QueryParams) (*Index, error) {
	pro, err := b.proCtl.GetByName(ctx, projectName, project.Metadata(true))
	if err != nil {
		if errors.IsNotFoundErr(err) {
			return nil, errors.NotFoundError(errors.Errorf("project %s not found", projectName))
		}
		return nil, err
	}
	if !pro.FlatpakIndexEnabled() {
		return nil, errors.NotFoundError(errors.Errorf("flatpak index not enabled for project %s", projectName))
	}

	index := &Index{
		Registry: registryURL,
		Results:  []IndexRepository{},
	}

	repos, err := b.repoCtl.List(ctx, &q.Query{
		Keywords: map[string]any{
			"ProjectID": pro.ProjectID,
		},
	})
	if err != nil {
		return nil, err
	}

	for _, repo := range repos {
		arts, err := b.artCtl.List(ctx, &q.Query{
			Keywords: map[string]any{
				"RepositoryID": repo.RepositoryID,
			},
		}, &artifact.Option{WithTag: true})
		if err != nil {
			return nil, err
		}

		repoResult := IndexRepository{
			Name: repo.Name,
		}

		for _, art := range arts {
			if art.IsImageIndex() && len(art.References) > 0 {
				imageList := b.buildImageList(ctx, art, params)
				if imageList != nil {
					repoResult.Lists = append(repoResult.Lists, *imageList)
				}
			} else {
				img := buildImage(art, params)
				if img != nil {
					repoResult.Images = append(repoResult.Images, *img)
				}
			}
		}

		if len(repoResult.Images) > 0 || len(repoResult.Lists) > 0 {
			index.Results = append(index.Results, repoResult)
		}
	}

	return index, nil
}

// buildImageList builds an IndexImageList from an image index artifact
func (b *Builder) buildImageList(ctx context.Context, art *artifact.Artifact, params QueryParams) *IndexImageList {
	tags := extractTagNames(art)
	if len(params.Tag) > 0 && !anyMatch(tags, params.Tag) {
		return nil
	}

	list := &IndexImageList{
		Digest:    art.Digest,
		MediaType: art.ManifestMediaType,
		Tags:      tags,
	}

	for _, ref := range art.References {
		child, err := b.artCtl.Get(ctx, ref.ChildID, &artifact.Option{WithTag: false})
		if err != nil {
			continue
		}
		img := buildImage(child, params)
		if img != nil {
			list.Images = append(list.Images, *img)
		}
	}

	if len(list.Images) == 0 {
		return nil
	}
	return list
}

// buildImage builds an IndexImage from a single-platform artifact
func buildImage(art *artifact.Artifact, params QueryParams) *IndexImage {
	labels := collectLabels(art)

	// must have org.flatpak.ref to be a Flatpak image
	flatpakRef := labels[AnnotationFlatpakRef]
	if flatpakRef == "" {
		return nil
	}

	// extract OS and architecture from extra_attrs
	os := getExtraAttrString(art, "os")
	arch := getExtraAttrString(art, "architecture")

	// apply OS filter
	if len(params.OS) > 0 && os != "" && !containsString(params.OS, os) {
		return nil
	}

	// apply architecture filter (check both OCI arch and Flatpak ref arch)
	if len(params.Architecture) > 0 {
		matched := (arch != "" && containsString(params.Architecture, arch))
		if !matched {
			refArch := extractArchFromRef(flatpakRef)
			matched = containsString(params.Architecture, refArch)
		}
		if !matched {
			return nil
		}
	}

	// apply label filters
	if !matchesLabelFilters(labels, params.Label) {
		return nil
	}

	// apply annotation filters (annotations are merged into labels)
	if !matchesLabelFilters(labels, params.Annotation) {
		return nil
	}

	// split labels into annotations (OCI manifest annotations) and config labels
	annotations := make(map[string]string)
	configLabels := make(map[string]string)
	for k, v := range art.Annotations {
		annotations[k] = v
	}
	if configRaw, ok := art.ExtraAttrs["config"]; ok {
		if configMap, ok := configRaw.(map[string]any); ok {
			if labelsRaw, ok := configMap["Labels"]; ok {
				if labelsMap, ok := labelsRaw.(map[string]any); ok {
					for k, v := range labelsMap {
						if vs, ok := v.(string); ok {
							configLabels[k] = vs
						}
					}
				}
			}
		}
	}

	tags := extractTagNames(art)

	img := &IndexImage{
		Digest:    art.Digest,
		MediaType: art.ManifestMediaType,
		OS:        os,
		Architecture: arch,
		Tags:      tags,
	}
	if len(annotations) > 0 {
		img.Annotations = annotations
	}
	if len(configLabels) > 0 {
		img.Labels = configLabels
	}
	return img
}

// collectLabels merges annotations and config labels from the artifact
func collectLabels(art *artifact.Artifact) map[string]string {
	labels := make(map[string]string)

	for k, v := range art.Annotations {
		labels[k] = v
	}

	if configRaw, ok := art.ExtraAttrs["config"]; ok {
		if configMap, ok := configRaw.(map[string]any); ok {
			if labelsRaw, ok := configMap["Labels"]; ok {
				if labelsMap, ok := labelsRaw.(map[string]any); ok {
					for k, v := range labelsMap {
						if _, exists := labels[k]; !exists {
							if vs, ok := v.(string); ok {
								labels[k] = vs
							}
						}
					}
				}
			}
		}
	}

	return labels
}

// matchesLabelFilters checks whether labels satisfy all the given filters
func matchesLabelFilters(labels map[string]string, filters []LabelFilter) bool {
	for _, f := range filters {
		val, exists := labels[f.Key]
		if !exists {
			return false
		}
		if !f.ExistsOnly && val != f.Value {
			return false
		}
	}
	return true
}

// getExtraAttrString extracts a string value from artifact ExtraAttrs
func getExtraAttrString(art *artifact.Artifact, key string) string {
	if raw, ok := art.ExtraAttrs[key]; ok {
		if s, ok := raw.(string); ok {
			return s
		}
	}
	return ""
}

// extractTagNames extracts tag name strings from artifact tags
func extractTagNames(art *artifact.Artifact) []string {
	var names []string
	for _, t := range art.Tags {
		names = append(names, t.Name)
	}
	return names
}

// anyMatch returns true if any element of a is also in b
func anyMatch(a, b []string) bool {
	for _, av := range a {
		for _, bv := range b {
			if av == bv {
				return true
			}
		}
	}
	return false
}

// extractArchFromRef parses the architecture from a Flatpak ref string
// Format: "app/com.example.App/x86_64/stable"
func extractArchFromRef(ref string) string {
	parts := strings.Split(ref, "/")
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

// extractNameFromRef extracts the app ID from a Flatpak ref
func extractNameFromRef(ref string) string {
	parts := strings.Split(ref, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return ref
}

// containsString checks if a string slice contains a specific string
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}
